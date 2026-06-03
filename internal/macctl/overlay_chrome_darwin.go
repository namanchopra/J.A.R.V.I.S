//go:build darwin

// overlay_chrome_darwin.go — Cocoa bridge to flip the main Wails window
// between titled (default Mac chrome) and borderless (overlay form factor).
//
// Why a CGO bridge: Wails v2 single-window means we can't have one window
// with chrome and another without. Toggling NSWindowStyleMask at runtime is
// the only way to keep the main HUD with normal Mac controls while making
// the overlay truly frameless.
//
// Why it's safe to assume window index 0: Wails v2 only constructs a single
// NSWindow for the lifetime of the app. We grab it via [NSApp.windows
// firstObject]. Any future plugin that opens additional windows would need
// to revisit this.
//
// Threading: all NSWindow mutation must run on the main thread. The CGO
// function dispatches via dispatch_async to the main queue so callers can
// invoke from any goroutine without blocking.

package macctl

/*
#cgo CFLAGS: -x objective-c
#cgo LDFLAGS: -framework Cocoa
#import <Cocoa/Cocoa.h>

static void overlay_setMainWindowFrameless(int frameless) {
    dispatch_async(dispatch_get_main_queue(), ^{
        NSArray<NSWindow *> *windows = [NSApp windows];
        if (windows.count == 0) return;
        NSWindow *w = windows.firstObject;

        NSWindowStyleMask mask = [w styleMask];
        if (frameless) {
            // Strip everything that puts chrome on the window.
            mask &= ~(NSWindowStyleMaskTitled
                    | NSWindowStyleMaskClosable
                    | NSWindowStyleMaskMiniaturizable
                    | NSWindowStyleMaskResizable);
            mask |= NSWindowStyleMaskBorderless;
            // Borderless NSWindows return NO from canBecomeKeyWindow by
            // default which would block keyboard events (including Escape).
            // We can't override that without subclassing; instead we use
            // activateIgnoringOtherApps below to put Jarvis in the
            // foreground -- the WebView still receives key events from the
            // active app even on a non-key NSWindow.
        } else {
            // Restore the default titled chrome set Wails creates at boot.
            mask |= NSWindowStyleMaskTitled
                  | NSWindowStyleMaskClosable
                  | NSWindowStyleMaskMiniaturizable
                  | NSWindowStyleMaskResizable;
            mask &= ~NSWindowStyleMaskBorderless;
        }
        [w setStyleMask:mask];

        // Activate Jarvis so the new window state is visible and receives
        // focus. activateIgnoringOtherApps is what closes the gap when the
        // global hotkey fires from another app: without this, the overlay
        // appears but the previously-focused app keeps keyboard focus.
        [NSApp activateIgnoringOtherApps:YES];
    });
}
*/
import "C"

// SetMainWindowFrameless flips the Wails main window between its default
// titled chrome and a borderless overlay style. Frameless=true also brings
// Jarvis to the foreground via NSApplication.activateIgnoringOtherApps so
// the overlay actually gets focus when the global hotkey fires from
// another app.
//
// Idempotent: calling twice with the same value is a no-op at the Cocoa
// level (the style mask already matches). Safe to call from any goroutine
// — the underlying Cocoa work runs on the main thread via dispatch_async.
func SetMainWindowFrameless(frameless bool) {
	flag := C.int(0)
	if frameless {
		flag = 1
	}
	C.overlay_setMainWindowFrameless(flag)
}
