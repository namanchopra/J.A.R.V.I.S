package jarvis

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/namanchopra/jarvis/internal/jarvis/audio"
	"github.com/namanchopra/jarvis/internal/paths"
)

// JarvisState represents the current phase of the Jarvis voice companion.
type JarvisState string

const (
	JarvisIdle      JarvisState = "idle"
	JarvisListening JarvisState = "listening"
	JarvisThinking  JarvisState = "thinking"
	JarvisSpeaking  JarvisState = "speaking"
)

// JarvisEvent is emitted to the frontend whenever Jarvis's state or conversation changes.
type JarvisEvent struct {
	Type      string    `json:"type"`                // "state_change", "message", "error", "audio_level"
	State     JarvisState  `json:"state"`               // current state
	Text      string    `json:"text"`                // message text (for "message" events)
	Role      string    `json:"role"`                // "user" or "jarvis" (for "message" events)
	Level     float64   `json:"level,omitempty"`     // audio level 0-1 (for "audio_level" events)
	Timestamp time.Time `json:"timestamp"`           // when the event occurred
}

// Message represents a single chat message in the conversation history.
type Message struct {
	Role      string    `json:"role"`      // "user", "assistant", "system"
	Content   string    `json:"content"`   // message body
	Timestamp time.Time `json:"timestamp"` // when the message was created
}

// JarvisConfig holds all configuration for the Jarvis voice companion.
type JarvisConfig struct {
	Enabled             bool    `json:"enabled"`
	Provider            string  `json:"provider"` // "cli" (default, uses claude CLI) or "api" (uses Anthropic API key)
	APIKey              string  `json:"apiKey"`
	Voice               string  `json:"voice"` // macOS voice name, default "Daniel"
	AmbientEnabled      bool    `json:"ambientEnabled"`
	Verbosity           string  `json:"verbosity"` // "concise" or "detailed"
	PicovoiceAccessKey  string  `json:"picovoiceAccessKey"`
	WakeWordModelPath   string  `json:"wakeWordModelPath"`   // custom .ppn path, empty = built-in "Jarvis"
	WakeWordSensitivity float32 `json:"wakeWordSensitivity"` // 0.0-1.0, default 0.5
	ElevenLabsKey       string  `json:"elevenLabsKey"`       // ElevenLabs API key for high-quality voice
	ElevenLabsVoiceID   string  `json:"elevenLabsVoiceId"`   // ElevenLabs voice ID
}

// followUpTimeout is the duration the voice loop waits for a follow-up
// utterance before returning to wake-word mode.
const followUpTimeout = 5 * time.Second

// stepTimeout is the maximum duration any single pipeline step (transcribe,
// think, speak) may take before the loop falls back to idle.
const stepTimeout = 30 * time.Second

// dismissPhrases are utterances that explicitly end a multi-turn conversation.
var dismissPhrases = []string{
	"thanks",
	"thank you",
	"that's all",
	"thats all",
	"that is all",
	"goodbye",
	"bye",
	"nevermind",
	"never mind",
}

// Jarvis is the top-level orchestrator for the AI voice companion.
// It manages state transitions, delegates to subsystems (STT, LLM, TTS),
// and emits events to the frontend via the Wails runtime.
type Jarvis struct {
	state   JarvisState
	config  JarvisConfig
	ambient bool // true when ambient listening (mic + wake word only) is active
	mu      sync.RWMutex
	emitFn  func(JarvisEvent)
	ctx     context.Context
	cancel  context.CancelFunc

	// ambientCancel cancels only the ambient listening goroutine (wake word
	// detection) without tearing down the full orchestrator context.
	ambientCancel context.CancelFunc

	// --- audio level emitter ---
	audioLevelStop chan struct{} // closed to stop the audio level emitter goroutine

	// --- subsystems ---
	mic             *audio.MicCapture
	wakeWord        *audio.WakeWordDetector
	vad             *audio.VAD
	transcriber     *audio.Transcriber
	fastTranscriber *audio.FastTranscriber
	speaker         *audio.Speaker
	chatClient   *ChatClient
	conversation *Conversation
	context      ContextProvider
	commands     *CommandRouter
	analyzer     *OutputAnalyzer
	monitor      *SessionMonitor
}

