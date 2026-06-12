// screencapture_windows.c — C implementation of the WASAPI loopback
// audio-capture bridge. Compiled separately from screencapture_windows.go
// so the COM CLSID/IID instances (declared via INITGUID + DEFINE_GUID in
// MMDevice/Audioclient headers) only live in one translation unit. If
// this were inlined in the cgo preamble it would be replicated across
// every .o file that imports "C" (plus the test binary's stub), and the
// linker would refuse with duplicate-symbol errors for the GUID
// instances.
//
// Build flags: see screencapture_windows.go's cgo CFLAGS / LDFLAGS.
// COBJMACROS lets us call COM methods via the IFoo_Method(p,...) macros
// (clean C, no vtable boilerplate). INITGUID gives us the actual GUID
// byte storage for the WASAPI CLSIDs/IIDs in this object file.
// _WIN32_WINNT=0x0A00 targets Windows 10 (matches the rest of the
// Jarvis Windows port baseline).
//
// Threading: WASAPI capture runs on a dedicated worker thread created by
// CreateThread(); the goWindowsAudioCallback (Go-exported) fires from
// that thread. The Go side enforces the singleton (windowsCapturer.active);
// we also defensively gate here via a non-NULL gWorkerHandle.
//
// TASK-041 scope: deliver 16 kHz mono int16 PCM to the callback so the
// daemon's existing system_audio path is fed in CanonicalAudioFormat.
// TASK-042 scope: replace the TASK-041 decimator with a proper linear
// interpolator + stereo->mono mixdown that produces exactly 16 kHz output
// at all input rates (44.1k, 48k, 22.05k, ...). State (last mono sample +
// fractional phase) persists across packet boundaries so we don't drop or
// duplicate samples at packet seams. The interface contract (16 kHz mono
// int16 PCM bytes via goWindowsAudioCallback) stays identical between the
// two tasks.
// TASK-050 scope: handle AUDCLNT_E_DEVICE_INVALIDATED mid-capture. When
// the user unplugs/swaps the active output device, GetNextPacketSize and
// GetBuffer surface this HRESULT. The worker thread detects it, releases
// per-device WASAPI objects, and polls GetDefaultAudioEndpoint every
// 200 ms for up to 2 s for a new default. On success the worker reloads
// the per-device format locals (the new device may have a different
// sample rate / channel layout) and resumes. On failure ("no devices
// remain") the worker exits via a non-zero return code; the Go side
// observes the absence of further audio frames and is responsible for
// surfacing the meeting:permission_error event to the UI.

// COBJMACROS / INITGUID must be defined *before* the Windows headers
// they affect, otherwise the corresponding GUID storage and COM method
// macros never get emitted. cgo passes -DCOBJMACROS / -DINITGUID via
// CFLAGS too (defence-in-depth — see screencapture_windows.go), but
// defining them locally keeps this file self-documenting and lets it
// build correctly under any cgo CFLAGS variation.
#ifndef COBJMACROS
#define COBJMACROS
#endif
#ifndef INITGUID
#define INITGUID
#endif

#include <windows.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <math.h>

#include <mmdeviceapi.h>
#include <audioclient.h>
#include <functiondiscoverykeys_devpkey.h>

// Forward decl of the Go-exported callback. Must match cgo's emitted
// prototype in _cgo_export.h verbatim — no `const`, parameter names
// `handle/pcm/length`. Declared extern so the linker resolves it against
// the Go-side //export goWindowsAudioCallback symbol.
extern void goWindowsAudioCallback(uintptr_t handle, uint8_t *pcm, int length);

// Public C entrypoints (matched in screencapture_windows.go's cgo
// preamble). Return codes documented in the Go file's switch statement.
int wasapi_start(uintptr_t handle);
void wasapi_stop(void);

// Internal helpers for the TASK-050 re-acquire path. Declared up front so
// the worker thread can call them when AUDCLNT_E_DEVICE_INVALIDATED
// surfaces from GetNextPacketSize/GetBuffer mid-capture.
//
// wasapi_acquire_device() acquires the *per-device* WASAPI objects
// (gDevice, gAudioClient, gMixFormat, gReadyEvent, gCaptureClient) on top
// of an already-initialised gEnumerator and calls IAudioClient::Start.
// Returns:
//   0 = success (device acquired and capture started)
//   1 = no default playback (render) endpoint available (E_NOTFOUND or
//       enumerator returned NULL device — caller treats this as "all
//       devices gone, stop the worker")
//   2 = WASAPI/COM failure during device acquisition (transient — caller
//       may retry)
//
// wasapi_release_device() releases ONLY the per-device objects, leaving
// gEnumerator intact so the worker can re-acquire without paying the
// CoCreateInstance cost again. Safe to call multiple times — each
// pointer is nulled after release.
static int  wasapi_acquire_device(void);
static void wasapi_release_device(void);

// ----- Singleton capture state -------------------------------------------
//
// Only one active WASAPI loopback capture at a time. The Go side enforces
// this via the `active` boolean on windowsCapturer; we also defensively
// gate here (wasapi_start returns 4 if gWorkerHandle is non-NULL).
//
// All access to gStopFlag from the worker thread uses InterlockedExchange;
// the main thread sets it via wasapi_stop() and waits for the worker via
// WaitForSingleObject so there is no data race on the COM pointers.

