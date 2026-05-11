package audio

import (
	"context"
	"log/slog"
	"math"
	"sync"
	"time"
)

// VAD (Voice Activity Detector) monitors audio from a MicCapture and fires
// callbacks when speech starts and stops. Uses RMS energy thresholds — no
// external dependencies, no accounts, no wake words. Just talk.
type VAD struct {
	mic *MicCapture

	// SpeechThreshold is the minimum RMS energy to consider as speech.
	// Default: 0.015 (works well for normal speaking volume).
	SpeechThreshold float64

	// SilenceDuration is how long silence must last to consider speech ended.
	// Default: 600ms.
	SilenceDuration time.Duration

	// SpeechMinDuration is the minimum speech duration before triggering.
	// Filters out brief noises (keyboard clicks, coughs). Default: 300ms.
	SpeechMinDuration time.Duration

	mu      sync.Mutex
	running bool
	muted   bool // when true, VAD ignores all audio (used during TTS to prevent feedback)
}

// NewVAD creates a Voice Activity Detector attached to the given MicCapture.
func NewVAD(mic *MicCapture) *VAD {
	return &VAD{
		mic:               mic,
		SpeechThreshold:   0.015,
		SilenceDuration:   350 * time.Millisecond,
		SpeechMinDuration: 200 * time.Millisecond,
	}
}

// Listen runs the VAD loop. It calls onSpeechStart when voice is detected and
// onSpeechEnd when silence returns. Blocks until ctx is cancelled.
//
// The typical flow:
//  1. Silence... silence... silence...
//  2. User starts talking → onSpeechStart()
//  3. User stops talking → onSpeechEnd()
//  4. Back to step 1
func (v *VAD) Listen(ctx context.Context, onSpeechStart func(), onSpeechEnd func()) error {
	v.mu.Lock()
	if v.running {
		v.mu.Unlock()
		return nil
	}
	v.running = true
	v.mu.Unlock()

	defer func() {
		v.mu.Lock()
		v.running = false
		v.mu.Unlock()
	}()

	const chunkSize = 512 // ~32ms at 16kHz

	var (
		inSpeech      bool
		speechStart   time.Time
		silenceStart  time.Time
		silenceActive bool
	)

	slog.Info("vad: listening for speech", "threshold", v.SpeechThreshold)

	for {
		select {
		case <-ctx.Done():
			return nil
		default:
		}

		samples, err := v.mic.ReadChunkFloat(chunkSize)
		if err != nil {
			if err == ErrCaptureStopped {
				return nil
			}
			slog.Debug("vad: mic read error, retrying", "err", err)
			continue
		}

		// When muted (e.g., during TTS playback), consume audio but ignore it.
		v.mu.Lock()
		muted := v.muted
		v.mu.Unlock()
		if muted {
			continue
		}

		energy := rmsEnergy(samples)

		if !inSpeech {
			// Waiting for speech to start.
			if energy >= v.SpeechThreshold {
				if speechStart.IsZero() {
					speechStart = time.Now()
				}
				// Only trigger after minimum duration (filters brief noises).
				if time.Since(speechStart) >= v.SpeechMinDuration {
					inSpeech = true
					silenceActive = false
					slog.Debug("vad: speech detected", "energy", energy)
					if onSpeechStart != nil {
						onSpeechStart()
					}
				}
			} else {
				speechStart = time.Time{} // reset
			}
		} else {
			// In speech — waiting for silence to end it.
			if energy < v.SpeechThreshold {
				if !silenceActive {
					silenceStart = time.Now()
					silenceActive = true
				}
				if time.Since(silenceStart) >= v.SilenceDuration {
					inSpeech = false
					silenceActive = false
					speechStart = time.Time{}
					slog.Debug("vad: speech ended", "silence_ms", v.SilenceDuration.Milliseconds())
					if onSpeechEnd != nil {
						onSpeechEnd()
					}
				}
			} else {
				silenceActive = false // speech resumed
			}
		}
	}
}

// Mute pauses speech detection. Audio is still consumed from the mic
// (so buffers don't overflow) but all frames are ignored. Use this
// during TTS playback to prevent Jarvis from hearing itself.
func (v *VAD) Mute() {
	v.mu.Lock()
	v.muted = true
	v.mu.Unlock()
	slog.Debug("vad: muted")
}

// Unmute resumes speech detection after a mute. Drains any buffered audio
// (echo from TTS) and waits for reverb to die before listening again.
func (v *VAD) Unmute() {
	// Wait for echo and reverb to fully fade before listening again.
	// 1.5s is needed because speakers (especially laptop) produce reverb
	// that lingers after TTS finishes. Too short = Jarvis hears itself.
	time.Sleep(1500 * time.Millisecond)
	// Drain any audio captured during speech (it's just echo).
	v.mic.Drain()
	v.mu.Lock()
	v.muted = false
	v.mu.Unlock()
	slog.Debug("vad: unmuted, buffer drained")
}

// IsRunning returns true if the VAD loop is active.
func (v *VAD) IsRunning() bool {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.running
}

// rmsEnergy computes the root-mean-square of the audio samples.
func rmsEnergy(samples []float32) float64 {
	if len(samples) == 0 {
		return 0
	}
	var sum float64
	for _, s := range samples {
		sum += float64(s) * float64(s)
	}
	return math.Sqrt(sum / float64(len(samples)))
}