// NewJarvis creates a Jarvis orchestrator with the given configuration.
// The emitFn callback is invoked on every state or message change
// so the frontend can stay in sync.
//
// Subsystem initialisation failures (e.g. missing Whisper model) are logged
// as warnings rather than fatal errors so that Jarvis can still operate in
// text-only mode.
func NewJarvis(cfg JarvisConfig, ctxProvider ContextProvider, actionProvider ActionProvider, getOutput func(int) (string, error), emitFn func(JarvisEvent)) *Jarvis {
	v := &Jarvis{
		state:  JarvisIdle,
		config: cfg,
		emitFn: emitFn,
	}

	// --- Audio subsystems ---
	v.mic = audio.NewMicCapture()

	v.vad = audio.NewVAD(v.mic)

	v.wakeWord = audio.NewWakeWordDetector(
		cfg.PicovoiceAccessKey,
		cfg.WakeWordModelPath,
		cfg.WakeWordSensitivity,
		v.mic,
	)

	transcriber, err := audio.NewTranscriber("")
	if err != nil {
		slog.Warn("transcriber unavailable, voice input disabled", "err", err)
	} else {
		v.transcriber = transcriber
	}

	// Try faster-whisper Python sidecar for much faster STT (~0.3s vs ~3s).
	// Falls back to whisper-cli if Python/faster-whisper not available.
	sttScript := findSTTScript()
	if sttScript != "" {
		ft := audio.NewFastTranscriber(sttScript, "small.en")
		if startErr := ft.Start(); startErr != nil {
			slog.Warn("fast STT server unavailable, using whisper-cli fallback", "err", startErr)
		} else {
			v.fastTranscriber = ft
		}
	}

	if cfg.ElevenLabsKey != "" {
		v.speaker = audio.NewElevenLabsSpeaker(cfg.ElevenLabsKey, cfg.ElevenLabsVoiceID)
		slog.Info("jarvis: using ElevenLabs voice")
	} else {
		v.speaker = audio.NewSpeaker(cfg.Voice)
	}

	// --- LLM & conversation ---
	if cfg.Provider != "" {
		v.chatClient = NewChatClientWithProvider(ChatProvider(cfg.Provider), cfg.APIKey)
	} else {
		v.chatClient = NewChatClient(cfg.APIKey) // auto-detects: CLI if available, else API
	}
	v.conversation = NewConversation(8000, 5*time.Minute)

	// --- Context, commands, analysis ---
	v.context = ctxProvider
	v.commands = NewCommandRouter(actionProvider)
	v.analyzer = NewOutputAnalyzer(getOutput)

	// --- Proactive session monitor ---
	v.monitor = NewSessionMonitor(ctxProvider, v.speaker, v.vad, emitFn, v.GetState)

	return v
}

// Start initialises the orchestrator's lifecycle context and launches the
// voice pipeline. If AmbientEnabled is true in config, Start automatically
// enters ambient mode (mic + wake word only — lightweight). Otherwise Jarvis
// operates in text-only mode (via SendMessage) until explicitly activated.
func (v *Jarvis) Start(ctx context.Context) error {
	v.mu.Lock()
	v.ctx, v.cancel = context.WithCancel(ctx)
	v.mu.Unlock()

	// Build a status summary for the start-up log.
	status := v.subsystemStatus()

	// Mute VAD before starting ambient so the auto-greet TTS doesn't
	// trigger the voice pipeline (which would cause multiple greetings).
	if v.vad != nil {
		v.vad.Mute()
	}

	if v.config.AmbientEnabled {
		if err := v.StartAmbient(ctx); err != nil {
			slog.Warn("ambient mode failed to start, falling back to text-only", "err", err)
			v.emitFn(JarvisEvent{
				Type:      "error",
				Text:      fmt.Sprintf("Voice mode unavailable: %s", err),
				Timestamp: time.Now(),
			})
		}
	}

	slog.Info("jarvis started", "ambient", v.config.AmbientEnabled, "subsystems", status)

	// Auto-greet: proactively speak a briefing on startup (Jarvis style).
	// Runs in a goroutine so Start() returns immediately. The goroutine
	// unmutes VAD after speaking so the voice pipeline activates only after
	// the greeting is done.
	go v.autoGreet()

	// Proactive session monitor: watches for session state changes and
	// speaks alerts when something completes, fails, or needs approval.
	go v.monitor.Start(v.ctx)

	return nil
}

