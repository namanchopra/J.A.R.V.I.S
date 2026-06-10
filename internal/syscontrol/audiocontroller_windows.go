//go:build windows

// audiocontroller_windows.go — Windows backend for syscontrol.AudioController.
//
// Talks to the Core Audio MMDevice API to obtain the default render (playback)
// endpoint, then activates the IAudioEndpointVolume COM interface to set the
// master volume scalar and toggle the mute flag. This is the same path the
// Settings → System → Sound slider takes, so changes are coherent with the
// rest of the Windows audio stack and survive volume-mixer redraws.
//
// Call graph for SetVolume:
//
//	CoInitializeEx(NULL, COINIT_APARTMENTTHREADED)
//	  -> CoCreateInstance(CLSID_MMDeviceEnumerator, IID_IMMDeviceEnumerator)
//	  -> IMMDeviceEnumerator::GetDefaultAudioEndpoint(eRender, eConsole, &dev)
//	  -> IMMDevice::Activate(IID_IAudioEndpointVolume, CLSCTX_ALL, NULL, &vol)
//	  -> IAudioEndpointVolume::SetMasterVolumeLevelScalar(level, NULL)
//
// Mute/Unmute use the same activation path then invoke
// IAudioEndpointVolume::SetMute. SetMute is idempotent in Core Audio — calling
// it with the current state is a no-op at the OS layer — which matches the
// macOS Mute/Unmute semantics required by the interface contract.
//
// All COM failures are wrapped with the method name and surface to the caller
// as a regular error; we never panic on a missing endpoint, no audio stack,
// or a CoInitialize failure on a service-locked SKU. The interface contract
// (audiocontroller.go) and TASK-022 AC #3 both require graceful degradation.
package syscontrol

import (
	"fmt"
	"log/slog"
	"math"
	"sync"
	"syscall"
	"unsafe"

	ole "github.com/go-ole/go-ole"
)

// MMDevice / IAudioEndpointVolume GUIDs. The string forms below match the
// canonical values in mmdeviceapi.h and endpointvolume.h shipped with the
// Windows SDK. Kept package-private (initialised once) because go-ole's
// NewGUID parses the textual form each call and we'd rather pay that cost
// at package init than on every SetVolume call.
var (
	audioClsidMMDeviceEnumerator = ole.NewGUID("BCDE0395-E52F-467C-8E3D-C4579291692E")
	audioIIDIMMDeviceEnumerator  = ole.NewGUID("A95664D2-9614-4F35-A746-DE8DB63617E6")
	audioIIDIAudioEndpointVolume = ole.NewGUID("5CDF2C82-841E-4546-9722-0CF74078229A")
)

// Constants from mmdeviceapi.h / objbase.h. The vtable indices below come from
// endpointvolume.h — the IAudioEndpointVolume layout is stable since Vista and
// is not expected to change (Microsoft adds new endpoint volume features via
// sibling interfaces like IAudioEndpointVolumeEx instead).
const (
	// EDataFlow.eRender — playback endpoints (speakers/headphones).
	audioEDataFlowRender = 0
	// ERole.eConsole — the "system default" role used for general audio.
	audioERoleConsole = 0
	// CLSCTX_ALL — the activation context used by IMMDevice::Activate when
	// instantiating the IAudioEndpointVolume interface on the endpoint.
	audioCLSCTXAll = 0x17
	// CoInitializeEx success codes we treat as "COM is already initialised
	// on this thread in a compatible mode" — neither requires us to call
	// CoUninitialize on the way out (the caller owned the prior init).
	audioSFalse          = 0x00000001 // S_FALSE
	audioRPCEChangedMode = 0x80010106 // RPC_E_CHANGED_MODE
)

// audioInitOnce guarantees we only resolve the lazy DLL procs (currently
// none — IAudioEndpointVolume has no direct stdcall fall-backs) and emit the
// startup log line once per process. Kept around so that adding lazy procs
// in future remains a one-line change in audioInit.
var audioInitOnce sync.Once

func audioInit() {
	audioInitOnce.Do(func() {
		slog.Debug("syscontrol/audio: Windows backend initialised")
	})
}

