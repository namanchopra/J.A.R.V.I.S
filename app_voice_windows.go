//go:build windows

// app_voice_windows.go — Windows Core Audio mic enumeration via the
// IMMDeviceEnumerator COM interface (MMDevice API, Vista+).
//
// We talk to the Core Audio MMDevice API directly via COM so the enumeration
// is consistent with what the rest of the Windows audio stack sees (the same
// list users get under Settings → System → Sound → Input). The library path
// is:
//
//   CoInitializeEx(NULL, COINIT_APARTMENTTHREADED)
//     -> CoCreateInstance(CLSID_MMDeviceEnumerator, IID_IMMDeviceEnumerator)
//     -> IMMDeviceEnumerator::EnumAudioEndpoints(eCapture, DEVICE_STATE_ACTIVE)
//     -> IMMDeviceCollection::GetCount + Item(i)
//        for each IMMDevice:
//          GetId(&wcharPtr)         // stable endpoint GUID, survives reboot
//          OpenPropertyStore(STGM_READ, &store)
//          store.GetValue(PKEY_Device_FriendlyName, &propVariant)
//     -> IMMDeviceEnumerator::GetDefaultAudioEndpoint(eCapture, eConsole)
//        used to flag IsDefault on the matching ID
//
// Device IDs returned to the frontend are the raw endpoint strings from
// IMMDevice::GetId (e.g. `{0.0.1.00000000}.{abc123...}`) which are guaranteed
// stable across reboots by the OS — this satisfies TASK-010 AC #2.
//
// On any failure (no audio stack, no devices, CoInitialize fails on a
// service-locked SKU) we return `[]AudioDevice{}` rather than nil so Wails
// serialises an empty JSON array to the frontend rather than `null`
// (TASK-010 AC #3).
//
// This file replaces the generic non-darwin placeholder in app_voice_other.go
// for Windows builds (the placeholder's build tag is `!darwin && !windows`,
// so the two files are mutually exclusive at compile time).
package main

import (
	"log/slog"
	"syscall"
	"unicode/utf16"
	"unsafe"

	ole "github.com/go-ole/go-ole"
)

// MMDevice API GUIDs. The string forms below match the canonical values in
// mmdeviceapi.h / functiondiscoverykeys_devpkey.h.
var (
	clsidMMDeviceEnumerator = ole.NewGUID("BCDE0395-E52F-467C-8E3D-C4579291692E")
	iidIMMDeviceEnumerator  = ole.NewGUID("A95664D2-9614-4F35-A746-DE8DB63617E6")

	// PKEY_Device_FriendlyName  = {a45c254e-df1c-4efd-8020-67d146a850e0}, pid 14
	// The IPropertyStore::GetValue API takes a PROPERTYKEY (GUID + DWORD).
	pkeyDeviceFriendlyName = propertyKey{
		fmtID: *ole.NewGUID("a45c254e-df1c-4efd-8020-67d146a850e0"),
		pID:   14,
	}
)

// Constants from mmdeviceapi.h / objbase.h.
const (
	// EDataFlow
	eCapture = 1
	// ERole
	eConsole = 0
	// DEVICE_STATE_ACTIVE — only show endpoints that are plugged in + enabled.
	deviceStateActive = 0x00000001
	// STGM_READ — open IPropertyStore read-only.
	stgmRead = 0
	// PROPVARIANT vt values we care about.
	vtLPWSTR = 31
	// CoInitializeEx return code indicating COM was already initialised on
	// this thread with a compatible model. Treat as success.
	sFalse = 0x00000001
	rpcEChangedMode = 0x80010106 // RPC_E_CHANGED_MODE
)

// propertyKey mirrors the Win32 PROPERTYKEY struct: a GUID followed by a
// DWORD property id. Layout must match the C struct exactly (16 bytes GUID +
// 4 bytes pid + 4 bytes implicit padding on 64-bit) — Go's layout for this
// definition is wire-compatible because the GUID embedding is a plain struct
// and the trailing uint32 leaves room for natural alignment.
type propertyKey struct {
	fmtID ole.GUID
	pID   uint32
}

// propVariant is a deliberately small subset of the Win32 PROPVARIANT union:
// we only need to read VT_LPWSTR (friendly-name strings). The full union is
// much larger but only the first 16 bytes (vt+reserved+data pointer) are
// touched by IPropertyStore::GetValue when the variant type is LPWSTR, so a
// 24-byte struct (with 8 bytes trailing padding for safety on 64-bit) is
// safe to pass. We use *uint16 for the pointer field so go-vet's unsafeptr
// analyser does not flag the uintptr→unsafe.Pointer reinterpretation when we
// dereference the LPWSTR; the COM-allocated buffer is GC-invisible so this
// typed pointer never participates in Go heap management.
type propVariant struct {
	vt         uint16
	wReserved1 uint16
	wReserved2 uint16
	wReserved3 uint16
	val        *uint16 // for VT_LPWSTR this is the LPWSTR (UTF-16) buffer
	_          uintptr // padding to match sizeof(PROPVARIANT) on 64-bit
}

