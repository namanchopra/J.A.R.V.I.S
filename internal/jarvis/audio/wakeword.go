package audio

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"

	porcupine "github.com/Picovoice/porcupine/binding/go/v3"
)

// ---------------------------------------------------------------------------
// Errors
// ---------------------------------------------------------------------------

// ErrPorcupineUnavailable indicates that the Porcupine engine could not be
// initialised. Common causes include missing native library files for the
// current platform, an invalid or expired access key, or a corrupt/missing
// keyword model file.
var ErrPorcupineUnavailable = errors.New("audio: porcupine engine unavailable")

// ---------------------------------------------------------------------------
// WakeWordDetector
// ---------------------------------------------------------------------------

// WakeWordDetector listens to a MicCapture audio stream and invokes a
// callback whenever the configured wake word is detected. Detection is
// powered by the Picovoice Porcupine on-device wake word engine.
//
// By default the built-in "Jarvis" keyword is used. When modelPath is set
// to a custom .ppn file, that keyword model is used instead.
//
// The detector does not own the MicCapture — the caller is responsible for
// starting and stopping the mic independently.
type WakeWordDetector struct {
	accessKey   string
	modelPath   string
	sensitivity float32
	mic         *MicCapture
	mu          sync.Mutex
}

// NewWakeWordDetector creates a WakeWordDetector configured with the given
// Picovoice access key, optional custom keyword model path, sensitivity,
// and a shared MicCapture instance.
//
// If sensitivity is <= 0 it defaults to 0.5. Sensitivity values closer to
// 1.0 increase the detection rate at the cost of more false positives;
// values closer to 0.0 reduce false positives but may miss wake words
// spoken quietly or in noisy environments.
func NewWakeWordDetector(accessKey string, modelPath string, sensitivity float32, mic *MicCapture) *WakeWordDetector {
	if sensitivity <= 0 {
		sensitivity = 0.5
	}
	return &WakeWordDetector{
		accessKey:   accessKey,
		modelPath:   modelPath,
		sensitivity: sensitivity,
		mic:         mic,
	}
}

// ---------------------------------------------------------------------------
// Detection loop
// ---------------------------------------------------------------------------

// Start runs the wake word detection loop, reading audio frames from the
// MicCapture and passing them through the Porcupine engine. When the wake
// word is detected, onDetected is called synchronously (the detection loop
// blocks until the callback returns).
//
// Start blocks until ctx is cancelled, at which point it cleans up the
// Porcupine instance and returns nil. It returns a non-nil error only if
// the engine cannot be initialised (e.g. missing access key, invalid model,
// or native library not found).
func (w *WakeWordDetector) Start(ctx context.Context, onDetected func()) error {
	// -----------------------------------------------------------------------
	// Validate access key
	// -----------------------------------------------------------------------
	if w.accessKey == "" {
		return fmt.Errorf(
			"audio: Picovoice access key required — sign up free at console.picovoice.ai",
		)
	}

	// -----------------------------------------------------------------------
	// Create and initialise Porcupine
	// -----------------------------------------------------------------------
	p := porcupine.Porcupine{
		AccessKey:     w.accessKey,
		Sensitivities: []float32{w.sensitivity},
	}

	if w.modelPath != "" {
		p.KeywordPaths = []string{w.modelPath}
		slog.Info("wake word detector using custom keyword model",
			"model_path", w.modelPath,
			"sensitivity", w.sensitivity,
		)
	} else {
		p.BuiltInKeywords = []porcupine.BuiltInKeyword{porcupine.JARVIS}
		slog.Info("wake word detector using built-in keyword",
			"keyword", "Jarvis",
			"sensitivity", w.sensitivity,
		)
	}

	if err := p.Init(); err != nil {
		return fmt.Errorf("%w: %w", ErrPorcupineUnavailable, err)
	}
	defer func() {
		if err := p.Delete(); err != nil {
			slog.Warn("failed to release porcupine resources", "err", err)
		}
	}()

	frameLength := porcupine.FrameLength
	slog.Info("wake word detection started",
		"frame_length", frameLength,
		"sample_rate", porcupine.SampleRate,
	)

	// -----------------------------------------------------------------------
	// Detection loop — read frames and process until ctx is cancelled
	// -----------------------------------------------------------------------
	for {
		// Check for cancellation before blocking on mic read.
		select {
		case <-ctx.Done():
			slog.Info("wake word detection stopped")
			return nil
		default:
		}

		frame, err := w.mic.ReadChunk(frameLength)
		if err != nil {
			// If the mic capture was stopped we should exit cleanly rather
			// than spinning on repeated errors.
			if errors.Is(err, ErrCaptureStopped) {
				slog.Info("wake word detection exiting: mic capture stopped")
				return nil
			}
			// Transient mic read errors (e.g. buffer underrun) are logged
			// and retried. The next iteration will check ctx again.
			slog.Warn("wake word: mic read error, retrying", "err", err)
			continue
		}

		keywordIndex, err := p.Process(frame)
		if err != nil {
			slog.Warn("wake word: process error", "err", err)
			continue
		}

		if keywordIndex >= 0 {
			slog.Info("wake word detected", "keyword_index", keywordIndex)
			onDetected()
		}
	}
}

// Stop is a no-op provided for API symmetry with other subsystems. The
// detection loop is cancelled via the context passed to Start.
func (w *WakeWordDetector) Stop() {
	// Cancellation is handled by the ctx passed to Start.
}