// StartAmbient enters ambient listening mode: microphone capture + voice
// activity detection are started. When speech is detected, the full pipeline
// (STT → Claude → TTS) activates. No wake word needed — just talk.
//
// If a Picovoice access key is configured, uses Porcupine wake word detection
// instead of VAD (more precise, less CPU). Otherwise defaults to VAD.
func (v *Jarvis) StartAmbient(ctx context.Context) error {
	v.mu.Lock()
	// Ensure we have a lifecycle context. If Start() already set one we
	// keep it; if StartAmbient is called standalone we create one.
	if v.cancel == nil {
		v.ctx, v.cancel = context.WithCancel(ctx)
	}

	// Create a child context for the ambient goroutine so StopAmbient can
	// cancel detection without tearing down the whole orchestrator.
	ambientCtx, ambientCancel := context.WithCancel(v.ctx)
	v.ambientCancel = ambientCancel
	v.mu.Unlock()

	// Start mic capture. If this fails, ambient mode cannot operate.
	if err := v.mic.Start(); err != nil {
		ambientCancel()
		return fmt.Errorf("ambient: mic capture failed: %w", err)
	}

	// Choose detection mode: wake word (if Picovoice key set) or VAD (default).
	if v.config.PicovoiceAccessKey != "" {
		// Wake word mode — more precise, requires Picovoice account.
		slog.Info("jarvis ambient: using wake word detection (Porcupine)")
		go func() {
			if err := v.wakeWord.Start(ambientCtx, v.handleWakeWord); err != nil {
				slog.Warn("ambient: wake word detection failed, falling back to VAD", "err", err)
				v.startVADLoop(ambientCtx)
			}
		}()
	} else {
		// VAD mode — no accounts needed, just talks and Jarvis listens.
		slog.Info("jarvis ambient: using voice activity detection (no wake word)")
		v.startVADLoop(ambientCtx)
	}

	v.mu.Lock()
	v.ambient = true
	v.mu.Unlock()

	v.setState(JarvisIdle)
	slog.Info("jarvis ambient mode started")

	return nil
}

// startVADLoop launches the Voice Activity Detection loop in a goroutine.
// When speech is detected, it triggers the voice pipeline (same as wake word).
// When speech ends, the transcription is already captured by TranscribeFromMic
// in the voiceTurn flow.
func (v *Jarvis) startVADLoop(ctx context.Context) {
	go func() {
		err := v.vad.Listen(ctx,
			func() {
				// Speech started — trigger the voice pipeline.
				// Only activate if we're idle (not already in a conversation).
				if v.GetState() == JarvisIdle {
					v.handleWakeWord()
				}
			},
			nil, // speech end is handled by TranscribeFromMic's silence detection
		)
		if err != nil {
			slog.Warn("vad: listen loop ended", "err", err)
		}

		v.mu.Lock()
		v.ambient = false
		v.mu.Unlock()
	}()
}

// StopAmbient stops ambient listening by tearing down microphone capture
// and wake word detection. Other subsystems (STT, LLM, TTS) are left
// untouched so text-only mode continues to work.
func (v *Jarvis) StopAmbient() {
	v.mu.Lock()
	wasAmbient := v.ambient
	v.ambient = false
	ambientCancel := v.ambientCancel
	v.ambientCancel = nil
	v.mu.Unlock()

	if !wasAmbient {
		return
	}

	// Cancel the ambient context to stop the wake word detection goroutine.
	if ambientCancel != nil {
		ambientCancel()
	}

	if v.mic != nil {
		_ = v.mic.Stop()
	}

	slog.Info("jarvis ambient mode stopped")
}

// IsAmbient reports whether ambient listening mode is active. In ambient
// mode the wake word detector is running but Jarvis is not in an active
// conversation (state is JarvisIdle).
func (v *Jarvis) IsAmbient() bool {
	v.mu.RLock()
	defer v.mu.RUnlock()
	return v.ambient && v.state == JarvisIdle
}

// Stop tears down the orchestrator and resets state to idle.
// It is safe to call on a Jarvis instance that was never started.
func (v *Jarvis) Stop() {
	v.mu.Lock()
	defer v.mu.Unlock()

	if v.ambientCancel != nil {
		v.ambientCancel()
		v.ambientCancel = nil
	}

	if v.cancel != nil {
		v.cancel()
		v.cancel = nil
	}

	// Stop audio level emitter if running.
	if v.audioLevelStop != nil {
		close(v.audioLevelStop)
		v.audioLevelStop = nil
	}

	// Tear down subsystems in reverse order of start-up.
	if v.fastTranscriber != nil {
		v.fastTranscriber.Stop()
	}
	if v.speaker != nil {
		_ = v.speaker.Stop()
	}
	if v.mic != nil {
		_ = v.mic.Stop()
	}

	v.ambient = false
	v.state = JarvisIdle
	slog.Info("jarvis stopped")
}

// GetState returns the current JarvisState in a thread-safe manner.
func (v *Jarvis) GetState() JarvisState {
	v.mu.RLock()
	defer v.mu.RUnlock()
	return v.state
}

// setState updates the state and emits a state_change event to the frontend.
// It also manages the audio level emitter: starting it when entering
// JarvisSpeaking and stopping it when leaving JarvisSpeaking.
func (v *Jarvis) setState(s JarvisState) {
	v.mu.Lock()
	prev := v.state
	v.state = s
	v.mu.Unlock()

	// Manage audio level emitter based on state transitions.
	if s == JarvisSpeaking && prev != JarvisSpeaking {
		v.startAudioLevelEmitter()
	} else if s != JarvisSpeaking && prev == JarvisSpeaking {
		v.stopAudioLevelEmitter()
	}

	v.emitFn(JarvisEvent{
		Type:      "state_change",
		State:     s,
		Timestamp: time.Now(),
	})
}

