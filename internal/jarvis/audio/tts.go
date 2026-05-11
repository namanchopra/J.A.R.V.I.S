// Package audio provides text-to-speech capabilities for the Jarvis voice
// companion. Supports two backends:
//   - macOS `say` command (free, local, decent quality)
//   - ElevenLabs API (paid, cloud, Jarvis-quality voice)
package audio

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/namanchopra/jarvis/internal/paths"
)

const defaultVoice = "Daniel"

// ElevenLabs voice IDs for Jarvis-like voices.
const (
	// "Antoni" — deep, warm, British-ish. Good Jarvis vibes.
	ElevenLabsVoiceAntoni = "ErXwobaYiN019PkySvjV"
	// "Adam" — deep, authoritative, clear.
	ElevenLabsVoiceAdam = "pNInz6obpgDQGcFmaJgB"
	// "Daniel" — British, warm, conversational.
	ElevenLabsVoiceDaniel = "onwK4e9ZLuTAKqWW03F9"
)

// Speaker drives text-to-speech. Supports macOS `say` (free) or
// ElevenLabs (paid, much better quality). Safe for concurrent use.
// EdgeTTS voice options — British male voices for Jarvis-like feel.
const (
	EdgeVoiceRyan   = "en-GB-RyanNeural"   // British male, calm, clear — best Jarvis match
	EdgeVoiceThomas = "en-GB-ThomasNeural" // British male, deeper tone
)

type Speaker struct {
	voice             string
	edgeTTSVoice      string // Edge TTS voice name (free, neural)
	edgeTTSBinary     string // path to edge-tts binary
	elevenLabsKey     string
	elevenLabsVoiceID string
	cmd               *exec.Cmd
	stopStream        chan struct{} // closed by Stop() to abort SpeakStream
	mu                sync.Mutex
}

// NewSpeaker creates a Speaker. Auto-detects Edge TTS (free, Jarvis-quality)
// and uses it if available. Falls back to macOS `say` if not installed.
func NewSpeaker(voice string) *Speaker {
	if voice == "" {
		voice = defaultVoice
	}
	s := &Speaker{voice: voice}

	// Auto-detect Edge TTS — free neural voice, sounds like Jarvis.
	edgePaths := []string{
		paths.DataPath("edge-tts-env", "bin", "edge-tts"),
		"/opt/homebrew/bin/edge-tts",
		"/usr/local/bin/edge-tts",
	}
	for _, p := range edgePaths {
		if _, err := os.Stat(p); err == nil {
			s.edgeTTSBinary = p
			s.edgeTTSVoice = EdgeVoiceRyan
			slog.Info("tts: using Edge TTS (free neural voice)", "voice", EdgeVoiceRyan)
			return s
		}
	}
	// Also check PATH.
	if p, err := exec.LookPath("edge-tts"); err == nil {
		s.edgeTTSBinary = p
		s.edgeTTSVoice = EdgeVoiceRyan
		slog.Info("tts: using Edge TTS (free neural voice)", "voice", EdgeVoiceRyan)
		return s
	}

	slog.Info("tts: using macOS say (install edge-tts for better voice)")
	return s
}

// NewElevenLabsSpeaker creates a Speaker that uses ElevenLabs for
// high-quality AI voice. Falls back to macOS `say` if the API call fails.
func NewElevenLabsSpeaker(apiKey, voiceID string) *Speaker {
	if voiceID == "" {
		voiceID = ElevenLabsVoiceDaniel // British, warm — best Jarvis match
	}
	return &Speaker{
		voice:             defaultVoice, // fallback
		elevenLabsKey:     apiKey,
		elevenLabsVoiceID: voiceID,
	}
}

