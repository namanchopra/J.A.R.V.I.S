// screencapture_darwin.m — ObjC implementation of the ScreenCaptureKit
// bridge. Compiled separately from screencapture_darwin.go so the ObjC
// class implementation (@implementation JarvisSCKAudioSink) only lives in
// one translation unit. If this were inlined in the cgo preamble, the
// preamble would be replicated across multiple cgo-generated .o files
// (one per Go file with `import "C"` plus the test binary's stub), and
// the linker would refuse with "duplicate symbol _OBJC_CLASS_..." errors.
//
// Build flags: see screencapture_darwin.go's cgo CFLAGS. ARC is enabled
// (-fobjc-arc) and macOS deployment target is pinned to 13.0
// (-mmacosx-version-min=13.0) so weak-linking against ScreenCaptureKit
// works on the minimum supported OS.
//
// Threading: the SCStreamOutput callback fires on the serial dispatch
// queue created here ("com.jarvis.sck.audio"). Frames arrive in order on
// that queue; the Go callback is invoked synchronously inside the
// delegate method.

#import <Cocoa/Cocoa.h>
#import <ScreenCaptureKit/ScreenCaptureKit.h>
#import <AVFoundation/AVFoundation.h>
#import <CoreMedia/CoreMedia.h>
#include <stdint.h>
#include <stdlib.h>
#include <string.h>

// Forward decl of the Go-exported callback. Must match cgo's emitted
// prototype in _cgo_export.h verbatim — no `const`, parameter names
// `handle/pcm/length`. Declared `extern` so the linker resolves it
// against the Go-side //export goAudioCallback symbol.
extern void goAudioCallback(uintptr_t handle, uint8_t *pcm, int length);

// Public C entrypoints (matched in screencapture_darwin.go's cgo preamble).
int sck_check_version(void);
int sck_start(uintptr_t handle);
void sck_stop(void);

// ----- JarvisSCKAudioSink -------------------------------------------------
//
// Owns the AVAudioConverter (cached per stream session; per-buffer
// instantiation is a measurable perf hit per Apple's AVAudioConverter
// sample code) and the cgo.Handle for the Go AudioCallback.

@interface JarvisSCKAudioSink : NSObject <SCStreamOutput, SCStreamDelegate>
@property (nonatomic, assign) uintptr_t goHandle;
@property (nonatomic, strong) AVAudioConverter *converter;
@property (nonatomic, strong) AVAudioFormat *srcFormat;
@property (nonatomic, strong) AVAudioFormat *dstFormat;
@end

@implementation JarvisSCKAudioSink

- (instancetype)initWithHandle:(uintptr_t)h {
    self = [super init];
    if (self) {
        _goHandle = h;
        // Source: SCK delivers 48 kHz stereo float32 non-interleaved.
        _srcFormat = [[AVAudioFormat alloc]
            initWithCommonFormat:AVAudioPCMFormatFloat32
                      sampleRate:48000.0
                        channels:2
                     interleaved:NO];
        // Destination: CanonicalAudioFormat — 16 kHz mono int16 interleaved.
        // Whisper-friendly. Matches the const in screencapture.go.
        _dstFormat = [[AVAudioFormat alloc]
            initWithCommonFormat:AVAudioPCMFormatInt16
                      sampleRate:16000.0
                        channels:1
                     interleaved:YES];
        _converter = [[AVAudioConverter alloc] initFromFormat:_srcFormat toFormat:_dstFormat];
        if (_converter == nil) {
            NSLog(@"[jarvis-sck] failed to create AVAudioConverter (src=%@ dst=%@)", _srcFormat, _dstFormat);
        }
    }
    return self;
}

// SCStreamDelegate — called for stream-level errors (not per-frame).
- (void)stream:(SCStream *)stream didStopWithError:(NSError *)error {
    NSLog(@"[jarvis-sck] stream stopped with error: %@", error);
}

