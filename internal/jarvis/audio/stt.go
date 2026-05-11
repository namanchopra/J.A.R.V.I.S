// Package audio provides speech-to-text transcription for the Jarvis voice
// companion. It shells out to the whisper-cpp CLI binary rather than linking
// the C library via CGo, which avoids complex build-system entanglement with
// Wails and keeps the dependency tree clean.
package audio

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
)

// ---------------------------------------------------------------------------
// Constants
// ---------------------------------------------------------------------------

const (
	// defaultSilenceThresholdMs is how many milliseconds of silence must
	// elapse before TranscribeFromMic considers the utterance complete.
	defaultSilenceThresholdMs = 700 // balance between snappy and letting user finish

	// defaultMaxDurationSec is the maximum recording duration for a single
	// TranscribeFromMic invocation.
	defaultMaxDurationSec = 30

	// defaultRMSThreshold is the RMS amplitude below which a frame is
	// considered silence. Empirically, 0.01 works well for typical desktop
	// microphones at 16-bit / 16 kHz.
	defaultRMSThreshold float32 = 0.01

	// whisperSampleRate is the sample rate expected by whisper.cpp. Must
	// match the capture rate defined in capture.go (16 kHz).
	whisperSampleRate = 16000
)

// ---------------------------------------------------------------------------
// Errors
// ---------------------------------------------------------------------------

// ErrWhisperUnavailable indicates that the whisper-cpp binary or the GGML
// model file could not be found on this system.
var ErrWhisperUnavailable = errors.New("audio: whisper-cpp is unavailable")

// ---------------------------------------------------------------------------
// Transcriber
// ---------------------------------------------------------------------------

// Transcriber drives speech-to-text transcription by shelling out to the
// whisper-cpp CLI. It is safe for concurrent use; the mutex serialises calls
// to Transcribe so that only one whisper process runs at a time.
type Transcriber struct {
	modelPath          string
	binaryPath         string
	silenceThresholdMs int
	mu                 sync.Mutex
}

// NewTranscriber creates a Transcriber backed by the whisper-cpp CLI.
//
// modelPath is the path to a GGML Whisper model file. When empty, the default
// path (~/.awm/models/ggml-base.en.bin) is used. The constructor verifies that
// both the model file and the whisper-cpp binary exist, returning
// ErrWhisperUnavailable (wrapped with context) if either is missing.
func NewTranscriber(modelPath string) (*Transcriber, error) {
	if modelPath == "" {
		modelPath = defaultWhisperModelPath()
	}

	// --- model file ---
	if _, err := os.Stat(modelPath); err != nil {
		return nil, fmt.Errorf(
			"%w: model not found at %s -- run ./scripts/setup-jarvis.sh to download it",
			ErrWhisperUnavailable, modelPath,
		)
	}

	// --- whisper-cpp binary ---
	binPath, err := findWhisperBinary()
	if err != nil {
		return nil, err
	}

	slog.Info("transcriber initialised",
		"model", modelPath,
		"binary", binPath,
	)

	return &Transcriber{
		modelPath:          modelPath,
		binaryPath:         binPath,
		silenceThresholdMs: defaultSilenceThresholdMs,
	}, nil
}

// ---------------------------------------------------------------------------
// Public API
// ---------------------------------------------------------------------------

