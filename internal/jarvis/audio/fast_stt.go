package audio

import (
	"bufio"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/namanchopra/jarvis/internal/paths"
)

// FastTranscriber uses a long-lived Python faster-whisper process for
// near-instant speech-to-text. The model stays loaded in GPU memory
// between requests, avoiding the 2-5s cold start of whisper-cli.
//
// Protocol: send WAV path on stdin, read transcription text on stdout.
type FastTranscriber struct {
	scriptPath string
	model      string
	cmd        *exec.Cmd
	stdin      io.WriteCloser
	stdout     *bufio.Scanner
	mu         sync.Mutex
	running    bool
}

// NewFastTranscriber creates a FastTranscriber. It does NOT start the Python
// process yet — call Start() to launch it. The scriptPath should point to
// scripts/jarvis-stt-server.py.
func NewFastTranscriber(scriptPath string, model string) *FastTranscriber {
	if model == "" {
		model = "base.en"
	}
	return &FastTranscriber{
		scriptPath: scriptPath,
		model:      model,
	}
}

// Start launches the Python faster-whisper server process. It blocks until
// the server prints "READY" on stdout (model loaded), or returns an error
// if the process fails to start within 30 seconds.
func (f *FastTranscriber) Start() error {
	f.mu.Lock()
	defer f.mu.Unlock()

	if f.running {
		return nil
	}

	pythonPath, err := findPython()
	if err != nil {
		return fmt.Errorf("fast_stt: %w", err)
	}

	f.cmd = exec.Command(pythonPath, f.scriptPath, "--model", f.model)
	f.cmd.Stderr = os.Stderr // Python logs go to stderr

	stdin, err := f.cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("fast_stt: stdin pipe: %w", err)
	}
	f.stdin = stdin

	stdoutPipe, err := f.cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("fast_stt: stdout pipe: %w", err)
	}
	f.stdout = bufio.NewScanner(stdoutPipe)

	if err := f.cmd.Start(); err != nil {
		return fmt.Errorf("fast_stt: start failed: %w", err)
	}

	// Wait for READY signal.
	ready := make(chan bool, 1)
	go func() {
		if f.stdout.Scan() {
			line := strings.TrimSpace(f.stdout.Text())
			if line == "READY" {
				ready <- true
			} else {
				ready <- false
			}
		} else {
			ready <- false
		}
	}()

	select {
	case ok := <-ready:
		if !ok {
			f.cmd.Process.Kill()
			return fmt.Errorf("fast_stt: server did not send READY")
		}
	case <-time.After(30 * time.Second):
		f.cmd.Process.Kill()
		return fmt.Errorf("fast_stt: server startup timeout (30s)")
	}

	f.running = true
	slog.Info("fast STT server started", "model", f.model, "pid", f.cmd.Process.Pid)
	return nil
}

// Transcribe sends a WAV file path to the Python server and reads back
// the transcription. Returns empty string if no speech detected.
// Thread-safe — requests are serialized via mutex.
func (f *FastTranscriber) Transcribe(audio []float32) (string, error) {
	if len(audio) == 0 {
		return "", nil
	}

	f.mu.Lock()
	defer f.mu.Unlock()

	if !f.running {
		return "", fmt.Errorf("fast_stt: server not running")
	}

	// Write audio to a temp WAV file (same format as whisper-cli).
	tmpFile, err := os.CreateTemp("", "jarvis-fast-stt-*.wav")
	if err != nil {
		return "", fmt.Errorf("fast_stt: temp file: %w", err)
	}
	wavPath := tmpFile.Name()
	tmpFile.Close()

	defer os.Remove(wavPath)

	if err := writeWAV(wavPath, audio, whisperSampleRate); err != nil {
		return "", fmt.Errorf("fast_stt: write WAV: %w", err)
	}

	// Send the WAV path to the Python server.
	_, err = fmt.Fprintln(f.stdin, wavPath)
	if err != nil {
		return "", fmt.Errorf("fast_stt: write to server: %w", err)
	}

	// Read the transcription response.
	if !f.stdout.Scan() {
		return "", fmt.Errorf("fast_stt: server closed unexpectedly")
	}

	text := strings.TrimSpace(f.stdout.Text())
	return text, nil
}

// TranscribeFromMic reads audio from a running MicCapture, collects until
// silence, then transcribes. Same interface as Transcriber.TranscribeFromMic.
func (f *FastTranscriber) TranscribeFromMic(mic *MicCapture, maxDurationSec int) (string, error) {
	if maxDurationSec <= 0 {
		maxDurationSec = defaultMaxDurationSec
	}

	maxSamples := whisperSampleRate * maxDurationSec

	// Collect audio chunks, track silence, stop after silence threshold.
	// Minimum 2s of audio before checking silence — gives user time to
	// finish their sentence. "Hey Jarvis, what's the status?" takes ~2s.
	const minRecordMs = 2000

	chunkSamples := framesPerBuffer
	chunkDurationMs := float64(chunkSamples) / float64(whisperSampleRate) * 1000.0

	collected := make([]float32, 0, maxSamples)
	silentMs := 0.0
	totalMs := 0.0

	slog.Debug("fast_stt: TranscribeFromMic started",
		"max_duration_sec", maxDurationSec,
		"silence_threshold_ms", defaultSilenceThresholdMs,
	)

	for len(collected) < maxSamples {
		chunk, err := mic.ReadChunkFloat(chunkSamples)
		if err != nil {
			if len(collected) > 0 {
				slog.Debug("fast_stt: mic error, transcribing partial", "samples", len(collected))
				break
			}
			return "", fmt.Errorf("fast_stt: mic read failed: %w", err)
		}

		collected = append(collected, chunk...)
		totalMs += chunkDurationMs

		// Simple silence detection: check if chunk is below threshold.
		// Only start checking after minimum recording duration.
		var sum float64
		for _, s := range chunk {
			sum += float64(s) * float64(s)
		}
		rms := sum / float64(len(chunk))

		if rms < float64(defaultRMSThreshold) {
			silentMs += chunkDurationMs
			if totalMs >= minRecordMs && silentMs >= float64(defaultSilenceThresholdMs) {
				slog.Debug("fast_stt: silence detected, stopping",
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

	slog.Info("fast_stt: collected audio", "samples", len(collected),
		"duration_ms", float64(len(collected))/float64(whisperSampleRate)*1000)

	return f.Transcribe(collected)
}

// Stop gracefully shuts down the Python server.
func (f *FastTranscriber) Stop() {
	f.mu.Lock()
	defer f.mu.Unlock()

	if !f.running || f.cmd == nil {
		return
	}

	f.stdin.Close()
	f.cmd.Process.Kill()
	f.cmd.Wait()
	f.running = false
	slog.Info("fast STT server stopped")
}

// IsRunning returns whether the Python server is alive.
func (f *FastTranscriber) IsRunning() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.running
}

// findPython locates the Python 3 binary. Prefers the dedicated
// faster-whisper venv at ~/.jarvis/jarvis-stt-env/ if it exists.
func findPython() (string, error) {
	// Check the dedicated venv first.
	venvPy := paths.DataPath("jarvis-stt-env", "bin", "python3")
	if _, err := os.Stat(venvPy); err == nil {
		return venvPy, nil
	}
	for _, name := range []string{"python3", "python"} {
		if p, err := exec.LookPath(name); err == nil {
			return p, nil
		}
	}
	return "", fmt.Errorf("python3 not found in PATH")
}