// SCStreamOutput — fires for every audio sample buffer. We only register
// SCStreamOutputTypeAudio so type==audio always here, but defensive-check
// anyway in case Apple changes the dispatch semantics.
- (void)stream:(SCStream *)stream
    didOutputSampleBuffer:(CMSampleBufferRef)sampleBuffer
                   ofType:(SCStreamOutputType)type {
    if (type != SCStreamOutputTypeAudio) return;
    if (!CMSampleBufferDataIsReady(sampleBuffer)) return;
    if (self.converter == nil) return;

    CMFormatDescriptionRef fmtDesc = CMSampleBufferGetFormatDescription(sampleBuffer);
    if (fmtDesc == NULL) return;
    const AudioStreamBasicDescription *asbd = CMAudioFormatDescriptionGetStreamBasicDescription(fmtDesc);
    if (asbd == NULL) return;

    CMItemCount frameCount = CMSampleBufferGetNumSamples(sampleBuffer);
    if (frameCount <= 0) return;

    // Build an AVAudioFormat from the CMSampleBuffer's stream description
    // and an AVAudioPCMBuffer to hold the input frames. Copy in via
    // CMSampleBufferCopyPCMDataIntoAudioBufferList (the Apple-recommended
    // path for CMSampleBuffer -> AudioBufferList interop).
    AVAudioFormat *inFmt = [[AVAudioFormat alloc] initWithStreamDescription:asbd];
    if (inFmt == nil) return;

    AVAudioPCMBuffer *inputBuffer = [[AVAudioPCMBuffer alloc]
        initWithPCMFormat:inFmt
            frameCapacity:(AVAudioFrameCount)frameCount];
    if (inputBuffer == nil) return;
    inputBuffer.frameLength = (AVAudioFrameCount)frameCount;

    AudioBufferList *abl = inputBuffer.mutableAudioBufferList;
    OSStatus copyErr = CMSampleBufferCopyPCMDataIntoAudioBufferList(
        sampleBuffer,
        0,
        (int32_t)frameCount,
        abl);
    if (copyErr != noErr) {
        NSLog(@"[jarvis-sck] CMSampleBufferCopyPCMDataIntoAudioBufferList failed: %d", (int)copyErr);
        return;
    }

    // Output capacity: 48 kHz -> 16 kHz is 1/3 reduction; provision
    // frameCount * (16000/48000) + 16 slack frames.
    AVAudioFrameCount outCap = (AVAudioFrameCount)((double)frameCount * 16000.0 / 48000.0) + 16;
    AVAudioPCMBuffer *outputBuffer = [[AVAudioPCMBuffer alloc]
        initWithPCMFormat:self.dstFormat
            frameCapacity:outCap];
    if (outputBuffer == nil) return;

    // Feed AVAudioConverter via the block-based API. The block returns the
    // single prepared input buffer on first call, then signals NoDataNow
    // so the converter flushes any internal latency and returns.
    __block BOOL inputDelivered = NO;
    NSError *convErr = nil;
    AVAudioConverterOutputStatus status = [self.converter
        convertToBuffer:outputBuffer
                  error:&convErr
     withInputFromBlock:^AVAudioBuffer * _Nullable(AVAudioPacketCount inNumberOfPackets,
                                                   AVAudioConverterInputStatus * _Nonnull outStatus) {
        if (inputDelivered) {
            *outStatus = AVAudioConverterInputStatus_NoDataNow;
            return nil;
        }
        inputDelivered = YES;
        *outStatus = AVAudioConverterInputStatus_HaveData;
        return inputBuffer;
    }];

    if (status == AVAudioConverterOutputStatus_Error || convErr != nil) {
        // Failure-case policy (TASK-004 brief): drop the frame silently,
        // log via NSLog, do NOT tear down the stream. A single corrupt
        // buffer must not break a 60-minute meeting.
        NSLog(@"[jarvis-sck] AVAudioConverter failed: status=%ld err=%@",
              (long)status, convErr);
        return;
    }

    AVAudioFrameCount outFrames = outputBuffer.frameLength;
    if (outFrames == 0) return;

    AudioBufferList *outAbl = outputBuffer.mutableAudioBufferList;
    if (outAbl == NULL || outAbl->mNumberBuffers == 0) return;
    uint8_t *pcmBytes = (uint8_t *)outAbl->mBuffers[0].mData;
    UInt32 pcmBytesLen = outAbl->mBuffers[0].mDataByteSize;
    if (pcmBytes == NULL || pcmBytesLen == 0) return;

    // Hand off to Go. The Go side copies before returning, so we don't
    // need to hold the bytes alive past this call.
    goAudioCallback(self.goHandle, pcmBytes, (int)pcmBytesLen);
}

@end

// ----- Static globals for stream / sink ----------------------------------
//
// Only one capture session at a time. The Go side enforces this via the
// `active` boolean on darwinCapturer; we also defensively gate here
// (sck_start returns 3 if gStream is already non-nil).

static SCStream *gStream = nil;
static JarvisSCKAudioSink *gSink = nil;
static dispatch_queue_t gQueue = NULL;

// ----- Version check -----------------------------------------------------

int sck_check_version(void) {
    NSOperatingSystemVersion v = [[NSProcessInfo processInfo] operatingSystemVersion];
    return (v.majorVersion >= 13) ? 1 : 0;
}

// ----- Start -------------------------------------------------------------
//
// Returns:
//   0  = success
//   1  = unsupported OS (defence-in-depth; Go also checks)
//   2  = permission denied (SCStreamErrorUserDeclined or auth failure)
//   3  = generic SCK failure (logged via NSLog)
//
// Implementation: SCK's APIs (getShareableContent, startCapture) are async
// with completion handlers; we wrap them in dispatch_semaphore_t with a 3s
// timeout to convert async into sync. This keeps sck_start synchronous so
// Go callers see a single error return.