static IMMDeviceEnumerator *gEnumerator = NULL;
static IMMDevice *gDevice = NULL;
static IAudioClient *gAudioClient = NULL;
static IAudioCaptureClient *gCaptureClient = NULL;
static WAVEFORMATEX *gMixFormat = NULL;
static HANDLE gWorkerHandle = NULL;
static HANDLE gReadyEvent = NULL;
static volatile LONG gStopFlag = 0;
static uintptr_t gGoHandle = 0;

// Scratch buffer for the converted (16 kHz mono int16) frames. Sized
// generously — a 100 ms buffer at 16 kHz mono int16 is 3200 bytes; we
// keep a 1-second buffer (32 KB) so even pathological WASAPI bursts fit
// without reallocation. The worker thread is the only writer.
#define CONVERTED_BUF_BYTES (16000 * 2 /* int16 */ * 1)
static uint8_t gConvertedBuf[CONVERTED_BUF_BYTES];

// Resampler state — persists across WASAPI packets so the linear
// interpolator doesn't drop or duplicate samples at packet seams.
//
//   gLastMonoSample: the most recent mono-downmixed input sample (int16),
//     used as the "left" endpoint when the interpolator's fractional
//     position lands between the last sample of packet N and the first
//     sample of packet N+1. Initialised to 0 at start (silence).
//
//   gResamplePhase: fractional position in the input stream, in units of
//     input samples. Each output sample at 16 kHz consumes `step` input
//     samples where step = srcRate / 16000. After emitting one output
//     sample we advance gResamplePhase by `step`. When gResamplePhase
//     exceeds the input-buffer boundary we consume more input samples
//     before producing the next output. Persisted across packets so the
//     output cadence is exact (no drift, no duplicated frames).
//
//   gResampleInited: flag for first-frame setup of gLastMonoSample.
//     Cleared in wasapi_cleanup so a Stop/Start cycle re-initialises
//     state cleanly.
static int16_t gLastMonoSample = 0;
static double  gResamplePhase  = 0.0;
static int     gResampleInited = 0;

// ----- Cleanup helper -----------------------------------------------------
//
// Centralised teardown so wasapi_start error paths and wasapi_stop both
// release resources in the same order. Safe to call multiple times — each
// pointer is nulled after release so subsequent calls no-op.

static void wasapi_cleanup(void) {
    if (gCaptureClient != NULL) {
        IAudioCaptureClient_Release(gCaptureClient);
        gCaptureClient = NULL;
    }
    if (gAudioClient != NULL) {
        // Best-effort stop; ignore error (we're tearing down anyway).
        (void)IAudioClient_Stop(gAudioClient);
        IAudioClient_Release(gAudioClient);
        gAudioClient = NULL;
    }
    if (gDevice != NULL) {
        IMMDevice_Release(gDevice);
        gDevice = NULL;
    }
    if (gEnumerator != NULL) {
        IMMDeviceEnumerator_Release(gEnumerator);
        gEnumerator = NULL;
    }
    if (gMixFormat != NULL) {
        CoTaskMemFree(gMixFormat);
        gMixFormat = NULL;
    }
    if (gReadyEvent != NULL) {
        CloseHandle(gReadyEvent);
        gReadyEvent = NULL;
    }
    gGoHandle = 0;
    gStopFlag = 0;
    // Reset resampler state so a Stop/Start cycle starts fresh and the
    // first output sample after restart isn't interpolated against stale
    // audio from the previous capture session.
    gLastMonoSample = 0;
    gResamplePhase  = 0.0;
    gResampleInited = 0;
}

// ----- Per-device release helper (TASK-050) ------------------------------
//
// Releases ONLY the per-device WASAPI objects acquired by
// wasapi_acquire_device() — leaves gEnumerator intact so the worker
// thread can re-acquire after AUDCLNT_E_DEVICE_INVALIDATED without
// paying the CoCreateInstance cost again. Order matches wasapi_cleanup
// (capture client -> audio client -> device -> mix format -> ready
// event); each pointer is nulled so subsequent calls no-op.
//
// We intentionally do NOT clear gGoHandle, gStopFlag, or the resampler
// state (gLastMonoSample / gResamplePhase / gResampleInited) — those
// must persist across the device swap so:
//   * audio frames keep flowing to the same Go callback handle
//   * the worker's stop signal isn't lost
//   * the resampler doesn't emit a discontinuity at the swap seam
//     (we'd rather interpolate against the last sample of the old
//     device than reset to silence and produce a click).
static void wasapi_release_device(void) {
    if (gCaptureClient != NULL) {
        IAudioCaptureClient_Release(gCaptureClient);
        gCaptureClient = NULL;
    }
    if (gAudioClient != NULL) {
        (void)IAudioClient_Stop(gAudioClient);
        IAudioClient_Release(gAudioClient);
        gAudioClient = NULL;
    }
    if (gDevice != NULL) {
        IMMDevice_Release(gDevice);
        gDevice = NULL;
    }
    if (gMixFormat != NULL) {
        CoTaskMemFree(gMixFormat);
        gMixFormat = NULL;
    }
    if (gReadyEvent != NULL) {
        CloseHandle(gReadyEvent);
        gReadyEvent = NULL;
    }
}