// GetHistory returns the conversation history from the underlying Conversation.
func (v *Jarvis) GetHistory() []Message {
	return v.conversation.GetMessages()
}

// SendMessage accepts user text and returns a response from Jarvis.
// This is the text-only fallback that bypasses audio input but still
// speaks the response via TTS.
//
// When the API provider is active, uses streaming so the first sentence
// plays while the rest generates. Falls back to batch mode for CLI.
func (v *Jarvis) SendMessage(text string) (string, error) {
	if text == "" {
		return "", fmt.Errorf("empty message")
	}

	v.setState(JarvisThinking)

	// Streaming path: API provider streams sentences to TTS as they arrive.
	if v.isAPIProvider() {
		responseText, err := v.processUserTextStreaming(text)
		if err != nil {
			v.setState(JarvisIdle)
			return "", err
		}
		// processUserTextStreaming already handled TTS and emitted events.
		v.setState(JarvisIdle)
		return responseText, nil
	}

	// Batch path: CLI provider gets the full response then speaks it.
	responseText, err := v.processUserText(text)
	if err != nil {
		v.setState(JarvisIdle)
		return "", err
	}

	// Mute VAD before speaking so Jarvis doesn't hear itself.
	if v.vad != nil {
		v.vad.Mute()
	}

	// Speak the response asynchronously; do not block the caller.
	v.setState(JarvisSpeaking)
	done := v.speaker.SpeakAsync(responseText)

	// Emit the message event.
	v.emitFn(JarvisEvent{
		Type:      "message",
		State:     JarvisSpeaking,
		Text:      responseText,
		Role:      "jarvis",
		Timestamp: time.Now(),
	})

	// Wait for TTS to complete, then unmute and return to idle.
	go func() {
		<-done
		if v.vad != nil {
			v.vad.Unmute()
		}
		v.setState(JarvisIdle)
	}()

	return responseText, nil
}

// ---------------------------------------------------------------------------
// Voice pipeline
// ---------------------------------------------------------------------------

// handleWakeWord is the core voice loop callback invoked when the wake word
// is detected. It orchestrates: listen -> transcribe -> think -> speak -> idle,
// with multi-turn follow-up support and a 30-second per-step timeout safety net.
func (v *Jarvis) handleWakeWord() {
	v.voiceTurn()
}

// voiceTurn executes a single listen->think->speak cycle. It is called both
// from the wake-word callback (initial turn) and could be reused for follow-up
// turns. The follow-up path currently lives in waitForFollowUp for tighter
// timeout control.
func (v *Jarvis) voiceTurn() {
	// Guard: only proceed if we have a transcriber.
	if v.transcriber == nil {
		slog.Warn("voice turn skipped: transcriber unavailable")
		v.speakError("Sorry, speech recognition is not available right now.")
		return
	}

	// --- Listen ---
	v.setState(JarvisListening)

	// Stop any speech in progress so the mic hears the user, not echo.
	if err := v.speaker.Stop(); err != nil {
		slog.Warn("failed to stop speaker before listening", "err", err)
	}

	userText, err := v.transcribeWithTimeout()
	if err != nil {
		slog.Error("transcription failed", "err", err)
		v.speakError("Sorry, I couldn't hear you clearly.")
		return
	}

	userText = strings.TrimSpace(userText)
	if userText == "" {
		slog.Info("empty transcription, returning to idle")
		v.setState(JarvisIdle)
		return
	}

	slog.Info("user said", "text", userText)

	// No wake word required — Jarvis is always attentive, like Jarvis.
	// Optionally strip "jarvis" / "hey jarvis" prefix if present.
	lower := strings.ToLower(userText)
	for _, prefix := range []string{
		"hey jarvis ", "hey jarvis, ", "jarvis ", "jarvis, ",
		"ok jarvis ", "ok jarvis, ",
	} {
		if strings.HasPrefix(lower, prefix) {
			userText = strings.TrimSpace(userText[len(prefix):])
			break
		}
	}
	if strings.TrimSpace(userText) == "" {
		userText = "hello"
	}

	// Emit the user message event.
	v.emitFn(JarvisEvent{
		Type:      "message",
		State:     JarvisListening,
		Text:      userText,
		Role:      "user",
		Timestamp: time.Now(),
	})

	// Check for dismiss phrases that end the conversation.
	if isDismissPhrase(userText) {
		v.setState(JarvisSpeaking)
		if v.vad != nil {
			v.vad.Mute()
		}
		_ = v.speaker.Speak("Anytime.")
		if v.vad != nil {
			v.vad.Unmute()
		}
		v.setState(JarvisIdle)
		return
	}

	// --- Think + Speak ---
	v.setState(JarvisThinking)

	// Mute VAD before speaking so Jarvis doesn't hear itself.
	if v.vad != nil {
		v.vad.Mute()
	}

	// Streaming path: API provider streams sentences to TTS as they arrive.
	// processUserTextStreaming handles setState(JarvisSpeaking) internally on
	// first sentence, and emits message_chunk + message events.
	if v.isAPIProvider() {
		responseText, err := v.processUserTextStreaming(userText)
		if err != nil {
			if v.vad != nil {
				v.vad.Unmute()
			}
			slog.Error("LLM streaming failed", "err", err)
			v.speakError("Sorry, I couldn't process that.")
			return
		}
		_ = responseText // already spoken and emitted by streaming pipeline

		if v.vad != nil {
			v.vad.Unmute()
		}
	} else {
		// Batch path: CLI provider gets the full response then speaks it.
		responseText, err := v.processUserTextWithTimeout(userText)
		if err != nil {
			if v.vad != nil {
				v.vad.Unmute()
			}
			slog.Error("LLM processing failed", "err", err)
			v.speakError("Sorry, I couldn't process that.")
			return
		}

		v.setState(JarvisSpeaking)

		v.emitFn(JarvisEvent{
			Type:      "message",
			State:     JarvisSpeaking,
			Text:      responseText,
			Role:      "jarvis",
			Timestamp: time.Now(),
		})

		if err := v.speaker.Speak(responseText); err != nil {
			slog.Error("TTS failed", "err", err)
		}

		if v.vad != nil {
			v.vad.Unmute()
		}
	}

	// --- Multi-turn follow-up ---
	v.setState(JarvisIdle)
	v.waitForFollowUp()
}