// IMMDeviceEnumerator vtable. Order matches mmdeviceapi.h exactly — the
// first three slots are IUnknown methods, then the IMMDeviceEnumerator
// specifics. Only the slots we actually invoke are typed; the rest are
// kept as uintptr for clarity.
type immDeviceEnumeratorVtbl struct {
	ole.IUnknownVtbl
	EnumAudioEndpoints              uintptr
	GetDefaultAudioEndpoint         uintptr
	GetDevice                       uintptr
	RegisterEndpointNotificationCallback   uintptr
	UnregisterEndpointNotificationCallback uintptr
}

type immDeviceCollectionVtbl struct {
	ole.IUnknownVtbl
	GetCount uintptr
	Item     uintptr
}

type immDeviceVtbl struct {
	ole.IUnknownVtbl
	Activate          uintptr
	OpenPropertyStore uintptr
	GetId             uintptr
	GetState          uintptr
}

type iPropertyStoreVtbl struct {
	ole.IUnknownVtbl
	GetCount uintptr
	GetAt    uintptr
	GetValue uintptr
	SetValue uintptr
	Commit   uintptr
}

// procCoTaskMemFree is used to free LPWSTR buffers returned by
// IMMDevice::GetId. We resolve it at package init via a Windows syscall
// rather than via go-ole (which keeps the freer private). Failing to free
// these would leak a few hundred bytes per Settings open — small, but the
// daemon long-runs so we honour the contract.
var (
	modOle32           = syscall.NewLazyDLL("ole32.dll")
	procCoTaskMemFree  = modOle32.NewProc("CoTaskMemFree")
	procPropVariantInit  = modOle32.NewProc("PropVariantInit")
	procPropVariantClear = modOle32.NewProc("PropVariantClear")
)

// enumerateAudioInputs returns the list of active microphone endpoints the
// Windows MMDevice API reports. Returns `[]AudioDevice{}` (never nil) on any
// failure so the frontend always receives a JSON array.
func enumerateAudioInputs() []AudioDevice {
	// Always return a non-nil slice so the Wails binding never serialises
	// `null` into the Settings dropdown payload (TASK-010 AC #3).
	devices := make([]AudioDevice, 0, 4)

	// COM apartment initialisation. STA matches the rest of Wails' Windows
	// thread setup (the main thread runs STA). If COM was already initialised
	// in a compatible mode we get S_FALSE or RPC_E_CHANGED_MODE back — both
	// are recoverable and we proceed without calling CoUninitialize on the
	// way out (the caller owned the prior init).
	owns := false
	if err := ole.CoInitializeEx(0, ole.COINIT_APARTMENTTHREADED); err != nil {
		// CoInitializeEx returns an *OleError when hr != 0. We treat S_FALSE
		// and RPC_E_CHANGED_MODE as "COM is already up" and continue without
		// taking ownership of the uninit.
		if oerr, ok := err.(*ole.OleError); ok {
			hr := uint32(oerr.Code())
			if hr != sFalse && hr != rpcEChangedMode {
				slog.Debug("enumerateAudioInputs: CoInitializeEx failed", "err", err)
				return devices
			}
		} else {
			slog.Debug("enumerateAudioInputs: CoInitializeEx failed", "err", err)
			return devices
		}
	} else {
		owns = true
	}
	if owns {
		defer ole.CoUninitialize()
	}

	unk, err := ole.CreateInstance(clsidMMDeviceEnumerator, iidIMMDeviceEnumerator)
	if err != nil || unk == nil {
		slog.Debug("enumerateAudioInputs: CoCreateInstance(MMDeviceEnumerator) failed", "err", err)
		return devices
	}
	defer unk.Release()

	enumVT := (*immDeviceEnumeratorVtbl)(unsafe.Pointer(unk.RawVTable))

	// EnumAudioEndpoints(eCapture, DEVICE_STATE_ACTIVE, &pCollection).
	var pCollection *ole.IUnknown
	hr, _, _ := syscall.SyscallN(
		enumVT.EnumAudioEndpoints,
		uintptr(unsafe.Pointer(unk)),
		uintptr(eCapture),
		uintptr(deviceStateActive),
		uintptr(unsafe.Pointer(&pCollection)),
	)
	if hr != 0 || pCollection == nil {
		slog.Debug("enumerateAudioInputs: EnumAudioEndpoints failed", "hr", hr)
		return devices
	}
	defer pCollection.Release()

	collVT := (*immDeviceCollectionVtbl)(unsafe.Pointer(pCollection.RawVTable))

	// GetDefaultAudioEndpoint(eCapture, eConsole, &pDefaultDevice). Best-effort —
	// if this fails we just skip the IsDefault flag.
	var defaultID string
	var pDefault *ole.IUnknown
	if hrd, _, _ := syscall.SyscallN(
		enumVT.GetDefaultAudioEndpoint,
		uintptr(unsafe.Pointer(unk)),
		uintptr(eCapture),
		uintptr(eConsole),
		uintptr(unsafe.Pointer(&pDefault)),
	); hrd == 0 && pDefault != nil {
		defaultID = deviceGetID(pDefault)
		pDefault.Release()
	}

	// GetCount(&count).
	var count uint32
	if hrc, _, _ := syscall.SyscallN(
		collVT.GetCount,
		uintptr(unsafe.Pointer(pCollection)),
		uintptr(unsafe.Pointer(&count)),
	); hrc != 0 {
		slog.Debug("enumerateAudioInputs: GetCount failed", "hr", hrc)
		return devices
	}

	seen := make(map[string]bool, int(count))

	for i := uint32(0); i < count; i++ {
		var pDev *ole.IUnknown
		hri, _, _ := syscall.SyscallN(
			collVT.Item,
			uintptr(unsafe.Pointer(pCollection)),
			uintptr(i),
			uintptr(unsafe.Pointer(&pDev)),
		)
		if hri != 0 || pDev == nil {
			continue
		}

		id := deviceGetID(pDev)
		name := deviceGetFriendlyName(pDev)
		pDev.Release()

		if id == "" || name == "" || seen[id] {
			continue
		}
		seen[id] = true

		devices = append(devices, AudioDevice{
			ID:        id,
			Name:      name,
			IsDefault: id == defaultID,
		})
	}

	// If MMDevice didn't return a default endpoint (rare — happens on locked-
	// down SKUs or right after the only mic was unplugged) promote the first
	// device so the Settings dropdown still has a sensible initial selection.
	if len(devices) > 0 {
		hasDefault := false
		for _, d := range devices {
			if d.IsDefault {
				hasDefault = true
				break
			}
		}
		if !hasDefault {
			devices[0].IsDefault = true
		}
	}

	return devices
}

