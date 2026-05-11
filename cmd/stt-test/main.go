// Quick STT diagnostic — checks if whisper-cli is findable and working.
// Run: go run ./cmd/stt-test/
package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/namanchopra/jarvis/internal/paths"
)

func main() {
	// Check PATH
	names := []string{"whisper-cli", "whisper-cpp", "main"}
	for _, name := range names {
		if p, err := exec.LookPath(name); err == nil {
			fmt.Printf("Found in PATH: %s -> %s\n", name, p)
		}
	}

	// Check well-known paths
	wellKnownPaths := []string{
		"/opt/homebrew/bin/whisper-cli",
		"/opt/homebrew/bin/whisper-cpp",
		"/usr/local/bin/whisper-cli",
		"/usr/local/bin/whisper-cpp",
	}
	for _, p := range wellKnownPaths {
		if _, err := os.Stat(p); err == nil {
			fmt.Printf("Found at: %s\n", p)
		}
	}

	// Check model — prefer bundled .app models, fall back to ~/.jarvis/models.
	// (Phase 2 TASK-015 migrated this away from the legacy ~/.awm path.)
	modelsDir := paths.BundledModelsDir()
	if modelsDir == "" {
		modelsDir = paths.ModelsDir()
	}
	modelPath := filepath.Join(modelsDir, "ggml-base.en.bin")
	if info, err := os.Stat(modelPath); err == nil {
		fmt.Printf("Model found: %s (%d MB)\n", modelPath, info.Size()/1024/1024)
	} else {
		fmt.Printf("Model MISSING: %s\n", modelPath)
	}

	// Try a quick transcription of silence
	whisperPath := "/opt/homebrew/bin/whisper-cli"
	if _, err := os.Stat(whisperPath); err != nil {
		fmt.Println("\nwhisper-cli not at /opt/homebrew/bin/whisper-cli")
		return
	}

	fmt.Printf("\nTesting whisper-cli with model...\n")
	cmd := exec.Command(whisperPath, "-m", modelPath, "--help")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		fmt.Printf("whisper-cli failed: %v\n", err)
	} else {
		fmt.Println("whisper-cli is working!")
	}
}