// transcribeWithTimeout wraps TranscribeFromMic with the step timeout.
// Prefers the fast Python transcriber if available.
func (v *Jarvis) transcribeWithTimeout() (string, error) {
	type result struct {
		text string
		err  error
	}
	ch := make(chan result, 1)

	go func() {
		if v.fastTranscriber != nil && v.fastTranscriber.IsRunning() {
			text, err := v.fastTranscriber.TranscribeFromMic(v.mic, 30)
			ch <- result{text, err}
		} else if v.transcriber != nil {
			text, err := v.transcriber.TranscribeFromMic(v.mic, 30)
			ch <- result{text, err}
		} else {
			ch <- result{"", fmt.Errorf("no transcriber available")}
		}
	}()

	select {
	case r := <-ch:
		return r.text, r.err
	case <-time.After(stepTimeout):
		return "", fmt.Errorf("transcription timed out after %s", stepTimeout)
	case <-v.ctx.Done():
		return "", v.ctx.Err()
	}
}

// processUserText runs the LLM pipeline for the given user text: refresh
// context if expired, build the system prompt, call Claude, handle any
// action commands, and return the spoken response text.
func (v *Jarvis) processUserText(userText string) (string, error) {
	// Refresh context if the conversation has expired (stale window).
	if v.conversation.IsExpired() {
		slog.Info("conversation expired, injecting fresh context")
		v.conversation.Reset()
	}

	// Add the user message to the conversation.
	v.conversation.AddMessage("user", userText)

	// Build system prompt with live context.
	envContext := ""
	if v.context != nil {
		envContext = AssembleContext(v.context)
	}
	systemPrompt := BuildSystemPrompt(envContext, v.config.Verbosity)

	// Call Claude with token-truncated history to stay within context window.
	messages := v.conversation.GetMessagesForLLM()
	resp, err := v.chatClient.Chat(v.ctx, systemPrompt, messages)
	if err != nil {
		return "", fmt.Errorf("chat failed: %w", err)
	}

	// Record the assistant's response.
	v.conversation.AddMessage("assistant", resp.Text)

	slog.Info("claude response", "text", resp.Text, "has_action", resp.Action != nil, "raw_len", len(resp.RawText))

	// Execute any embedded action command.
	if resp.Action != nil {
		confirmation, cmdErr := v.commands.Execute(resp.Action)
		if cmdErr != nil {
			slog.Error("command execution failed",
				"action", resp.Action.Action,
				"err", cmdErr,
			)
		} else {
			slog.Info("command executed",
				"action", resp.Action.Action,
				"confirmation", confirmation,
			)
		}

		// Emit a navigate event so the frontend switches views.
		if resp.Action.Action == ActionNavigateView {
			v.emitFn(JarvisEvent{
				Type:      "navigate",
				Text:      resp.Action.Command,
				Timestamp: time.Now(),
			})
		}
	}

	return resp.Text, nil
}