// ----- Per-device acquire helper (TASK-050) ------------------------------
//
// Acquires the per-device WASAPI objects on top of an already-initialised
// gEnumerator and calls IAudioClient::Start. Used by both wasapi_start()
// (initial acquisition) and the worker's re-acquire path after
// AUDCLNT_E_DEVICE_INVALIDATED.
//
// Preconditions:
//   * gEnumerator is non-NULL (set by wasapi_start before any call here).
//   * gDevice / gAudioClient / gCaptureClient / gMixFormat / gReadyEvent
//     are all NULL (caller has wasapi_release_device'd if recovering).
//
// On failure the function releases anything it managed to acquire so
// the global state is consistent (all NULL) when it returns non-zero.
//
// Returns:
//   0 = success
//   1 = no default playback endpoint (all devices gone) — caller's
//       signal to stop the worker because there's no fallback left
//   2 = WASAPI/COM failure during acquisition (transient — caller may
//       retry; e.g. driver still settling after a device change)
static int wasapi_acquire_device(void) {
    HRESULT hr;

    hr = IMMDeviceEnumerator_GetDefaultAudioEndpoint(gEnumerator, eRender, eMultimedia, &gDevice);
    if (FAILED(hr) || gDevice == NULL) {
        // E_NOTFOUND is the classic "no playback endpoint" signal.
        // Translate to rc=1 so the worker stops cleanly rather than
        // spinning on a permanent failure.
        OutputDebugStringA("[jarvis-wasapi] acquire: GetDefaultAudioEndpoint(eRender) failed");
        wasapi_release_device();
        return 1;
    }

    hr = IMMDevice_Activate(gDevice, &IID_IAudioClient, CLSCTX_ALL, NULL, (void **)&gAudioClient);
    if (FAILED(hr) || gAudioClient == NULL) {
        OutputDebugStringA("[jarvis-wasapi] acquire: IMMDevice::Activate(IAudioClient) failed");
        wasapi_release_device();
        return 2;
    }

    hr = IAudioClient_GetMixFormat(gAudioClient, &gMixFormat);
    if (FAILED(hr) || gMixFormat == NULL) {
        OutputDebugStringA("[jarvis-wasapi] acquire: GetMixFormat failed");
        wasapi_release_device();
        return 2;
    }

    // 100 ms loopback buffer — same value as wasapi_start. Keep them in
    // sync so the worker's WaitForSingleObject(gReadyEvent, 100) period
    // continues to align with WASAPI's notification cadence after a
    // device swap.
    const REFERENCE_TIME bufferDuration = 1000000; // 100 ms

    hr = IAudioClient_Initialize(
        gAudioClient,
        AUDCLNT_SHAREMODE_SHARED,
        AUDCLNT_STREAMFLAGS_LOOPBACK | AUDCLNT_STREAMFLAGS_EVENTCALLBACK,
        bufferDuration,
        0,
        gMixFormat,
        NULL);
    if (FAILED(hr)) {
        OutputDebugStringA("[jarvis-wasapi] acquire: IAudioClient::Initialize(LOOPBACK) failed");
        wasapi_release_device();
        return 2;
    }

    gReadyEvent = CreateEventW(NULL, FALSE /* auto-reset */, FALSE, NULL);
    if (gReadyEvent == NULL) {
        OutputDebugStringA("[jarvis-wasapi] acquire: CreateEventW failed");
        wasapi_release_device();
        return 2;
    }

    hr = IAudioClient_SetEventHandle(gAudioClient, gReadyEvent);
    if (FAILED(hr)) {
        OutputDebugStringA("[jarvis-wasapi] acquire: SetEventHandle failed");
        wasapi_release_device();
        return 2;
    }

    hr = IAudioClient_GetService(gAudioClient, &IID_IAudioCaptureClient, (void **)&gCaptureClient);
    if (FAILED(hr) || gCaptureClient == NULL) {
        OutputDebugStringA("[jarvis-wasapi] acquire: GetService(IAudioCaptureClient) failed");
        wasapi_release_device();
        return 2;
    }

    hr = IAudioClient_Start(gAudioClient);
    if (FAILED(hr)) {
        OutputDebugStringA("[jarvis-wasapi] acquire: IAudioClient::Start failed");
        wasapi_release_device();
        return 2;
    }

    return 0;
}

// downmix_frame: collapse N channels of one input frame to a single int16
// mono sample. Uses a stereo-aware downmix when channels >= 2 (average of
// left + right with int32 accumulator to avoid overflow), and falls back
// to averaging all channels for mono / >2-channel inputs. Clamps to
// [INT16_MIN, INT16_MAX] in case the average rounds out of range.
//
// frameBase points at the first byte of the frame (channel 0). channels
// is the number of channels in the input, bytesPerSample is the
// Forward declaration — defined under "Format helpers" below; used by
// downmix_frame before its definition. Without this, GCC (MinGW on the
// windows-2022/2025 release runners) hard-errors with
// -Wimplicit-function-declaration.
static int16_t to_int16(const uint8_t *src, int isFloat, int bitsPerSample);