// immDeviceEnumeratorAudioVtbl mirrors the IMMDeviceEnumerator vtable from
// mmdeviceapi.h. We only invoke GetDefaultAudioEndpoint; the other slots are
// typed as uintptr placeholders so the offset of the slot we call is correct.
// The leading IUnknownVtbl embeds QueryInterface/AddRef/Release in the right
// order — every COM vtable starts with those three slots.
type immDeviceEnumeratorAudioVtbl struct {
	ole.IUnknownVtbl
	EnumAudioEndpoints                     uintptr
	GetDefaultAudioEndpoint                uintptr
	GetDevice                              uintptr
	RegisterEndpointNotificationCallback   uintptr
	UnregisterEndpointNotificationCallback uintptr
}

// immDeviceAudioVtbl mirrors the IMMDevice vtable from mmdeviceapi.h. We only
// invoke Activate; everything else is placeholder padding.
type immDeviceAudioVtbl struct {
	ole.IUnknownVtbl
	Activate          uintptr
	OpenPropertyStore uintptr
	GetId             uintptr
	GetState          uintptr
}

// iAudioEndpointVolumeVtbl mirrors the IAudioEndpointVolume vtable from
// endpointvolume.h. The slot ordering is load-bearing — calling SetMute at a
// SetMasterVolumeLevelScalar offset would, at best, fail with E_INVALIDARG
// and, at worst, corrupt the volume slider. Each typed slot below has its
// offset verified against the public Windows SDK header.
type iAudioEndpointVolumeVtbl struct {
	ole.IUnknownVtbl
	RegisterControlChangeNotify   uintptr // 3
	UnregisterControlChangeNotify uintptr // 4
	GetChannelCount               uintptr // 5
	SetMasterVolumeLevel          uintptr // 6
	SetMasterVolumeLevelScalar    uintptr // 7 — we call this
	GetMasterVolumeLevel          uintptr // 8
	GetMasterVolumeLevelScalar    uintptr // 9
	SetChannelVolumeLevel         uintptr // 10
	SetChannelVolumeLevelScalar   uintptr // 11
	GetChannelVolumeLevel         uintptr // 12
	GetChannelVolumeLevelScalar   uintptr // 13
	SetMute                       uintptr // 14 — we call this
	GetMute                       uintptr // 15
	GetVolumeStepInfo             uintptr // 16
	VolumeStepUp                  uintptr // 17
	VolumeStepDown                uintptr // 18
	QueryHardwareSupport          uintptr // 19
	GetVolumeRange                uintptr // 20
}

// WindowsAudioController is the IAudioEndpointVolume-backed implementation of
// syscontrol.AudioController. It carries no per-instance state — every call
// re-opens the default endpoint so a mid-session audio-device hot-swap is
// picked up automatically (matching the macOS osascript backend, which also
// re-resolves "output volume" on every call).
//
// The zero value is usable; NewWindowsAudioController exists only to mirror
// the constructor pattern used elsewhere in the codebase
// (NewWindowsTerminalProvider, NewController in macctl) and to give us a
// future hook for injecting a test seam without breaking callers.
type WindowsAudioController struct{}

// NewWindowsAudioController returns an AudioController backed by the Windows
// Core Audio IAudioEndpointVolume COM interface.
func NewWindowsAudioController() *WindowsAudioController {
	audioInit()
	return &WindowsAudioController{}
}

// Compile-time assertion that *WindowsAudioController satisfies the cross-
// platform AudioController interface declared in audiocontroller.go. Any
// signature drift on either side fails the Windows build immediately.
var _ AudioController = (*WindowsAudioController)(nil)