// Transcribe converts a buffer of float32 PCM audio (16 kHz, mono, range
// [-1.0, 1.0]) into text. It writes the samples to a temporary WAV file,
// invokes whisper-cpp, and parses the resulting text from stdout.
//
// An empty string (not an error) is returned when whisper detects no speech.
func (t *Transcriber) Transcribe(audio []float32) (string, error) {
	if len(audio) == 0 {
		return "", nil
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	// Write audio to a temporary WAV file.
	tmpFile, err := os.CreateTemp("", "jarvis-stt-*.wav")
	if err != nil {
		return "", fmt.Errorf("audio: failed to create temp WAV file: %w", err)
	}
	wavPath := tmpFile.Name()
	tmpFile.Close() // writeWAV reopens by path

	defer func() {
		if rmErr := os.Remove(wavPath); rmErr != nil {
			slog.Debug("could not remove temp WAV file", "path", wavPath, "err", rmErr)
		}
		// whisper-cpp with --output-txt writes a .txt file next to the input.
		txtPath := wavPath + ".txt"
		if rmErr := os.Remove(txtPath); rmErr != nil && !errors.Is(rmErr, os.ErrNotExist) {
			slog.Debug("could not remove temp txt file", "path", txtPath, "err", rmErr)
		}
	}()

	if err := writeWAV(wavPath, audio, whisperSampleRate); err != nil {
		return "", fmt.Errorf("audio: failed to write WAV file: %w", err)
	}

	// Run whisper-cpp.
	//   -m  model path
	//   -f  input WAV file
	//   -l  language (English)
	//   --no-timestamps  suppress [00:00.000 --> 00:05.000] prefixes
	//   --output-txt     write a .txt file (we still prefer stdout parsing)
	cmd := exec.Command(
		t.binaryPath,
		"-m", t.modelPath,
		"-f", wavPath,
		"--no-timestamps",
		"-l", "en",
		"--output-txt",
	)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	slog.Debug("running whisper-cpp",
		"binary", t.binaryPath,
		"model", t.modelPath,
		"wav", wavPath,
		"audio_samples", len(audio),
	)

	if err := cmd.Run(); err != nil {
		slog.Error("whisper-cpp failed",
			"err", err,
			"stderr", stderr.String(),
		)
		return "", fmt.Errorf("audio: whisper-cpp execution failed: %w", err)
	}

	text := parseWhisperOutput(stdout.String())

	if text == "" {
		slog.Debug("whisper returned no speech")
		return "", nil
	}

	slog.Info("transcription complete",
		"text_length", len(text),
	)
	return text, nil
}

// TranscribeFromMic reads audio from a running MicCapture, collects samples
// until silence is detected or maxDurationSec elapses, then transcribes the
// collected audio.
//
// Silence detection: consecutive frames whose RMS amplitude falls below
// defaultRMSThreshold for at least silenceThresholdMs are treated as the end
// of the utterance. If maxDurationSec is <= 0, defaultMaxDurationSec (30 s)
// is used.
func (t *Transcriber) TranscribeFromMic(mic *MicCapture, maxDurationSec int) (string, error) {
	if maxDurationSec <= 0 {
		maxDurationSec = defaultMaxDurationSec
	}

	// Each ReadChunkFloat call returns framesPerBuffer (512) samples at
	// 16 kHz, giving ~32 ms per chunk.
	chunkSamples := framesPerBuffer
	chunkDurationMs := float64(chunkSamples) / float64(whisperSampleRate) * 1000.0

	maxSamples := maxDurationSec * whisperSampleRate
	collected := make([]float32, 0, maxSamples)

	silentMs := 0.0

	slog.Debug("TranscribeFromMic started",
		"max_duration_sec", maxDurationSec,
		"silence_threshold_ms", t.silenceThresholdMs,
	)

	for len(collected) < maxSamples {
		chunk, err := mic.ReadChunkFloat(chunkSamples)
		if err != nil {
			// If capture stopped while we already have audio, transcribe
			// what we have rather than discarding it.
			if errors.Is(err, ErrCaptureStopped) && len(collected) > 0 {
				slog.Debug("capture stopped mid-recording, transcribing partial audio",
					"samples", len(collected),
				)
				break
			}
			return "", fmt.Errorf("audio: mic read failed: %w", err)
		}

		collected = append(collected, chunk...)

		if detectSilence(chunk, defaultRMSThreshold) {
			silentMs += chunkDurationMs
			if silentMs >= float64(t.silenceThresholdMs) {
				slog.Debug("silence detected, stopping recording",
					"silence_ms", silentMs,
					"total_samples", len(collected),
				)
				break
			}
		} else {
			silentMs = 0
		}
	}

	if len(collected) == 0 {
		return "", nil
	}

	return t.Transcribe(collected)
}

// ---------------------------------------------------------------------------
// WAV writer
// ---------------------------------------------------------------------------

// writeWAV writes PCM audio samples to a valid WAV file at path. The samples
// are float32 in the [-1.0, 1.0] range and are converted to 16-bit signed
// integers for the WAV data chunk.
//
// Format: RIFF/WAVE, PCM (format tag 1), 1 channel, sampleRate Hz, 16-bit.
func writeWAV(path string, samples []float32, sampleRate int) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()

	numSamples := len(samples)
	dataSize := uint32(numSamples * 2) // 2 bytes per int16 sample
	fileSize := uint32(36 + dataSize)  // 36 = header bytes before data chunk payload
	byteRate := uint32(sampleRate * 2) // sampleRate * numChannels * bytesPerSample
	blockAlign := uint16(2)            // numChannels * bytesPerSample

	// --- RIFF header ---
	if _, err := f.Write([]byte("RIFF")); err != nil {
		return err
	}
	if err := binary.Write(f, binary.LittleEndian, fileSize); err != nil {
		return err
	}
	if _, err := f.Write([]byte("WAVE")); err != nil {
		return err
	}

	// --- fmt sub-chunk ---
	if _, err := f.Write([]byte("fmt ")); err != nil {
		return err
	}
	if err := binary.Write(f, binary.LittleEndian, uint32(16)); err != nil { // sub-chunk size
		return err
	}
	if err := binary.Write(f, binary.LittleEndian, uint16(1)); err != nil { // PCM format
		return err
	}
	if err := binary.Write(f, binary.LittleEndian, uint16(1)); err != nil { // mono
		return err
	}
	if err := binary.Write(f, binary.LittleEndian, uint32(sampleRate)); err != nil {
		return err
	}
	if err := binary.Write(f, binary.LittleEndian, byteRate); err != nil {
		return err
	}
	if err := binary.Write(f, binary.LittleEndian, blockAlign); err != nil {
		return err
	}
	if err := binary.Write(f, binary.LittleEndian, uint16(16)); err != nil { // bits per sample
		return err
	}

	// --- data sub-chunk ---
	if _, err := f.Write([]byte("data")); err != nil {
		return err
	}
	if err := binary.Write(f, binary.LittleEndian, dataSize); err != nil {
		return err
	}

	// Convert float32 [-1.0, 1.0] to int16 and write.
	for _, s := range samples {
		// Clamp to [-1.0, 1.0] to avoid overflow when casting.
		if s > 1.0 {
			s = 1.0
		} else if s < -1.0 {
			s = -1.0
		}
		sample := int16(s * 32767)
		if err := binary.Write(f, binary.LittleEndian, sample); err != nil {
			return err
		}
	}

	return nil
}