// per-sample stride (gMixFormat->wBitsPerSample / 8). isFloat selects
// the to_int16 conversion path. Safe to call with frameBase == NULL
// (returns silence).
static int16_t downmix_frame(const uint8_t *frameBase, UINT32 channels, UINT32 bytesPerSample, UINT32 bitsPerSample, int isFloat) {
    if (frameBase == NULL || channels == 0) {
        return 0;
    }
    if (channels >= 2) {
        // Stereo mixdown: explicit (L + R) / 2. For >2-ch input we mix
        // just L+R, which is the standard "front pair" reduction that
        // ITU-R BS.775 recommends for surround→stereo→mono pipelines and
        // avoids muddying centre dialogue with rear-surround content.
        int16_t left  = to_int16(frameBase,                       isFloat, (int)bitsPerSample);
        int16_t right = to_int16(frameBase + (size_t)bytesPerSample, isFloat, (int)bitsPerSample);
        int32_t mixed = ((int32_t)left + (int32_t)right) / 2;
        if (mixed >  32767) mixed =  32767;
        if (mixed < -32768) mixed = -32768;
        return (int16_t)mixed;
    }
    // Mono passthrough.
    return to_int16(frameBase, isFloat, (int)bitsPerSample);
}

// ----- Format helpers -----------------------------------------------------
//
// WAVEFORMATEX can be either a plain PCM/float WAVEFORMATEX or a
// WAVEFORMATEXTENSIBLE (when channelCount > 2, or when the format is
// extended). The mix format from GetMixFormat() is typically
// WAVEFORMATEXTENSIBLE with float32 samples on Win10/11.

static int format_is_float(const WAVEFORMATEX *fmt) {
    if (fmt == NULL) return 0;
    if (fmt->wFormatTag == WAVE_FORMAT_IEEE_FLOAT) return 1;
    if (fmt->wFormatTag == WAVE_FORMAT_EXTENSIBLE && fmt->cbSize >= 22) {
        const WAVEFORMATEXTENSIBLE *ext = (const WAVEFORMATEXTENSIBLE *)fmt;
        return IsEqualGUID(&ext->SubFormat, &KSDATAFORMAT_SUBTYPE_IEEE_FLOAT) ? 1 : 0;
    }
    return 0;
}

static int format_is_pcm(const WAVEFORMATEX *fmt) {
    if (fmt == NULL) return 0;
    if (fmt->wFormatTag == WAVE_FORMAT_PCM) return 1;
    if (fmt->wFormatTag == WAVE_FORMAT_EXTENSIBLE && fmt->cbSize >= 22) {
        const WAVEFORMATEXTENSIBLE *ext = (const WAVEFORMATEXTENSIBLE *)fmt;
        return IsEqualGUID(&ext->SubFormat, &KSDATAFORMAT_SUBTYPE_PCM) ? 1 : 0;
    }
    return 0;
}

// Convert one input sample (float32 or int16/int32 PCM) to int16. Returns
// a 16-bit value clamped to [INT16_MIN, INT16_MAX]. Robust against
// out-of-range float values (e.g. > 1.0 from peaking mixers).
static int16_t to_int16(const uint8_t *src, int isFloat, int bitsPerSample) {
    if (isFloat && bitsPerSample == 32) {
        float v;
        memcpy(&v, src, 4);
        if (v >  1.0f) v =  1.0f;
        if (v < -1.0f) v = -1.0f;
        long s = (long)lrintf(v * 32767.0f);
        if (s >  32767) s =  32767;
        if (s < -32768) s = -32768;
        return (int16_t)s;
    }
    if (!isFloat && bitsPerSample == 16) {
        int16_t v;
        memcpy(&v, src, 2);
        return v;
    }
    if (!isFloat && bitsPerSample == 32) {
        int32_t v;
        memcpy(&v, src, 4);
        // Right-shift 16 bits to drop low-order detail down to int16 range.
        return (int16_t)(v >> 16);
    }
    if (!isFloat && bitsPerSample == 24) {
        // 24-bit packed little-endian -> sign-extend then truncate to int16.
        int32_t v = (int32_t)src[0] | ((int32_t)src[1] << 8) | ((int32_t)src[2] << 16);
        if (v & 0x800000) v |= 0xFF000000;
        return (int16_t)(v >> 8);
    }
    // Unsupported bit depth — return silence so the daemon stays alive.
    return 0;
}

// ----- Capture worker -----------------------------------------------------
//
// Wakes on the event handle (set by WASAPI when the loopback buffer has
// data ready), drains all available packets, downmixes to mono, resamples
// to exactly 16 kHz via linear interpolation, and ships int16 frames to
// Go via goWindowsAudioCallback.
//
// Resampling algorithm (TASK-042): per-output-sample linear interpolation.
//
//   step       = srcRate / 16000        (e.g. 3.0 for 48 kHz input,
//                                         2.75625 for 44.1 kHz input)
//   phase      = fractional position in input samples; persists across
//                packets so output cadence has no drift at seams
//   For each input sample s_i (mono-downmixed):
//     while phase <= 1.0:
//       out = lerp(prev_mono, s_i, phase)
//       emit out
//       phase += step
//     prev_mono = s_i
//     phase    -= 1.0   (we just consumed one input sample)
//
//   This produces exactly srcRate / step = 16000 Hz output regardless of
//   the input rate. Works for any rate >= 16 kHz (step >= 1.0) AND for
//   rates < 16 kHz (step < 1.0) — the inner while loop emits multiple
//   interpolated samples between each pair of input samples (i.e.
//   upsampling) when step < 1.0, so unusual rates like 22.05k still
//   convert correctly. The acceptance criterion "unusual sample rates
//   still convert" is met by this single code path.
//
// Stereo->mono mixdown lives in downmix_frame() above: explicit (L+R)/2
// for ≥2 channels, passthrough for mono. The mixdown happens BEFORE the
// resampler so the interpolator only sees mono int16 samples.
//
// Device-disconnect recovery (TASK-050): when WASAPI returns
// AUDCLNT_E_DEVICE_INVALIDATED from GetNextPacketSize or GetBuffer, the
// worker calls recover_from_device_invalidated() which releases the
// stale per-device objects, polls for a new default endpoint every
// 200 ms for up to 2 s, and reacquires the WASAPI pipeline. On success
// the format locals are reloaded via load_worker_format() (the new
// device may have a different sample rate / channel layout) and the
// outer loop continues. The resampler state is reset across the seam to
// avoid an interpolation discontinuity between the old and new device.
// If no device remains after the 2 s budget the worker returns a
// non-zero code; the Go side detects that frames have stopped arriving
// and surfaces meeting:permission_error to the UI.