// SetVolume sets the default render endpoint's master volume to pct (0..100).
//
// Out-of-range inputs are rejected before any COM activation — a stray voice
// misfire ("set volume to a hundred and fifty") must never reach the OS
// (matches the macOS reference in internal/macctl/audio.go).
//
// IAudioEndpointVolume::SetMasterVolumeLevelScalar takes a float32 in [0.0,
// 1.0]; we divide pct by 100.0 to convert. We pass a NULL event-context GUID
// (NULL pguidEventContext) — Windows will then emit a generic change event
// to other listeners (volume mixer, hotkeys etc.).
func (c *WindowsAudioController) SetVolume(pct int) (string, error) {
	if pct < 0 || pct > 100 {
		return "", fmt.Errorf("SetVolume(%d): pct must be in 0..100", pct)
	}
	endpoint, releaseEndpoint, releaseCOM, err := openDefaultRenderEndpoint()
	if err != nil {
		return "", fmt.Errorf("SetVolume: %w", err)
	}
	defer releaseCOM()
	defer releaseEndpoint()

	vt := (*iAudioEndpointVolumeVtbl)(unsafe.Pointer(endpoint.RawVTable))
	level := float32(pct) / 100.0
	// SetMasterVolumeLevelScalar(float level, LPCGUID pguidEventContext).
	// math.Float32bits reinterprets the float32 as its IEEE-754 binary32
	// integer representation; on Windows amd64 the Go runtime's SyscallN
	// mirrors the integer-register copy into XMM0 so the Windows fastcall
	// calling convention sees a real float in the FP register. We never
	// target 386 (the project builds amd64/arm64 only) so this recipe —
	// the same one go-wca uses — is correct on all our targets.
	hr, _, _ := syscall.SyscallN(
		vt.SetMasterVolumeLevelScalar,
		uintptr(unsafe.Pointer(endpoint)),
		uintptr(math.Float32bits(level)),
		0, // NULL pguidEventContext
	)
	if hr != 0 {
		return "", fmt.Errorf("SetVolume(%d): SetMasterVolumeLevelScalar HRESULT=0x%08x", pct, uint32(hr))
	}
	return "", nil
}

// Mute sets the default render endpoint's mute flag to TRUE. Idempotent —
// muting an already-muted device is a no-op at the COM layer; implementations
// MUST NOT surface that as an error (audiocontroller.go contract).
func (c *WindowsAudioController) Mute() (string, error) {
	if err := setMute(true); err != nil {
		return "", fmt.Errorf("Mute: %w", err)
	}
	return "", nil
}

// Unmute clears the default render endpoint's mute flag. Counterpart to Mute;
// equally idempotent.
func (c *WindowsAudioController) Unmute() (string, error) {
	if err := setMute(false); err != nil {
		return "", fmt.Errorf("Unmute: %w", err)
	}
	return "", nil
}

// setMute is the shared helper for Mute/Unmute. Extracted because the COM
// dance to obtain IAudioEndpointVolume is identical for both — the only
// difference is the BOOL we pass to SetMute.
//
// IAudioEndpointVolume::SetMute(BOOL bMute, LPCGUID pguidEventContext). BOOL
// on Windows is a 32-bit int (1 == TRUE, 0 == FALSE). We pass NULL for the
// event context so the standard volume change notification fires.
func setMute(mute bool) error {
	endpoint, releaseEndpoint, releaseCOM, err := openDefaultRenderEndpoint()
	if err != nil {
		return err
	}
	defer releaseCOM()
	defer releaseEndpoint()

	vt := (*iAudioEndpointVolumeVtbl)(unsafe.Pointer(endpoint.RawVTable))
	var bMute uintptr
	if mute {
		bMute = 1
	}
	hr, _, _ := syscall.SyscallN(
		vt.SetMute,
		uintptr(unsafe.Pointer(endpoint)),
		bMute,
		0, // NULL pguidEventContext
	)
	if hr != 0 {
		return fmt.Errorf("SetMute HRESULT=0x%08x", uint32(hr))
	}
	return nil
}