// ---------------------------------------------------------------------------
// Silence detection
// ---------------------------------------------------------------------------

// detectSilence returns true if the RMS amplitude of the provided samples is
// below thresholdRMS. This is used to determine when the speaker has stopped
// talking. An empty slice is treated as silence.
func detectSilence(samples []float32, thresholdRMS float32) bool {
	if len(samples) == 0 {
		return true
	}

	var sumSquares float64
	for _, s := range samples {
		sumSquares += float64(s) * float64(s)
	}
	rms := float32(math.Sqrt(sumSquares / float64(len(samples))))

	return rms < thresholdRMS
}

// ---------------------------------------------------------------------------
// Internal helpers
// ---------------------------------------------------------------------------

// findWhisperBinary locates the whisper-cpp CLI binary on this machine. It
// checks PATH first (for "whisper-cpp" and "main"), then falls back to common
// Homebrew and local build locations.
func findWhisperBinary() (string, error) {
	// Names to search in PATH. "whisper-cli" is the binary name in Homebrew
	// whisper-cpp v1.8+; "whisper-cpp" is the old name; "main" is the default
	// build output of `make` in the whisper.cpp repo.
	pathNames := []string{"whisper-cli", "whisper-cpp", "main"}
	for _, name := range pathNames {
		if p, err := exec.LookPath(name); err == nil {
			slog.Debug("found whisper binary in PATH", "binary", name, "path", p)
			return p, nil
		}
	}

	// Well-known locations on macOS.
	wellKnown := []string{
		"/opt/homebrew/bin/whisper-cli",
		"/opt/homebrew/bin/whisper-cpp",
		"/usr/local/bin/whisper-cli",
		"/usr/local/bin/whisper-cpp",
	}
	for _, p := range wellKnown {
		if _, err := os.Stat(p); err == nil {
			slog.Debug("found whisper binary at well-known path", "path", p)
			return p, nil
		}
	}

	return "", fmt.Errorf(
		"%w: whisper-cpp binary not found in PATH or common locations\n\n"+
			"Install whisper.cpp: brew install whisper-cpp OR build from https://github.com/ggerganov/whisper.cpp",
		ErrWhisperUnavailable,
	)
}