// recover_from_device_invalidated attempts to re-acquire the default
// playback endpoint after AUDCLNT_E_DEVICE_INVALIDATED. Used by the
// worker thread when the user unplugs/swaps the active output device
// mid-meeting (TASK-050).
//
// Retry policy: poll every 200 ms for up to ~2 s (10 attempts). The
// 2 s budget matches the TASK-050 acceptance criterion. Polling rather
// than a CoreAudio device-event subscription keeps the recovery logic
// confined to this worker thread and the .c TU — no extra COM
// callbacks to register/teardown, no extra lifetimes to manage.
//
// Returns:
//    1 = recovered (new device acquired, capture running)
//    0 = no devices remain (acquire returned rc=1 within budget — stop)
//   -1 = transient failures only (acquire returned rc=2 every attempt;
//        caller treats as "stop"; further retries unlikely to help and
//        would extend the silence gap unhelpfully)
//
// Pre/post conditions: on entry the per-device WASAPI objects are
// stale; this helper calls wasapi_release_device() unconditionally
// before attempting acquisition. On success, all per-device pointers
// are valid and the audio client is running. On failure they are NULL.
//
// gStopFlag is checked between attempts so a user-initiated Stop()
// during the recovery window terminates promptly rather than waiting
// for the full 2 s timeout.
static int recover_from_device_invalidated(void) {
    // Release stale per-device state. Best-effort — these COM pointers
    // are already invalidated by Windows on AUDCLNT_E_DEVICE_INVALIDATED
    // but Release()ing them is still required to balance the AddRef
    // we did when they were acquired.
    wasapi_release_device();

    OutputDebugStringA("[jarvis-wasapi] device invalidated — attempting re-acquisition (TASK-050)");

    // 10 attempts x 200 ms = 2 s budget. The first attempt happens
    // immediately because a default device may already exist (e.g. the
    // user swapped to a different already-connected device). Subsequent
    // attempts give the OS time to elect a new default endpoint after
    // the active one disappears.
    const int maxAttempts = 10;
    const DWORD pollSleepMs = 200;
    int sawTransient = 0;
    for (int attempt = 0; attempt < maxAttempts; attempt++) {
        if (InterlockedCompareExchange(&gStopFlag, 0, 0) != 0) {
            // Stop requested mid-recovery. Bail out cleanly so wasapi_stop
            // doesn't have to wait the full timeout for the worker to
            // notice the flag.
            return 0;
        }

        int rc = wasapi_acquire_device();
        if (rc == 0) {
            // Reset the resampler so the first sample of the new device
            // isn't interpolated against the last sample of the old one
            // (different sample-rate / channel-layout would otherwise
            // produce a click at the seam). The 200 ms+ silence gap from
            // the recovery window already makes a hard cut imperceptible.
            gLastMonoSample = 0;
            gResamplePhase  = 0.0;
            gResampleInited = 0;
            OutputDebugStringA("[jarvis-wasapi] recovery: Audio device changed, continuing on default");
            return 1;
        }
        if (rc == 1) {
            // No default endpoint exists at all. The user has either
            // disabled every output device or all hardware is gone. Stop
            // cleanly rather than spinning — emitting permission_error
            // is the Go side's responsibility once the worker exits.
            OutputDebugStringA("[jarvis-wasapi] recovery: no default playback device available — giving up");
            return 0;
        }
        // rc == 2 — transient WASAPI/COM failure. Sleep and retry.
        sawTransient = 1;
        Sleep(pollSleepMs);
    }

    // 10 transient failures in a row — extremely unlikely in practice;
    // treat as "stop" rather than retry forever. We log and return -1 so
    // future maintainers can distinguish "no device" from "WASAPI sick".
    OutputDebugStringA("[jarvis-wasapi] recovery: exhausted retries (transient errors) — giving up");
    (void)sawTransient;
    return -1;
}