int sck_start(uintptr_t handle) {
    if (sck_check_version() == 0) return 1;
    if (gStream != nil) return 3; // already running — defensive

    __block SCShareableContent *content = nil;
    __block NSError *contentErr = nil;

    dispatch_semaphore_t sem = dispatch_semaphore_create(0);
    [SCShareableContent getShareableContentWithCompletionHandler:^(SCShareableContent *c, NSError *e) {
        content = c;
        contentErr = e;
        dispatch_semaphore_signal(sem);
    }];
    // 3s timeout — SCK is normally <100 ms; anything past this is broken.
    if (dispatch_semaphore_wait(sem, dispatch_time(DISPATCH_TIME_NOW, 3 * NSEC_PER_SEC)) != 0) {
        NSLog(@"[jarvis-sck] getShareableContent timed out");
        return 3;
    }

    if (contentErr != nil) {
        NSInteger code = contentErr.code;
        NSLog(@"[jarvis-sck] getShareableContent error: %@ (code=%ld)", contentErr, (long)code);
        // -3801 = SCStreamErrorUserDeclined, -3802 = SCStreamErrorFailedToStart.
        // Any SCK-domain error at the shareable-content stage is treated as
        // permission denied — the user sees a more nuanced message via NSLog.
        if (code == -3801 ||
            code == -3802 ||
            [contentErr.domain isEqualToString:@"com.apple.ScreenCaptureKit.SCStreamErrorDomain"]) {
            return 2;
        }
        return 3;
    }

    if (content == nil || content.displays.count == 0) {
        NSLog(@"[jarvis-sck] no displays available");
        return 3;
    }

    // Filter: main display, no excluded windows. System audio capture is
    // independent of which display we point at, but SCStream requires a
    // valid content filter, so we pick the first display.
    SCDisplay *display = content.displays.firstObject;
    SCContentFilter *filter = [[SCContentFilter alloc]
        initWithDisplay:display
       excludingWindows:@[]];

    SCStreamConfiguration *config = [[SCStreamConfiguration alloc] init];
    config.capturesAudio = YES;
    config.excludesCurrentProcessAudio = YES; // avoid Jarvis-TTS feedback
    config.sampleRate = 48000;
    config.channelCount = 2;
    // SCK requires positive width/height even when no video output is
    // registered. Tiny 2x2 frame at 1 fps keeps GPU/IOSurface cost negligible.
    config.width = 2;
    config.height = 2;
    config.minimumFrameInterval = CMTimeMake(1, 1);

    JarvisSCKAudioSink *sink = [[JarvisSCKAudioSink alloc] initWithHandle:handle];

    SCStream *stream = [[SCStream alloc]
        initWithFilter:filter
         configuration:config
              delegate:sink];

    if (gQueue == NULL) {
        gQueue = dispatch_queue_create("com.jarvis.sck.audio", DISPATCH_QUEUE_SERIAL);
    }

    NSError *addErr = nil;
    BOOL ok = [stream addStreamOutput:sink
                                 type:SCStreamOutputTypeAudio
                   sampleHandlerQueue:gQueue
                                error:&addErr];
    if (!ok) {
        NSLog(@"[jarvis-sck] addStreamOutput failed: %@", addErr);
        return 3;
    }

    __block NSError *startErr = nil;
    dispatch_semaphore_t sem2 = dispatch_semaphore_create(0);
    [stream startCaptureWithCompletionHandler:^(NSError *e) {
        startErr = e;
        dispatch_semaphore_signal(sem2);
    }];
    if (dispatch_semaphore_wait(sem2, dispatch_time(DISPATCH_TIME_NOW, 3 * NSEC_PER_SEC)) != 0) {
        NSLog(@"[jarvis-sck] startCapture timed out");
        return 3;
    }
    if (startErr != nil) {
        NSInteger code = startErr.code;
        NSLog(@"[jarvis-sck] startCapture error: %@ (code=%ld)", startErr, (long)code);
        if (code == -3801) return 2;
        return 3;
    }

    gStream = stream;
    gSink = sink;
    return 0;
}

// ----- Stop --------------------------------------------------------------
//
// Idempotent — if gStream is already nil, do nothing. ARC handles the
// actual releases when we drop the static refs. Fire-and-forget on
// stopCapture; Go's Stop() doesn't need to know the precise teardown
// moment.

void sck_stop(void) {
    if (gStream == nil) return;
    SCStream *s = gStream;
    gStream = nil;
    gSink = nil;
    [s stopCaptureWithCompletionHandler:^(NSError *e) {
        if (e != nil) {
            NSLog(@"[jarvis-sck] stopCapture error: %@", e);
        }
    }];
}