// defaultWhisperModelPath returns the default path to the Whisper GGML model
// file (~/.awm/models/ggml-base.en.bin). This duplicates the logic from the
// parent jarvis package's WhisperModelPath() to avoid a circular import (the
// audio sub-package cannot import its parent).
func defaultWhisperModelPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(".", ".awm", "models", "ggml-base.en.bin")
	}
	return filepath.Join(home, ".awm", "models", "ggml-base.en.bin")
}

// parseWhisperOutput extracts the transcription text from whisper-cpp stdout.
// whisper-cpp prints system info lines to stderr and the actual transcription
// (one or more lines, possibly prefixed with whitespace) to stdout. We trim
// whitespace and join all non-empty lines.
func parseWhisperOutput(raw string) string {
	var lines []string
	for _, line := range strings.Split(raw, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		// Skip whisper-cpp diagnostic lines that sometimes leak to stdout.
		// These typically start with "whisper_" or "system_info" or contain
		// timing metrics like "encode" / "decode".
		if strings.HasPrefix(trimmed, "whisper_") ||
			strings.HasPrefix(trimmed, "system_info") {
			continue
		}
		lines = append(lines, trimmed)
	}
	result := strings.Join(lines, " ")

	// Filter out Whisper hallucinations on silence/noise. These are well-known
	// artifacts that whisper produces when there's no actual speech.
	hallucinations := []string{
		"[BLANK_AUDIO]",
		"[blank_audio]",
		"(blank audio)",
		"(bell dings)",
		"(bell ringing)",
		"(music)",
		"(music playing)",
		"(silence)",
		"(no speech)",
		"(inaudible)",
		"(sighs)",
		"(coughing)",
		"(laughing)",
		"(breathing)",
		"(clicking)",
		"(typing)",
		"[MUSIC]",
		"[SOUND]",
		"[NOISE]",
		"Thank you.",
		"Thanks for watching.",
		"Thanks for watching!",
		"Goodbye.",
		"you",
		"You",
		"Thank you for watching.",
		"Subtitles by the Amara.org community",
	}

	trimmed := strings.TrimSpace(result)
	for _, h := range hallucinations {
		if strings.EqualFold(trimmed, h) {
			slog.Debug("filtered whisper hallucination", "text", trimmed)
			return ""
		}
	}

	// Also filter if the entire output is just parenthesized/bracketed noise.
	if (strings.HasPrefix(trimmed, "(") && strings.HasSuffix(trimmed, ")")) ||
		(strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]")) {
		slog.Debug("filtered bracketed noise", "text", trimmed)
		return ""
	}

	return result
}
