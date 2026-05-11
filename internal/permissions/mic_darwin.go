//go:build darwin

// Package permissions exposes macOS TCC (Transparency, Consent, and Control)
// permission checks for the microphone. The darwin implementation wraps the
// AVFoundation AVCaptureDevice API via cgo + Objective-C.
package permissions

/*
#cgo CFLAGS: -x objective-c -fobjc-arc
#cgo LDFLAGS: -framework Foundation -framework AVFoundation

#import <AVFoundation/AVFoundation.h>

// authStatus returns the current AVAuthorizationStatus for the audio media type.
// 0 = NotDetermined, 1 = Restricted, 2 = Denied, 3 = Authorized
static int authStatus(void) {
    return (int)[AVCaptureDevice authorizationStatusForMediaType:AVMediaTypeAudio];
}

// requestAccess triggers the OS-level permission dialog. The call is
// asynchronous from AVFoundation's perspective: the prompt is presented and
// the completion handler fires once the user decides. We return immediately
// from Go; callers should poll authStatus() to learn the user's decision.
static void requestAccess(void) {
    [AVCaptureDevice requestAccessForMediaType:AVMediaTypeAudio completionHandler:^(BOOL granted) {
        // No-op: the Go caller re-queries authStatus() to read the result.
        (void)granted;
    }];
}
*/
import "C"

// MicStatus returns the current microphone authorization state as one of:
// "granted", "denied", "not_determined", or "restricted".
func MicStatus() string {
	switch int(C.authStatus()) {
	case 0:
		return "not_determined"
	case 1:
		return "restricted"
	case 2:
		return "denied"
	case 3:
		return "granted"
	default:
		// Future-proofing: AVFoundation should never return anything else,
		// but if it does, surface it as not_determined so the caller will
		// re-prompt rather than silently treating it as denied.
		return "not_determined"
	}
}

// RequestMic triggers the macOS microphone permission prompt. It returns
// immediately; the caller should poll MicStatus to learn the user's decision.
// Calling this when the status is already "granted" or "denied" is a no-op
// from the user's perspective (no second prompt is shown).
func RequestMic() {
	C.requestAccess()
}