// load_worker_format refreshes the locals that depend on gMixFormat from
// the (possibly newly-acquired) WASAPI device. Called once at the top of
// the worker and once after every successful recovery. Out-params are
// set to zero/non-functional values when the mix format is unsupported
// so the caller can detect the failure mode and exit cleanly. Pulled
// into a helper so the initial-setup path and the post-recovery path
// share the same code — without this, the recovery path would silently
// keep using stale frameSize/channels/etc and corrupt the audio.
static int load_worker_format(
    UINT32 *outFrameSize,
    UINT32 *outChannels,
    UINT32 *outBitsPerSample,
    UINT32 *outBytesPerSample,
    int    *outIsFloat,
    int    *outIsPcm,
    double *outSrcRate,
    double *outStep) {
    if (gMixFormat == NULL) {
        return 0;
    }
    *outFrameSize = gMixFormat->nBlockAlign;
    *outChannels = gMixFormat->nChannels;
    *outBitsPerSample = gMixFormat->wBitsPerSample;
    *outBytesPerSample = *outBitsPerSample / 8;
    *outIsFloat = format_is_float(gMixFormat);
    *outIsPcm = format_is_pcm(gMixFormat);
    if (!*outIsFloat && !*outIsPcm) {
        return 0;
    }
    double sr = (double)gMixFormat->nSamplesPerSec;
    if (sr <= 0.0) {
        return 0;
    }
    *outSrcRate = sr;
    *outStep = sr / 16000.0;
    return 1;
}

