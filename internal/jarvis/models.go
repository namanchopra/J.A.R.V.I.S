// Package jarvis implements the Jarvis voice companion feature, providing wake
// word detection, speech-to-text transcription, and conversational AI via
// Claude. This file handles model path resolution and existence checks.
package jarvis

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/namanchopra/jarvis/internal/paths"
)

const (
	// whisperModel is the filename of the Whisper GGML model used for
	// speech-to-text transcription.
	whisperModel = "ggml-base.en.bin"

	// whisperModelURL is the canonical download location for the model file.
	whisperModelURL = "https://huggingface.co/ggerganov/whisper.cpp/resolve/main/ggml-base.en.bin"
)

// ModelsDir returns the directory where Jarvis looks up model files.
//
// Resolution order:
//  1. Bundled models inside a production .app
//     ("<.app>/Contents/Resources/models") via paths.BundledModelsDir().
//  2. User-writable dev/runtime location (~/.jarvis/models/) via
//     paths.ModelsDir(). When the user's home directory cannot be resolved,
//     paths.ModelsDir falls back to "./.jarvis/models" so the app keeps
//     running.
func ModelsDir() string {
	if bundled := paths.BundledModelsDir(); bundled != "" {
		return bundled
	}
	return paths.ModelsDir()
}

// WhisperModelPath returns the full filesystem path to the Whisper GGML model
// file (~/.jarvis/models/ggml-base.en.bin).
func WhisperModelPath() string {
	return filepath.Join(ModelsDir(), whisperModel)
}

// EnsureModels verifies that all required model files are present on disk.
// It returns nil when every model exists, or a descriptive error with download
// instructions when one or more models are missing.
func EnsureModels() error {
	modelPath := WhisperModelPath()

	if _, err := os.Stat(modelPath); err == nil {
		slog.Debug("whisper model found", "path", modelPath)
		return nil
	}

	return fmt.Errorf(
		"whisper model not found at %s\n\n"+
			"Run the setup script to download it:\n"+
			"  ./scripts/setup-jarvis.sh\n\n"+
			"Or download manually:\n"+
			"  mkdir -p %s\n"+
			"  curl -L -o %s \\\n"+
			"    %s",
		modelPath, ModelsDir(), modelPath, whisperModelURL,
	)
}