// deviceGetID calls IMMDevice::GetId(LPWSTR*). The returned LPWSTR is owned
// by the caller — we must CoTaskMemFree it after copying to a Go string.
func deviceGetID(dev *ole.IUnknown) string {
	vt := (*immDeviceVtbl)(unsafe.Pointer(dev.RawVTable))
	var w *uint16
	hr, _, _ := syscall.SyscallN(
		vt.GetId,
		uintptr(unsafe.Pointer(dev)),
		uintptr(unsafe.Pointer(&w)),
	)
	if hr != 0 || w == nil {
		return ""
	}
	s := utf16PtrToString(w)
	procCoTaskMemFree.Call(uintptr(unsafe.Pointer(w)))
	return s
}

// deviceGetFriendlyName opens the property store on an IMMDevice and reads
// PKEY_Device_FriendlyName. Returns "" on any failure so the caller can
// skip the entry (rather than render a nameless dropdown row).
func deviceGetFriendlyName(dev *ole.IUnknown) string {
	vt := (*immDeviceVtbl)(unsafe.Pointer(dev.RawVTable))

	var pStore *ole.IUnknown
	hr, _, _ := syscall.SyscallN(
		vt.OpenPropertyStore,
		uintptr(unsafe.Pointer(dev)),
		uintptr(stgmRead),
		uintptr(unsafe.Pointer(&pStore)),
	)
	if hr != 0 || pStore == nil {
		return ""
	}
	defer pStore.Release()

	storeVT := (*iPropertyStoreVtbl)(unsafe.Pointer(pStore.RawVTable))

	var pv propVariant
	procPropVariantInit.Call(uintptr(unsafe.Pointer(&pv)))

	hr2, _, _ := syscall.SyscallN(
		storeVT.GetValue,
		uintptr(unsafe.Pointer(pStore)),
		uintptr(unsafe.Pointer(&pkeyDeviceFriendlyName)),
		uintptr(unsafe.Pointer(&pv)),
	)
	if hr2 != 0 {
		procPropVariantClear.Call(uintptr(unsafe.Pointer(&pv)))
		return ""
	}

	var name string
	if pv.vt == vtLPWSTR && pv.val != nil {
		name = utf16PtrToString(pv.val)
	}

	// PropVariantClear frees the LPWSTR allocated by IPropertyStore::GetValue.
	procPropVariantClear.Call(uintptr(unsafe.Pointer(&pv)))
	return name
}

// utf16PtrToString walks a null-terminated UTF-16 buffer at p and returns it
// as a Go string. We cap the scan at 4096 code units to defend against a
// non-null-terminated buffer from a malformed COM caller; real device names
// are well under 100 chars.
//
// The pointer typically refers to memory allocated by Windows (CoTaskMemAlloc
// for IMMDevice::GetId, the property-store-internal allocator for PROPVARIANT
// VT_LPWSTR fields). That memory is GC-invisible, so dereferencing through an
// offset uintptr is safe — the Go runtime never relocates the buffer.
func utf16PtrToString(p *uint16) string {
	if p == nil {
		return ""
	}
	const maxLen = 4096
	base := unsafe.Pointer(p)
	buf := make([]uint16, 0, 64)
	for i := 0; i < maxLen; i++ {
		c := *(*uint16)(unsafe.Add(base, uintptr(i)*unsafe.Sizeof(uint16(0))))
		if c == 0 {
			break
		}
		buf = append(buf, c)
	}
	return string(utf16.Decode(buf))
}