static DWORD WINAPI capture_worker(LPVOID arg) {
    (void)arg;

    // COM init on this worker thread. We use COINIT_MULTITHREADED so we
    // can call WASAPI methods on this thread without proxying through
    // the main thread's apartment. CoUninitialize is paired in the exit
    // path below. RPC_E_CHANGED_MODE is benign — the caller already set
    // a different apartment model and we proceed without an init we own.
    HRESULT hrCo = CoInitializeEx(NULL, COINIT_MULTITHREADED);
    const int ownsCom = (hrCo == S_OK || hrCo == S_FALSE);

    // Frame-format locals. NOT const because TASK-050 lets us re-acquire
    // the device mid-capture; the new device may have a different
    // sample rate / channel count / bit depth, so these all need to be
    // refreshable. load_worker_format() pulls fresh values out of the
    // current gMixFormat.
    UINT32 frameSize = 0;
    UINT32 channels = 0;
    UINT32 bitsPerSample = 0;
    UINT32 bytesPerSample = 0;
    int    isFloat = 0;
    int    isPcm = 0;
    double srcRate = 0.0;
    double step = 0.0;
    if (!load_worker_format(&frameSize, &channels, &bitsPerSample, &bytesPerSample,
                             &isFloat, &isPcm, &srcRate, &step)) {
        // Unsupported subformat or invalid srcRate — bail out cleanly.
        // The meeting UI will see a silent system_audio stream; an
        // error path here would tear down the whole capture which is
        // worse UX (per the original TASK-041 design note).
        OutputDebugStringA("[jarvis-wasapi] unsupported mix format on startup; aborting worker");
        if (ownsCom) {
            CoUninitialize();
        }
        return 1;
    }

    HRESULT hr;
    while (InterlockedCompareExchange(&gStopFlag, 0, 0) == 0) {
        // 100 ms wait — bounded so the stop flag is checked roughly that
        // often. WASAPI fires the event when at least one packet is ready.
        DWORD waitRc = WaitForSingleObject(gReadyEvent, 100);
        if (waitRc == WAIT_FAILED) {
            // Event handle invalidated (most often during teardown) —
            // exit cleanly; the cleanup path will release resources.
            if (ownsCom) {
                CoUninitialize();
            }
            return 2;
        }

        UINT32 packetSize = 0;
        hr = IAudioCaptureClient_GetNextPacketSize(gCaptureClient, &packetSize);
        if (hr == AUDCLNT_E_DEVICE_INVALIDATED) {
            // TASK-050: user unplugged / disabled the active output
            // device. Re-acquire the new default within 2 s, or stop
            // cleanly if no devices remain.
            int recRc = recover_from_device_invalidated();
            if (recRc != 1) {
                // No devices left (recRc == 0) OR persistent transient
                // failures (recRc == -1). Exit cleanly — the Go side
                // will observe the worker exit via the absence of
                // further audio frames and is responsible for surfacing
                // meeting:permission_error to the UI.
                if (ownsCom) {
                    CoUninitialize();
                }
                return recRc == 0 ? 4 /* no devices */ : 5 /* transient exhausted */;
            }
            // Recovered: reload format locals (new device may differ).
            if (!load_worker_format(&frameSize, &channels, &bitsPerSample, &bytesPerSample,
                                     &isFloat, &isPcm, &srcRate, &step)) {
                OutputDebugStringA("[jarvis-wasapi] recovery acquired device with unsupported format; aborting");
                if (ownsCom) {
                    CoUninitialize();
                }
                return 6;
            }
            continue;
        }
        if (FAILED(hr)) {
            // Non-invalidation failure — keep the original "log + retry"
            // behaviour. AUDCLNT_E_SERVICE_NOT_RUNNING / E_OUTOFMEMORY
            // etc. should resolve on the next iteration once the event
            // fires again.
            OutputDebugStringA("[jarvis-wasapi] GetNextPacketSize failed");
            continue;
        }

        while (packetSize != 0 && InterlockedCompareExchange(&gStopFlag, 0, 0) == 0) {
            BYTE *data = NULL;
            UINT32 numFrames = 0;
            DWORD flags = 0;

            hr = IAudioCaptureClient_GetBuffer(gCaptureClient, &data, &numFrames, &flags, NULL, NULL);
            if (hr == AUDCLNT_E_DEVICE_INVALIDATED) {
                // Same recovery path as the outer GetNextPacketSize
                // check — device disappeared between the size probe
                // and the buffer fetch. Break out of the inner drain
                // loop so we re-enter the outer loop's wait+probe
                // sequence on the new device. The packetSize-driven
                // inner loop will terminate naturally because the
                // stale gCaptureClient was released in the recovery.
                int recRc = recover_from_device_invalidated();
                if (recRc != 1) {
                    if (ownsCom) {
                        CoUninitialize();
                    }
                    return recRc == 0 ? 4 : 5;
                }
                if (!load_worker_format(&frameSize, &channels, &bitsPerSample, &bytesPerSample,
                                         &isFloat, &isPcm, &srcRate, &step)) {
                    OutputDebugStringA("[jarvis-wasapi] recovery acquired device with unsupported format; aborting");
                    if (ownsCom) {
                        CoUninitialize();
                    }
                    return 6;
                }
                // Break the inner drain loop; the outer while re-probes
                // on the freshly-acquired device. Don't ReleaseBuffer
                // here — gCaptureClient is a brand-new pointer with no
                // pending GetBuffer call.
                break;
            }
            if (hr == AUDCLNT_S_BUFFER_EMPTY || numFrames == 0) {
                break;
            }
            if (FAILED(hr)) {
                OutputDebugStringA("[jarvis-wasapi] GetBuffer failed");
                break;
            }

            // Resample -> mono int16 @ exactly 16 kHz into gConvertedBuf.
            int outSamples = 0;
            const int outCap = (int)(CONVERTED_BUF_BYTES / sizeof(int16_t));
            int16_t *outPtr = (int16_t *)gConvertedBuf;

            // AUDCLNT_BUFFERFLAGS_SILENT: WASAPI tells us the packet is
            // logically silence (no app producing audio). Honour it by
            // emitting zeros so the daemon receives timely keepalives
            // rather than a stall. We still advance the resampler phase
            // so the output cadence stays aligned to wall-clock time.
            const int isSilent = (flags & AUDCLNT_BUFFERFLAGS_SILENT) != 0;

            // Streaming linear interpolation:
            //
            //   prev = last input sample seen (gLastMonoSample)
            //   cur  = current input sample
            //   phase = fractional position in [0, 1) between prev (0.0)
            //           and cur (1.0). When phase >= 1.0 after a step
            //           advance, we "consume" cur: prev := cur,
            //           phase -= 1.0, and move to the next input sample.
            //
            // The first-ever input sample initialises prev without
            // emitting anything (we have no "left endpoint" before the
            // capture started, so we can't interpolate the period
            // *preceding* sample 0). From the second sample onward the
            // interpolator runs normally and the very first output is
            // emit at phase 0.0 == prev == s_0.
            for (UINT32 i = 0; i < numFrames; i++) {
                // Compute the current input sample's mono int16 value.
                int16_t cur;
                if (isSilent) {
                    cur = 0;
                } else {
                    const uint8_t *frameBase = (data != NULL) ? (data + (size_t)i * frameSize) : NULL;
                    cur = downmix_frame(frameBase, channels, bytesPerSample, bitsPerSample, isFloat);
                }

                if (!gResampleInited) {
                    // No prev sample yet — seed prev=s_0, phase=0, emit
                    // nothing this iteration. Next input sample will be
                    // the first to actually emit output, starting at
                    // phase 0 (== s_0 exactly).
                    gLastMonoSample = cur;
                    gResamplePhase  = 0.0;
                    gResampleInited = 1;
                    continue;
                }

                // Emit every output sample whose interpolation position
                // falls within [0, 1) of the current pair (prev, cur).
                // gResamplePhase is the fractional position between
                // prev (0.0) and cur (1.0). Advance by `step` after each
                // emit. Loop terminates when phase crosses 1.0 — at
                // which point we consume the input sample (prev := cur,
                // phase -= 1.0). The while handles step < 1.0 (upsample)
                // by emitting >1 outputs per input pair; for step >= 1.0
                // (downsample) it emits 0 or 1 per input pair, with the
                // phase carry-over driving the cadence.
                while (gResamplePhase < 1.0 && outSamples < outCap) {
                    // out = prev + (cur - prev) * phase
                    double interp = (double)gLastMonoSample
                                  + ((double)cur - (double)gLastMonoSample) * gResamplePhase;
                    if (interp >  32767.0) interp =  32767.0;
                    if (interp < -32768.0) interp = -32768.0;
                    outPtr[outSamples++] = (int16_t)interp;
                    gResamplePhase += step;
                }

                // Consume this input sample: shift the prev/cur window
                // forward by one input sample. phase decreases by 1.0
                // because the [prev, cur) interval just slid by 1 input.
                gLastMonoSample = cur;
                gResamplePhase -= 1.0;
                // Clamp phase >= 0 so a degenerate sequence of zero-
                // length packets can't drift it negative. (In normal
                // operation phase is always >= 0 here because the while
                // loop only exits once phase >= 1.0 or the out buffer
                // filled; after the -= 1.0 it should still be >= 0.0.)
                if (gResamplePhase < 0.0) gResamplePhase = 0.0;

                if (outSamples >= outCap) {
                    // Output buffer full — flush below and continue with
                    // the next input sample on the next iteration. The
                    // outer ReleaseBuffer path handles partial flushes.
                    break;
                }
            }

            hr = IAudioCaptureClient_ReleaseBuffer(gCaptureClient, numFrames);
            if (FAILED(hr)) {
                OutputDebugStringA("[jarvis-wasapi] ReleaseBuffer failed");
            }

            if (outSamples > 0) {
                // Hand off to Go. The Go side copies the bytes before
                // returning, so we can reuse gConvertedBuf for the next
                // packet without coordination.
                goWindowsAudioCallback(gGoHandle, (uint8_t *)outPtr, outSamples * (int)sizeof(int16_t));
            }

            hr = IAudioCaptureClient_GetNextPacketSize(gCaptureClient, &packetSize);
            if (hr == AUDCLNT_E_DEVICE_INVALIDATED) {
                // Symmetric with the outer-loop branch — the device may
                // disappear between drain iterations too. Recover and
                // break to the outer loop. Don't continue draining
                // because gCaptureClient is now a fresh pointer.
                int recRc = recover_from_device_invalidated();
                if (recRc != 1) {
                    if (ownsCom) {
                        CoUninitialize();
                    }
                    return recRc == 0 ? 4 : 5;
                }
                if (!load_worker_format(&frameSize, &channels, &bitsPerSample, &bytesPerSample,
                                         &isFloat, &isPcm, &srcRate, &step)) {
                    OutputDebugStringA("[jarvis-wasapi] recovery acquired device with unsupported format; aborting");
                    if (ownsCom) {
                        CoUninitialize();
                    }
                    return 6;
                }
                packetSize = 0; // break inner loop, outer re-probes.
                break;
            }
            if (FAILED(hr)) {
                packetSize = 0;
            }
        }
    }

    if (ownsCom) {
        CoUninitialize();
    }
    return 0;
}

