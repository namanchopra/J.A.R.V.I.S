//go:build darwin

// Quick mic test — records 3 seconds and prints RMS energy levels.
// Run: go run ./cmd/mic-test/
// If you see energy values > 0.01 when you talk, the mic works.
// darwin-only: wraps the PortAudio CGO binding, which the Go side only
// uses on macOS (see internal/jarvis/audio/capture_darwin.go).
package main

import (
	"fmt"
	"math"
	"os"
	"time"

	"github.com/gordonklaus/portaudio"
)

func main() {
	if err := portaudio.Initialize(); err != nil {
		fmt.Fprintf(os.Stderr, "PortAudio init failed: %v\n", err)
		os.Exit(1)
	}
	defer portaudio.Terminate()

	buf := make([]int16, 512)
	stream, err := portaudio.OpenDefaultStream(1, 0, 16000, len(buf), &buf)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Open stream failed: %v\n", err)
		fmt.Fprintf(os.Stderr, "If 'mic permission denied', grant access in System Settings > Privacy > Microphone\n")
		os.Exit(1)
	}
	defer stream.Close()

	if err := stream.Start(); err != nil {
		fmt.Fprintf(os.Stderr, "Start stream failed: %v\n", err)
		os.Exit(1)
	}
	defer stream.Stop()

	fmt.Println("Listening for 5 seconds... speak into your mic!")
	fmt.Println("You should see energy values jump when you talk.")
	fmt.Println()

	deadline := time.Now().Add(5 * time.Second)
	maxEnergy := 0.0

	for time.Now().Before(deadline) {
		if err := stream.Read(); err != nil {
			fmt.Fprintf(os.Stderr, "Read error: %v\n", err)
			continue
		}

		// Compute RMS energy
		var sum float64
		for _, s := range buf {
			f := float64(s) / 32768.0
			sum += f * f
		}
		rms := math.Sqrt(sum / float64(len(buf)))
		if rms > maxEnergy {
			maxEnergy = rms
		}

		// Visual bar
		bars := int(rms * 200)
		if bars > 50 {
			bars = 50
		}
		bar := ""
		for i := 0; i < bars; i++ {
			bar += "#"
		}

		fmt.Printf("\rEnergy: %.4f [%-50s]", rms, bar)
	}

	fmt.Printf("\n\nMax energy: %.4f\n", maxEnergy)
	fmt.Printf("VAD threshold: 0.015\n")
	if maxEnergy < 0.015 {
		fmt.Println("WARNING: Your voice didn't reach the VAD threshold.")
		fmt.Println("Either the mic isn't picking you up, or the threshold is too high.")
		fmt.Println("Try speaking louder or closer to the mic.")
	} else {
		fmt.Println("OK: Mic is capturing your voice above the VAD threshold.")
	}
}