// openDefaultRenderEndpoint walks the standard MMDevice → IMMDevice →
// IAudioEndpointVolume call graph and returns the IAudioEndpointVolume COM
// pointer along with two cleanup closures:
//
//   - releaseEndpoint releases the IAudioEndpointVolume + IMMDevice references.
//   - releaseCOM matches our CoInitializeEx: it calls CoUninitialize only when
//     we are the owner of the COM apartment for this thread (i.e. we got a
//     clean S_OK from CoInitializeEx, not an "already-init" status).
//
// Both closures are always safe to call (they may be no-ops). Callers MUST
// defer releaseEndpoint before releaseCOM — releasing a COM object after
// CoUninitialize is undefined.
//
// Errors are returned without wrapping; callers prefix with their own method
// name so the audit trail reads "SetVolume: openDefaultRenderEndpoint: ...".
func openDefaultRenderEndpoint() (endpoint *ole.IUnknown, releaseEndpoint, releaseCOM func(), err error) {
	releaseEndpoint = func() {}
	releaseCOM = func() {}

	owns := false
	if initErr := ole.CoInitializeEx(0, ole.COINIT_APARTMENTTHREADED); initErr != nil {
		// CoInitializeEx returns an *OleError when hr != 0. S_FALSE means COM
		// was already up on this thread with the same model; RPC_E_CHANGED_MODE
		// means it's up with a *different* model (we proceed anyway because
		// any subsequent COM call will fail with a clear HRESULT we can wrap).
		if oerr, ok := initErr.(*ole.OleError); ok {
			hr := uint32(oerr.Code())
			if hr != audioSFalse && hr != audioRPCEChangedMode {
				return nil, releaseEndpoint, releaseCOM, fmt.Errorf("CoInitializeEx: %w", initErr)
			}
		} else {
			return nil, releaseEndpoint, releaseCOM, fmt.Errorf("CoInitializeEx: %w", initErr)
		}
	} else {
		owns = true
	}
	if owns {
		releaseCOM = func() { ole.CoUninitialize() }
	}

	enum, createErr := ole.CreateInstance(audioClsidMMDeviceEnumerator, audioIIDIMMDeviceEnumerator)
	if createErr != nil || enum == nil {
		// Treat both an error and a nil interface as "no audio stack"; the
		// latter happens on Server SKUs where the MMDevice service is
		// disabled. We never panic on this path — TASK-022 AC #3.
		if createErr == nil {
			createErr = fmt.Errorf("nil IMMDeviceEnumerator")
		}
		return nil, releaseEndpoint, releaseCOM, fmt.Errorf("CoCreateInstance(MMDeviceEnumerator): %w", createErr)
	}
	defer enum.Release()

	enumVT := (*immDeviceEnumeratorAudioVtbl)(unsafe.Pointer(enum.RawVTable))

	// GetDefaultAudioEndpoint(eRender, eConsole, &pDevice).
	var device *ole.IUnknown
	hr, _, _ := syscall.SyscallN(
		enumVT.GetDefaultAudioEndpoint,
		uintptr(unsafe.Pointer(enum)),
		uintptr(audioEDataFlowRender),
		uintptr(audioERoleConsole),
		uintptr(unsafe.Pointer(&device)),
	)
	if hr != 0 || device == nil {
		// E_NOTFOUND (0x80070490) is the canonical "no default playback
		// endpoint" HRESULT — happens on headless / no-audio-driver SKUs
		// and matches TASK-022's "no endpoint" failure case.
		return nil, releaseEndpoint, releaseCOM, fmt.Errorf("GetDefaultAudioEndpoint HRESULT=0x%08x", uint32(hr))
	}

	devVT := (*immDeviceAudioVtbl)(unsafe.Pointer(device.RawVTable))

	// Activate(REFIID iid, DWORD dwClsCtx, PROPVARIANT *pActivationParams,
	//          void **ppInterface). pActivationParams is NULL for IAudioEndpointVolume.
	var iAudioEndpoint *ole.IUnknown
	hr2, _, _ := syscall.SyscallN(
		devVT.Activate,
		uintptr(unsafe.Pointer(device)),
		uintptr(unsafe.Pointer(audioIIDIAudioEndpointVolume)),
		uintptr(audioCLSCTXAll),
		0, // NULL pActivationParams
		uintptr(unsafe.Pointer(&iAudioEndpoint)),
	)
	if hr2 != 0 || iAudioEndpoint == nil {
		device.Release()
		return nil, releaseEndpoint, releaseCOM, fmt.Errorf("IMMDevice::Activate(IAudioEndpointVolume) HRESULT=0x%08x", uint32(hr2))
	}

	releaseEndpoint = func() {
		iAudioEndpoint.Release()
		device.Release()
	}
	return iAudioEndpoint, releaseEndpoint, releaseCOM, nil
}