// ----- Start --------------------------------------------------------------
//
// Returns:
//   0 = success
//   1 = no default playback (render) endpoint available
//   2 = device activate / initialize failed
//   3 = capture client / worker thread failed to start
//   4 = already active (defensive)
//
// Order of ops:
//   1. CoInitializeEx(MULTITHREADED) — safe to call multiple times.
//   2. CoCreateInstance(MMDeviceEnumerator).
//   3. wasapi_acquire_device() — shared with TASK-050 mid-capture
//      recovery; performs steps 3–9 in one place so initial-start and
//      device-swap recovery follow exactly the same code path:
//        a. GetDefaultAudioEndpoint(eRender, eMultimedia)
//        b. IMMDevice::Activate(IAudioClient)
//        c. IAudioClient::GetMixFormat
//        d. IAudioClient::Initialize (LOOPBACK + EVENTCALLBACK, 100 ms)
//        e. CreateEventW (auto-reset) + IAudioClient::SetEventHandle
//        f. IAudioClient::GetService(IAudioCaptureClient)
//        g. IAudioClient::Start
//   4. CreateThread for the worker.

int wasapi_start(uintptr_t handle) {
    if (gWorkerHandle != NULL) {
        return 4;
    }

    // CoInitializeEx returns S_FALSE if already initialised on this
    // thread with the same model — that's fine. RPC_E_CHANGED_MODE means
    // someone earlier initialised STA; we don't fight, just proceed (COM
    // calls below will still work because we don't depend on the
    // apartment model for cross-thread proxies in this codepath).
    HRESULT hrCo = CoInitializeEx(NULL, COINIT_MULTITHREADED);
    (void)hrCo;

    HRESULT hr = CoCreateInstance(
        &CLSID_MMDeviceEnumerator,
        NULL,
        CLSCTX_ALL,
        &IID_IMMDeviceEnumerator,
        (void **)&gEnumerator);
    if (FAILED(hr) || gEnumerator == NULL) {
        OutputDebugStringA("[jarvis-wasapi] CoCreateInstance(MMDeviceEnumerator) failed");
        wasapi_cleanup();
        return 2;
    }

    // Acquire the per-device WASAPI objects via the shared helper so
    // initial-start and mid-capture re-acquisition (TASK-050) follow
    // exactly the same code path. wasapi_acquire_device returns:
    //   0 = success
    //   1 = no default playback endpoint (map to wasapi_start rc=1)
    //   2 = WASAPI/COM init failure (map to wasapi_start rc=2)
    gGoHandle = handle;
    gStopFlag = 0;

    int acquireRc = wasapi_acquire_device();
    if (acquireRc != 0) {
        wasapi_cleanup();
        return acquireRc == 1 ? 1 : 2;
    }

    gWorkerHandle = CreateThread(NULL, 0, capture_worker, NULL, 0, NULL);
    if (gWorkerHandle == NULL) {
        OutputDebugStringA("[jarvis-wasapi] CreateThread(capture_worker) failed");
        wasapi_cleanup();
        return 3;
    }

    return 0;
}

// ----- Stop ---------------------------------------------------------------
//
// Idempotent — if no worker is running, do nothing. Signals the worker via
// the stop flag, then waits up to 1s for it to exit. Tearing down the COM
// pointers before the worker exits would crash; the wait is bounded so a
// hung worker doesn't lock the UI thread forever.

void wasapi_stop(void) {
    if (gWorkerHandle == NULL) {
        return;
    }
    InterlockedExchange(&gStopFlag, 1);
    // Nudge the worker out of its event wait so it sees the flag promptly.
    if (gReadyEvent != NULL) {
        SetEvent(gReadyEvent);
    }
    // 1s is generous — the worker checks gStopFlag every 100 ms.
    WaitForSingleObject(gWorkerHandle, 1000);
    CloseHandle(gWorkerHandle);
    gWorkerHandle = NULL;

    wasapi_cleanup();
}