// Speak plays text through TTS and blocks until speech finishes.
// If ElevenLabs is configured, uses that for high-quality voice.
// Falls back to macOS `say` if ElevenLabs fails or isn't configured.
func (s *Speaker) Speak(text string) error {
	if text == "" {
		return nil
	}

	// Priority: Edge TTS (free, neural) > ElevenLabs (paid) > macOS say (local)
	s.mu.Lock()
	hasEdge := s.edgeTTSBinary != ""
	hasElevenLabs := s.elevenLabsKey != ""
	s.mu.Unlock()

	if hasEdge {
		if err := s.speakEdgeTTS(text); err != nil {
			slog.Warn("Edge TTS failed, trying fallback", "err", err)
		} else {
			return nil
		}
	}

	if hasElevenLabs {
		if err := s.speakElevenLabs(text); err != nil {
			slog.Warn("ElevenLabs TTS failed, falling back to macOS say", "err", err)
		} else {
			return nil
		}
	}

	// Final fallback: macOS `say`
	s.mu.Lock()
	voice := s.voice
	s.mu.Unlock()

	err := s.runSay(voice, text)
	if err == nil {
		return nil
	}

	slog.Warn("configured voice failed, falling back to system default",
		"voice", voice,
		"err", err,
	)
	return s.runSay("", text)
}

// speakEdgeTTS uses Microsoft Edge TTS (free, neural voices, no API key).
// Generates an MP3 file and plays it via afplay.
func (s *Speaker) speakEdgeTTS(text string) error {
	s.mu.Lock()
	binary := s.edgeTTSBinary
	voice := s.edgeTTSVoice
	s.mu.Unlock()

	tmpFile, err := os.CreateTemp("", "jarvis-tts-*.mp3")
	if err != nil {
		return fmt.Errorf("edge-tts: temp file failed: %w", err)
	}
	tmpPath := tmpFile.Name()
	tmpFile.Close()
	defer os.Remove(tmpPath)

	// Generate the audio file.
	genCmd := exec.Command(binary,
		"--voice", voice,
		"--text", text,
		"--write-media", tmpPath,
	)
	if out, err := genCmd.CombinedOutput(); err != nil {
		return fmt.Errorf("edge-tts generate failed: %w (output: %s)", err, string(out))
	}

	// Play the audio file.
	playCmd := exec.Command("afplay", tmpPath)

	s.mu.Lock()
	s.cmd = playCmd
	s.mu.Unlock()

	err = playCmd.Run()

	s.mu.Lock()
	s.cmd = nil
	s.mu.Unlock()

	return err
}

// speakElevenLabs calls the ElevenLabs text-to-speech API and plays the
// returned audio via afplay (macOS audio player).
func (s *Speaker) speakElevenLabs(text string) error {
	s.mu.Lock()
	apiKey := s.elevenLabsKey
	voiceID := s.elevenLabsVoiceID
	s.mu.Unlock()

	url := fmt.Sprintf("https://api.elevenlabs.io/v1/text-to-speech/%s", voiceID)

	body, _ := json.Marshal(map[string]interface{}{
		"text":     text,
		"model_id": "eleven_turbo_v2_5",
		"voice_settings": map[string]interface{}{
			"stability":        0.5,
			"similarity_boost": 0.75,
			"style":            0.3,
		},
	})

	req, err := http.NewRequest("POST", url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("elevenlabs: request creation failed: %w", err)
	}
	req.Header.Set("xi-api-key", apiKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "audio/mpeg")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("elevenlabs: request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("elevenlabs: status %d: %s", resp.StatusCode, string(respBody))
	}

	// Write audio to temp file and play with afplay.
	tmpFile, err := os.CreateTemp("", "jarvis-tts-*.mp3")
	if err != nil {
		return fmt.Errorf("elevenlabs: temp file creation failed: %w", err)
	}
	defer os.Remove(tmpFile.Name())

	if _, err := io.Copy(tmpFile, resp.Body); err != nil {
		tmpFile.Close()
		return fmt.Errorf("elevenlabs: writing audio failed: %w", err)
	}
	tmpFile.Close()

	// Play the audio file.
	cmd := exec.Command("afplay", tmpFile.Name())

	s.mu.Lock()
	s.cmd = cmd
	s.mu.Unlock()

	err = cmd.Run()

	s.mu.Lock()
	s.cmd = nil
	s.mu.Unlock()

	return err
}

// SpeakAsync starts speaking text in a background goroutine and returns a
// channel that is closed once speech completes (or fails). Callers can
// select on the returned channel to coordinate concurrent work.
func (s *Speaker) SpeakAsync(text string) <-chan struct{} {
	done := make(chan struct{})
	go func() {
		defer close(done)
		if err := s.Speak(text); err != nil {
			slog.Error("async speech failed", "err", err)
		}
	}()
	return done
}