// processUserTextStreaming runs the LLM pipeline with streaming: sentences are
// piped to TTS as they arrive so the first sentence plays while the rest
// generates. Only works with API provider — the caller must check before
// calling. Handles its own setState transitions (thinking -> speaking -> idle
// is NOT done here — caller manages the final idle transition).
func (v *Jarvis) processUserTextStreaming(userText string) (string, error) {
	// Refresh context if the conversation has expired (stale window).
	if v.conversation.IsExpired() {
		slog.Info("conversation expired, injecting fresh context")
		v.conversation.Reset()
	}

	// Add the user message to the conversation.
	v.conversation.AddMessage("user", userText)

	// Build system prompt with live context.
	envContext := ""
	if v.context != nil {
		envContext = AssembleContext(v.context)
	}
	systemPrompt := BuildSystemPrompt(envContext, v.config.Verbosity)

	// Create the sentence channel and start TTS streaming.
	sentences := make(chan string, 8)
	ttsDone := v.speaker.SpeakStream(sentences)

	// Track whether we've transitioned to speaking state yet.
	speakingStarted := false

	// Stream from Claude with token-truncated history, piping sentences to TTS.
	messages := v.conversation.GetMessagesForLLM()
	resp, err := v.chatClient.ChatStream(v.ctx, systemPrompt, messages, func(sentence string) {
		// Transition to speaking on the first sentence.
		if !speakingStarted {
			speakingStarted = true
			v.setState(JarvisSpeaking)
		}

		// Emit chunk event so frontend can show progressive text.
		v.emitFn(JarvisEvent{
			Type:      "message_chunk",
			State:     JarvisSpeaking,
			Text:      sentence,
			Role:      "jarvis",
			Timestamp: time.Now(),
		})

		sentences <- sentence
	})

	// Close the sentence channel so SpeakStream knows no more sentences.
	close(sentences)

	if err != nil {
		// Wait for any already-queued sentences to finish speaking.
		<-ttsDone
		return "", fmt.Errorf("processUserTextStreaming: %w", err)
	}

	// Wait for TTS to finish speaking all sentences.
	<-ttsDone

	// Record the assistant's response.
	v.conversation.AddMessage("assistant", resp.Text)

	slog.Info("claude response (streamed)", "text", resp.Text, "has_action", resp.Action != nil, "raw_len", len(resp.RawText))

	// Emit the full message event.
	v.emitFn(JarvisEvent{
		Type:      "message",
		State:     JarvisSpeaking,
		Text:      resp.Text,
		Role:      "jarvis",
		Timestamp: time.Now(),
	})

	// Execute any embedded action command.
	if resp.Action != nil {
		confirmation, cmdErr := v.commands.Execute(resp.Action)
		if cmdErr != nil {
			slog.Error("command execution failed",
				"action", resp.Action.Action,
				"err", cmdErr,
			)
		} else {
			slog.Info("command executed",
				"action", resp.Action.Action,
				"confirmation", confirmation,
			)
		}

		// Emit a navigate event so the frontend switches views.
		if resp.Action.Action == ActionNavigateView {
			v.emitFn(JarvisEvent{
				Type:      "navigate",
				Text:      resp.Action.Command,
				Timestamp: time.Now(),
			})
		}
	}

	return resp.Text, nil
}

// isAPIProvider returns true when the chat client is configured to use the
// Anthropic Messages API (as opposed to the CLI). Used to decide whether
// streaming is available.
func (v *Jarvis) isAPIProvider() bool {
	return v.chatClient != nil && v.chatClient.provider == ChatProviderAPI
}

// processUserTextWithTimeout wraps processUserText with the step timeout.
func (v *Jarvis) processUserTextWithTimeout(userText string) (string, error) {
	type result struct {
		text string
		err  error
	}
	ch := make(chan result, 1)

	go func() {
		text, err := v.processUserText(userText)
		ch <- result{text, err}
	}()

	select {
	case r := <-ch:
		return r.text, r.err
	case <-time.After(stepTimeout):
		return "", fmt.Errorf("LLM processing timed out after %s", stepTimeout)
	case <-v.ctx.Done():
		return "", v.ctx.Err()
	}
}

