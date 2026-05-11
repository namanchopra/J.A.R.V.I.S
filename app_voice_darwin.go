//go:build darwin

// app_voice_darwin.go — macOS Core Audio mic enumeration via system_profiler.
//
// system_profiler SPAudioDataType -json -detailLevel mini emits a stable
// JSON structure that lists every Core Audio device the OS knows about,
// including each device's input-channel count and a flag marking the
// current system default input. We shell out (no cgo) and walk the JSON.
//
// Schema (truncated example):
//
//   {
//     "SPAudioDataType": [
//       {
//         "_items": [
//           {
//             "_name": "MacBook Pro Microphone",
//             "coreaudio_device_input": 1,
//             "coreaudio_default_audio_input_device": "spaudio_yes",
//             ...
//           },
//           {
//             "_name": "MacBook Pro Speakers",
//             "coreaudio_device_output": 2,   // output-only, skip
//             ...
//           }
//         ]
//       }
//     ]
//   }
//
// We filter to entries where coreaudio_device_input > 0 (a JSON number) and
// tag the one with coreaudio_default_audio_input_device == "spaudio_yes" as
// the default. The device ID exposed to the frontend is the human-readable
// `_name` (Core Audio AudioObjectID values are not stable across reboots
// without cgo bindings, and the daemon's audio pipeline already accepts the
// device name string).
package main

import (
	"encoding/json"
	"os/exec"
	"strings"
	"time"
)

// audioProfileDoc mirrors the relevant subset of system_profiler's output.
// We use json.RawMessage / map[string]json.RawMessage so unknown keys don't
// fail unmarshaling on future macOS releases that add new fields.
type audioProfileDoc struct {
	SPAudioDataType []struct {
		Items []map[string]json.RawMessage `json:"_items"`
	} `json:"SPAudioDataType"`
}

// enumerateAudioInputs runs `system_profiler SPAudioDataType -json` and
// returns the input devices it finds. Returns nil on any failure — the
// caller (the Wails binding) treats nil as "fall back to Default in the UI".
func enumerateAudioInputs() []AudioDevice {
	// 4-second cap — system_profiler typically returns in under 200ms but
	// can stall briefly on first invocation. We never want Settings to
	// block longer than that on a UI dropdown.
	cmd := exec.Command("system_profiler", "SPAudioDataType", "-json", "-detailLevel", "mini")
	out, err := runWithTimeout(cmd, 4*time.Second)
	if err != nil {
		return nil
	}

	var doc audioProfileDoc
	if err := json.Unmarshal(out, &doc); err != nil {
		return nil
	}

	devices := make([]AudioDevice, 0, 4)
	seen := make(map[string]bool)

	for _, group := range doc.SPAudioDataType {
		for _, item := range group.Items {
			// An input device has a numeric coreaudio_device_input >= 1.
			inputRaw, ok := item["coreaudio_device_input"]
			if !ok {
				continue
			}
			var inputChans int
			if err := json.Unmarshal(inputRaw, &inputChans); err != nil || inputChans < 1 {
				continue
			}

			// `_name` is the user-facing label and our stable ID.
			nameRaw, ok := item["_name"]
			if !ok {
				continue
			}
			var name string
			if err := json.Unmarshal(nameRaw, &name); err != nil {
				continue
			}
			name = strings.TrimSpace(name)
			if name == "" || seen[name] {
				continue
			}
			seen[name] = true

			// Default input marker.
			isDefault := false
			if defRaw, ok := item["coreaudio_default_audio_input_device"]; ok {
				var s string
				if err := json.Unmarshal(defRaw, &s); err == nil && s == "spaudio_yes" {
					isDefault = true
				}
			}

			devices = append(devices, AudioDevice{
				ID:        name,
				Name:      name,
				IsDefault: isDefault,
			})
		}
	}

	if len(devices) == 0 {
		return nil
	}

	// If system_profiler didn't mark any device as default (rare on locked-down
	// systems), promote the first input to default so the UI still has a
	// sensible initial selection.
	hasDefault := false
	for _, d := range devices {
		if d.IsDefault {
			hasDefault = true
			break
		}
	}
	if !hasDefault {
		devices[0].IsDefault = true
	}

	return devices
}

// runWithTimeout runs cmd and returns its stdout, killing the process if it
// has not exited within the supplied timeout. Kept private and stateless so
// it can be unit-tested in isolation if needed.
func runWithTimeout(cmd *exec.Cmd, timeout time.Duration) ([]byte, error) {
	type result struct {
		out []byte
		err error
	}
	done := make(chan result, 1)

	go func() {
		out, err := cmd.Output()
		done <- result{out, err}
	}()

	select {
	case r := <-done:
		return r.out, r.err
	case <-time.After(timeout):
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		// Drain the goroutine so it doesn't leak.
		<-done
		return nil, exec.ErrNotFound
	}
}