// SpeakStream reads sentences from the channel and speaks them
// sequentially. Each sentence starts playing as soon as it arrives (no
// batching). Returns a channel that closes when all sentences have been
// spoken or when Stop() is called.
//
// This enables overlapping LLM generation and speech — sentence 1 speaks
// while sentences 2..N are still being generated.
func (s *Speaker) SpeakStream(sentences <-chan string) <-chan struct{} {
	done := make(chan struct{})

	// Create a stop channel for this stream. Any previous stream's
	// channel was already nil-ed or closed, so this is safe.
	stop := make(chan struct{})
	s.mu.Lock()
	s.stopStream = stop
	s.mu.Unlock()

	go func() {
		defer close(done)
		for {
			select {
			case <-stop:
				return
			case sentence, ok := <-sentences:
				if !ok {
					// Channel closed — all sentences received.
					return
				}
				sentence = strings.TrimSpace(sentence)
				if sentence == "" {
					continue
				}
				if err := s.Speak(sentence); err != nil {
					// Check whether we were stopped. Speak returns
					// an error when the underlying process is killed,
					// so a stop signal means this is intentional.
					select {
					case <-stop:
						return
					default:
						slog.Error("stream speech failed", "err", err)
					}
				}
			}
		}
	}()

	return done
}

// Stop interrupts any speech that is currently playing by killing the
// underlying `say` process. If a SpeakStream goroutine is active it is
// also signalled to stop processing remaining sentences. Returns nil if
// nothing is playing.
func (s *Speaker) Stop() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Signal any active SpeakStream goroutine to stop.
	if s.stopStream != nil {
		close(s.stopStream)
		s.stopStream = nil
	}

	if s.cmd == nil || s.cmd.Process == nil {
		return nil
	}

	if err := s.cmd.Process.Kill(); err != nil {
		// Process may have already exited between the nil check and
		// the kill call. That is not an error worth surfacing.
		if !errors.Is(err, exec.ErrNotFound) && !strings.Contains(err.Error(), "process already finished") {
			return err
		}
	}

	// Wait so the OS can reclaim the process entry (avoid zombies).
	// Ignore the error — it will be "signal: killed" which is expected.
	_ = s.cmd.Wait()

	slog.Info("speech interrupted")
	s.cmd = nil
	return nil
}

// SetVoice changes the macOS voice used for subsequent Speak calls.
// It does not affect speech that is already in progress.
func (s *Speaker) SetVoice(voice string) {
	if voice == "" {
		voice = defaultVoice
	}
	s.mu.Lock()
	s.voice = voice
	s.mu.Unlock()

	slog.Info("voice changed", "voice", voice)
}

// ListVoices returns the names of all macOS voices installed on this machine.
// It parses the output of `say -v '?'`, where each line has the format:
//
//	Name    lang  # description
func ListVoices() ([]string, error) {
	cmd := exec.Command("say", "-v", "?")
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}

	var voices []string
	scanner := bufio.NewScanner(bytes.NewReader(out))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		// Voice names may contain spaces (e.g. "Bad News"), but every
		// line has a two-letter language tag separated by at least two
		// spaces from the voice name. Split on the double-space boundary
		// to extract the name reliably.
		name := extractVoiceName(line)
		if name != "" {
			voices = append(voices, name)
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return voices, nil
}

// extractVoiceName pulls the voice name from a single `say -v '?'` output
// line. The format is:
//
//	Alex                en_US    # Most people recognize me by my voice.
//
// The name runs from the start of the line up to (but not including) the
// first run of two or more consecutive spaces.
func extractVoiceName(line string) string {
	// Find the first occurrence of two consecutive spaces — that marks
	// the boundary between the voice name and the language tag.
	idx := strings.Index(line, "  ")
	if idx <= 0 {
		return ""
	}
	return strings.TrimSpace(line[:idx])
}

// runSay executes the macOS `say` command. When voice is non-empty the
// -v flag is included; when empty the system default voice is used.
// The currently running process is tracked in s.cmd so that Stop() can
// kill it.
func (s *Speaker) runSay(voice, text string) error {
	var args []string
	if voice != "" {
		args = append(args, "-v", voice)
	}
	args = append(args, text)

	cmd := exec.Command("say", args...)

	s.mu.Lock()
	s.cmd = cmd
	s.mu.Unlock()

	err := cmd.Run()

	s.mu.Lock()
	s.cmd = nil
	s.mu.Unlock()

	return err
}