// waitForFollowUp listens for a follow-up utterance within followUpTimeout.
// If speech is detected, it processes another voice turn without requiring
// the wake word. If the timeout expires with no speech, it returns silently
// and the wake word detector resumes normal operation.
func (v *Jarvis) waitForFollowUp() {
	if v.transcriber == nil {
		return
	}

	// Drain any lingering audio from the previous TTS playback. Without this,
	// residual echo in the mic buffer can be transcribed as a false follow-up.
	v.mic.Drain()

	// Read a short chunk of audio to detect speech within the follow-up window.
	type result struct {
		text string
		err  error
	}
	ch := make(chan result, 1)

	go func() {
		// Use a short max duration so the transcriber returns quickly on silence.
		text, err := v.transcriber.TranscribeFromMic(v.mic, 5)
		ch <- result{text, err}
	}()

	select {
	case r := <-ch:
		if r.err != nil {
			slog.Debug("follow-up listen failed", "err", r.err)
			return
		}
		text := strings.TrimSpace(r.text)
		if text == "" {
			slog.Debug("no follow-up detected, returning to wake-word mode")
			return
		}
		// Check for dismiss phrases.
		if isDismissPhrase(text) {
			v.setState(JarvisSpeaking)
			if v.vad != nil {
				v.vad.Mute()
			}
			_ = v.speaker.Speak("Anytime.")
			if v.vad != nil {
				v.vad.Unmute()
			}
			v.setState(JarvisIdle)
			return
		}
		slog.Info("follow-up detected", "text", text)
		v.emitFn(JarvisEvent{
			Type:      "message",
			State:     JarvisListening,
			Text:      text,
			Role:      "user",
			Timestamp: time.Now(),
		})
		// Process follow-up as a new turn (without wake word).
		v.setState(JarvisThinking)
		responseText, err := v.processUserTextWithTimeout(text)
		if err != nil {
			slog.Error("follow-up LLM processing failed", "err", err)
			v.speakError("Sorry, I couldn't process that.")
			return
		}
		v.setState(JarvisSpeaking)
		v.emitFn(JarvisEvent{
			Type:      "message",
			State:     JarvisSpeaking,
			Text:      responseText,
			Role:      "jarvis",
			Timestamp: time.Now(),
		})
		if v.vad != nil {
			v.vad.Mute()
		}
		if err := v.speaker.Speak(responseText); err != nil {
			slog.Error("follow-up TTS failed", "err", err)
		}
		if v.vad != nil {
			v.vad.Unmute()
		}
		v.setState(JarvisIdle)
		// Recurse for another follow-up window.
		v.waitForFollowUp()

	case <-time.After(followUpTimeout):
		slog.Debug("follow-up timeout, returning to wake-word mode")
		return

	case <-v.ctx.Done():
		return
	}
}

// ---------------------------------------------------------------------------
// Audio level emitter
// ---------------------------------------------------------------------------

// startAudioLevelEmitter begins emitting audio_level events at ~15fps with a
// sine-wave-based level that varies between 0.3 and 0.9. This simulates speech
// rhythm since we cannot easily read actual audio amplitude from the TTS
// process. Safe to call multiple times; subsequent calls are no-ops if already
// running.
func (v *Jarvis) startAudioLevelEmitter() {
	v.mu.Lock()
	if v.audioLevelStop != nil {
		v.mu.Unlock()
		return // already running
	}
	stop := make(chan struct{})
	v.audioLevelStop = stop
	v.mu.Unlock()

	go func() {
		ticker := time.NewTicker(66 * time.Millisecond) // ~15fps
		defer ticker.Stop()

		start := time.Now()
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				elapsed := time.Since(start).Seconds()
				// Primary wave (speech cadence ~3Hz) modulated by a slower
				// envelope (~0.7Hz) for natural variation.
				primary := math.Sin(elapsed * 3.0 * 2.0 * math.Pi)
				envelope := 0.7 + 0.3*math.Sin(elapsed*0.7*2.0*math.Pi)
				// Map to 0.3-0.9 range.
				level := 0.3 + 0.6*((primary*envelope+1.0)/2.0)
				// Clamp to [0, 1].
				if level > 1.0 {
					level = 1.0
				}
				if level < 0.0 {
					level = 0.0
				}

				v.emitFn(JarvisEvent{
					Type:      "audio_level",
					State:     JarvisSpeaking,
					Level:     level,
					Timestamp: time.Now(),
				})
			}
		}
	}()
}

