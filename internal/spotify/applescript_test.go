package spotify

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

// recorder captures every osascript script the driver would have issued
// and returns scripted responses to the caller.
type recorder struct {
	scripts []string
	out     string
	err     error
}

func (r *recorder) fn(script string) (string, error) {
	r.scripts = append(r.scripts, script)
	return r.out, r.err
}

func newDriver(r *recorder) *AppleScript {
	return &AppleScript{osascript: r.fn}
}

func TestPlay_IssuesValidAppleScript(t *testing.T) {
	r := &recorder{}
	a := newDriver(r)

	if err := a.Play("spotify:track:0VjIjW4GlUZAMYd2vXMi3b"); err != nil {
		t.Fatalf("Play returned error: %v", err)
	}
	if len(r.scripts) != 1 {
		t.Fatalf("expected exactly 1 script invocation, got %d", len(r.scripts))
	}
	got := r.scripts[0]
	wantSubstrings := []string{
		`tell application "Spotify"`,
		`play track`,
		`spotify:track:0VjIjW4GlUZAMYd2vXMi3b`,
	}
	for _, want := range wantSubstrings {
		if !strings.Contains(got, want) {
			t.Errorf("Play script missing %q in %q", want, got)
		}
	}
}

func TestPlay_RejectsEmptyURI(t *testing.T) {
	r := &recorder{}
	a := newDriver(r)

	err := a.Play("   ")
	if !errors.Is(err, ErrInvalidArg) {
		t.Fatalf("Play(empty) want ErrInvalidArg, got %v", err)
	}
	if len(r.scripts) != 0 {
		t.Errorf("Play(empty) must not invoke osascript, got %d invocations", len(r.scripts))
	}
}

func TestPlay_RejectsInjectionAttempts(t *testing.T) {
	r := &recorder{}
	a := newDriver(r)

	err := a.Play(`spotify:track:abc" & say "pwn`)
	if !errors.Is(err, ErrInvalidArg) {
		t.Fatalf("Play(injection) want ErrInvalidArg, got %v", err)
	}
	if len(r.scripts) != 0 {
		t.Errorf("Play(injection) must not invoke osascript, got %d invocations", len(r.scripts))
	}
}

func TestPlay_BubblesUpInvalidTrackError(t *testing.T) {
	r := &recorder{
		out: `0:18: execution error: Spotify got an error: Can't play track. (-2700)`,
		err: errors.New("exit status 1"),
	}
	a := newDriver(r)

	err := a.Play("spotify:track:invalid")
	if err == nil {
		t.Fatal("Play(invalid) expected error, got nil")
	}
	if !strings.Contains(err.Error(), "Play:") {
		t.Errorf("Play error not wrapped with method name: %v", err)
	}
}

func TestSimpleControls(t *testing.T) {
	cases := []struct {
		name    string
		fn      func(*AppleScript) error
		wantSub string
	}{
		{"Pause", func(a *AppleScript) error { return a.Pause() }, `tell application "Spotify" to pause`},
		{"Resume", func(a *AppleScript) error { return a.Resume() }, `tell application "Spotify" to play`},
		{"Next", func(a *AppleScript) error { return a.Next() }, `tell application "Spotify" to next track`},
		{"Previous", func(a *AppleScript) error { return a.Previous() }, `tell application "Spotify" to previous track`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := &recorder{}
			a := newDriver(r)

			if err := tc.fn(a); err != nil {
				t.Fatalf("%s returned error: %v", tc.name, err)
			}
			if len(r.scripts) != 1 {
				t.Fatalf("%s expected 1 script invocation, got %d", tc.name, len(r.scripts))
			}
			if !strings.Contains(r.scripts[0], tc.wantSub) {
				t.Errorf("%s script %q missing %q", tc.name, r.scripts[0], tc.wantSub)
			}
		})
	}
}

func TestSimpleControls_WrapErrors(t *testing.T) {
	for _, tc := range []struct {
		name string
		call func(*AppleScript) error
	}{
		{"Pause", func(a *AppleScript) error { return a.Pause() }},
		{"Resume", func(a *AppleScript) error { return a.Resume() }},
		{"Next", func(a *AppleScript) error { return a.Next() }},
		{"Previous", func(a *AppleScript) error { return a.Previous() }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := &recorder{err: errors.New("exit status 1")}
			a := newDriver(r)

			err := tc.call(a)
			if err == nil {
				t.Fatalf("%s expected error from failing osascript", tc.name)
			}
			if !strings.Contains(err.Error(), tc.name+":") {
				t.Errorf("%s error not wrapped with method name: %v", tc.name, err)
			}
		})
	}
}

func TestSetVolume_Valid(t *testing.T) {
	cases := []int{0, 1, 50, 99, 100}
	for _, pct := range cases {
		t.Run(fmt.Sprintf("pct=%d", pct), func(t *testing.T) {
			r := &recorder{}
			a := newDriver(r)

			if err := a.SetVolume(pct); err != nil {
				t.Fatalf("SetVolume(%d) error: %v", pct, err)
			}
			if len(r.scripts) != 1 {
				t.Fatalf("expected 1 script, got %d", len(r.scripts))
			}
			want := fmt.Sprintf("set sound volume to %d", pct)
			if !strings.Contains(r.scripts[0], want) {
				t.Errorf("SetVolume(%d) script %q missing %q", pct, r.scripts[0], want)
			}
			if !strings.Contains(r.scripts[0], `tell application "Spotify"`) {
				t.Errorf("SetVolume(%d) script missing Spotify tell block: %q", pct, r.scripts[0])
			}
		})
	}
}

func TestSetVolume_OutOfRange(t *testing.T) {
	cases := []int{-1, 101, -100, 1000}
	for _, pct := range cases {
		t.Run(fmt.Sprintf("pct=%d", pct), func(t *testing.T) {
			r := &recorder{}
			a := newDriver(r)

			err := a.SetVolume(pct)
			if !errors.Is(err, ErrInvalidArg) {
				t.Fatalf("SetVolume(%d) want ErrInvalidArg, got %v", pct, err)
			}
			if len(r.scripts) != 0 {
				t.Errorf("SetVolume(%d) must not invoke osascript on invalid input", pct)
			}
		})
	}
}

func TestCurrentTrack_ParsesPipeDelimitedOutput(t *testing.T) {
	r := &recorder{
		out: "Blinding Lights|The Weeknd|spotify:track:0VjIjW4GlUZAMYd2vXMi3b|45",
	}
	a := newDriver(r)

	got, err := a.CurrentTrack()
	if err != nil {
		t.Fatalf("CurrentTrack error: %v", err)
	}
	want := AppleScriptTrack{
		Name:            "Blinding Lights",
		Artist:          "The Weeknd",
		URI:             "spotify:track:0VjIjW4GlUZAMYd2vXMi3b",
		PositionSeconds: 45,
	}
	if got != want {
		t.Errorf("CurrentTrack got %+v, want %+v", got, want)
	}
	if len(r.scripts) != 1 {
		t.Fatalf("expected 1 script invocation, got %d", len(r.scripts))
	}
	for _, want := range []string{
		`tell application "Spotify"`,
		`current track`,
		`name of t`,
		`artist of t`,
		`spotify url of t`,
		`player position`,
	} {
		if !strings.Contains(r.scripts[0], want) {
			t.Errorf("CurrentTrack script missing %q in %q", want, r.scripts[0])
		}
	}
}

func TestCurrentTrack_SpotifyNotRunning_ReturnsZeroValueNoError(t *testing.T) {
	cases := []struct {
		name string
		out  string
		err  error
	}{
		{
			name: "error in stdout",
			out:  `0:53: execution error: Spotify got an error: Connection is invalid. (-609)`,
			err:  errors.New("exit status 1"),
		},
		{
			name: "error in error message only",
			out:  "",
			err:  errors.New("osascript: Spotify got an error: ..."),
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := &recorder{out: tc.out, err: tc.err}
			a := newDriver(r)

			got, err := a.CurrentTrack()
			if err != nil {
				t.Fatalf("CurrentTrack with Spotify not running expected nil error, got %v", err)
			}
			if got != (AppleScriptTrack{}) {
				t.Errorf("CurrentTrack with Spotify not running expected zero Track, got %+v", got)
			}
		})
	}
}

func TestCurrentTrack_OsascriptFailureUnrelatedToSpotify(t *testing.T) {
	r := &recorder{
		out: "",
		err: errors.New("osascript: file not found"),
	}
	a := newDriver(r)

	_, err := a.CurrentTrack()
	if err == nil {
		t.Fatal("CurrentTrack expected error for unrelated osascript failure")
	}
	if !strings.Contains(err.Error(), "CurrentTrack:") {
		t.Errorf("error not wrapped with method name: %v", err)
	}
}

func TestCurrentTrack_EmptyOutputReturnsZeroValue(t *testing.T) {
	r := &recorder{out: ""}
	a := newDriver(r)

	got, err := a.CurrentTrack()
	if err != nil {
		t.Fatalf("CurrentTrack empty output error: %v", err)
	}
	if got != (AppleScriptTrack{}) {
		t.Errorf("CurrentTrack empty output expected zero Track, got %+v", got)
	}
}

func TestCurrentTrack_MalformedOutput(t *testing.T) {
	cases := []string{
		"only|three|fields",
		"five|fields|here|are|present",
		"name|artist|uri|not-an-int",
	}
	for _, out := range cases {
		t.Run(out, func(t *testing.T) {
			r := &recorder{out: out}
			a := newDriver(r)

			_, err := a.CurrentTrack()
			if err == nil {
				t.Errorf("CurrentTrack with malformed %q expected error, got nil", out)
			}
		})
	}
}

func TestNewAppleScript_WiresDefault(t *testing.T) {
	a := NewAppleScript()
	if a == nil {
		t.Fatal("NewAppleScript returned nil")
	}
	if a.osascript == nil {
		t.Fatal("NewAppleScript did not wire default osascript invoker")
	}
}