// stopAudioLevelEmitter stops the audio level emitter goroutine if running,
// and emits a final level-0 event so the frontend can smoothly decay.
func (v *Jarvis) stopAudioLevelEmitter() {
	v.mu.Lock()
	stop := v.audioLevelStop
	v.audioLevelStop = nil
	v.mu.Unlock()

	if stop != nil {
		close(stop)
		// Emit a final zero-level so the frontend begins its decay immediately.
		v.emitFn(JarvisEvent{
			Type:      "audio_level",
			State:     JarvisIdle,
			Level:     0,
			Timestamp: time.Now(),
		})
	}
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// speakError speaks an error message to the user and returns to idle state.
func (v *Jarvis) speakError(msg string) {
	v.setState(JarvisSpeaking)
	v.emitFn(JarvisEvent{
		Type:      "error",
		State:     JarvisSpeaking,
		Text:      msg,
		Role:      "jarvis",
		Timestamp: time.Now(),
	})
	if v.vad != nil {
		v.vad.Mute()
	}
	_ = v.speaker.Speak(msg)
	if v.vad != nil {
		v.vad.Unmute()
	}
	v.setState(JarvisIdle)
}

// isDismissPhrase returns true if the user text matches one of the known
// phrases that signal the end of a multi-turn conversation.
func isDismissPhrase(text string) bool {
	lower := strings.ToLower(strings.TrimSpace(text))
	for _, phrase := range dismissPhrases {
		if lower == phrase {
			return true
		}
	}
	return false
}

// subsystemStatus returns a human-readable summary of which subsystems
// are available.
// autoGreet assembles context and speaks a proactive briefing on startup.
// This gives the Jarvis experience: the app opens and Jarvis greets you.
// VAD is already muted by Start() before this runs. This method unmutes
// VAD when done so the voice pipeline activates only after the greeting.
func (v *Jarvis) autoGreet() {
	// Brief delay to let the app window render first.
	time.Sleep(500 * time.Millisecond)

	// Always unmute VAD when done, even if we bail early.
	defer func() {
		if v.vad != nil {
			v.vad.Unmute()
		}
	}()

	if v.chatClient == nil {
		return
	}

	slog.Info("jarvis: auto-greeting")

	// Assemble fresh context.
	contextText := ""
	if v.context != nil {
		contextText = AssembleContext(v.context)
	}

	systemPrompt := BuildSystemPrompt(contextText, v.config.Verbosity)

	// Send a greeting prompt to Claude. Instruct it to greet first, then brief.
	v.conversation.AddMessage("user", "I just opened the app. Say hello naturally first, then give a very brief status update in one sentence.")

	resp, err := v.chatClient.Chat(v.ctx, systemPrompt, v.conversation.GetMessagesForLLM())
	if err != nil {
		slog.Warn("auto-greet failed", "err", err)
		return
	}

	v.conversation.AddMessage("assistant", resp.Text)

	// Emit the greeting event.
	v.emitFn(JarvisEvent{
		Type:      "message",
		State:     JarvisSpeaking,
		Text:      resp.Text,
		Role:      "jarvis",
		Timestamp: time.Now(),
	})

	// Speak it. VAD is already muted by Start().
	v.setState(JarvisSpeaking)
	if err := v.speaker.Speak(resp.Text); err != nil {
		slog.Error("auto-greet TTS failed", "err", err)
	}
	v.setState(JarvisIdle)

	// Reset conversation after greeting so the greeting exchange doesn't
	// pollute subsequent questions.
	v.conversation.Reset()
}

func (v *Jarvis) subsystemStatus() string {
	parts := make([]string, 0, 5)

	if v.mic != nil {
		parts = append(parts, "mic=ok")
	} else {
		parts = append(parts, "mic=unavailable")
	}

	if v.fastTranscriber != nil && v.fastTranscriber.IsRunning() {
		parts = append(parts, "stt=fast-whisper")
	} else if v.transcriber != nil {
		parts = append(parts, "stt=whisper-cli")
	} else {
		parts = append(parts, "stt=unavailable")
	}

	parts = append(parts, "tts=ok") // Speaker always initialises.

	if v.chatClient != nil && v.config.APIKey != "" {
		parts = append(parts, "llm=ok")
	} else {
		parts = append(parts, "llm=no-api-key")
	}

	if v.config.PicovoiceAccessKey != "" {
		parts = append(parts, "wakeword=ok")
	} else {
		parts = append(parts, "wakeword=no-access-key")
	}

	return strings.Join(parts, ", ")
}

// findSTTScript looks for the fast STT Python script relative to the
// executable or common locations.
func findSTTScript() string {
	// Check common locations relative to the project.
	candidates := []string{
		"scripts/jarvis-stt-server.py",
		"../scripts/jarvis-stt-server.py",
	}

	// Also check relative to the executable path.
	if exe, err := os.Executable(); err == nil {
		dir := filepath.Dir(exe)
		candidates = append(candidates,
			filepath.Join(dir, "scripts", "jarvis-stt-server.py"),
			filepath.Join(dir, "..", "scripts", "jarvis-stt-server.py"),
		)
	}

	// Check Jarvis data dir.
	candidates = append(candidates,
		paths.DataPath("jarvis-stt-server.py"),
	)

	for _, p := range candidates {
		if _, err := os.Stat(p); err == nil {
			slog.Info("found fast STT script", "path", p)
			return p
		}
	}

	slog.Info("fast STT script not found, will use whisper-cli")
	return ""
}
