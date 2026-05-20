package main

// ---------------------------------------------------------------------------
// Tests for TASK-016 — JarvisInstallBundledShortcuts.
//
// The fixtures are written into a TempDir and pinned via
// bundledShortcutsDirOverride so we never touch the real
// build/shortcuts/ tree. The `shortcuts` CLI is stubbed via
// shortcutsImportCommandFn, mirroring the startJarvisCommandFn test-seam
// pattern used in app_jarvis_test.go.
// ---------------------------------------------------------------------------

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/namanchopra/jarvis/internal/paths"
)

// writeManifest writes manifest.json into dir, then writes each entry's
// .shortcut file with the supplied body. Returns the dir for chaining.
func writeManifest(t *testing.T, dir string, entries []bundledShortcutEntry, bodies map[string]string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir shortcuts dir: %v", err)
	}
	// Build manifest body by hand — no need to round-trip the type since
	// the production decoder does that, and an inline write keeps the
	// fixture readable.
	var sb strings.Builder
	sb.WriteString(`{"shortcuts":[`)
	for i, e := range entries {
		if i > 0 {
			sb.WriteString(",")
		}
		sb.WriteString(`{"name":"`)
		sb.WriteString(e.Name)
		sb.WriteString(`","filename":"`)
		sb.WriteString(e.Filename)
		sb.WriteString(`","description":"`)
		sb.WriteString(e.Description)
		sb.WriteString(`"}`)
	}
	sb.WriteString("]}")
	if err := os.WriteFile(filepath.Join(dir, "manifest.json"), []byte(sb.String()), 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	for _, e := range entries {
		body, ok := bodies[e.Filename]
		if !ok {
			continue
		}
		if err := os.WriteFile(filepath.Join(dir, e.Filename), []byte(body), 0o644); err != nil {
			t.Fatalf("write %s: %v", e.Filename, err)
		}
	}
	return dir
}

// pinShortcutsDir overrides resolveBundledShortcutsDir for the test's
// lifetime so the installer reads from `dir`. The cleanup restores the
// previous value.
func pinShortcutsDir(t *testing.T, dir string) {
	t.Helper()
	prev := bundledShortcutsDirOverride
	bundledShortcutsDirOverride = func() string { return dir }
	t.Cleanup(func() { bundledShortcutsDirOverride = prev })
}

// stubShortcutsImport swaps shortcutsImportCommandFn for one that runs
// /usr/bin/true so Run() succeeds without actually shelling out to
// `shortcuts`. The supplied counter is incremented on each call so tests
// can assert the import-vs-skip split.
func stubShortcutsImport(t *testing.T, calls *int) {
	t.Helper()
	prev := shortcutsImportCommandFn
	shortcutsImportCommandFn = func(name string, arg ...string) *exec.Cmd {
		*calls++
		// /usr/bin/true exists on macOS; /bin/true is missing there.
		return exec.Command("/usr/bin/true")
	}
	t.Cleanup(func() { shortcutsImportCommandFn = prev })
}

// TestJarvisInstallBundledShortcuts_SentinelShortCircuit asserts that when
// the v0.3.0 sentinel already exists under ~/.jarvis, the installer
// returns "already installed" without scanning the shortcuts dir or
// invoking the `shortcuts` CLI.
func TestJarvisInstallBundledShortcuts_SentinelShortCircuit(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	// Pre-create the sentinel so the installer short-circuits.
	if err := os.MkdirAll(paths.JarvisHome(), 0o755); err != nil {
		t.Fatalf("mkdir jarvis home: %v", err)
	}
	sentinel := filepath.Join(paths.JarvisHome(), bundledShortcutsSentinelName)
	if err := os.WriteFile(sentinel, []byte("prior run\n"), 0o644); err != nil {
		t.Fatalf("write sentinel: %v", err)
	}

	// Pin the shortcuts dir at a directory that exists but contains nothing —
	// if the installer scans it for any reason the test fails on the missing
	// manifest. The point is it should NOT scan.
	pinShortcutsDir(t, filepath.Join(tmp, "shortcuts-dir"))
	_ = os.MkdirAll(filepath.Join(tmp, "shortcuts-dir"), 0o755)

	var calls int
	stubShortcutsImport(t, &calls)

	a := &App{}
	msg, err := a.JarvisInstallBundledShortcuts()
	if err != nil {
		t.Fatalf("JarvisInstallBundledShortcuts() = %v; want nil", err)
	}
	if !strings.Contains(msg, "already installed") {
		t.Errorf("msg = %q; expected to contain %q", msg, "already installed")
	}
	if calls != 0 {
		t.Errorf("shortcuts import invoked %d times; want 0 (sentinel short-circuit)", calls)
	}
}

// TestJarvisInstallBundledShortcuts_AllPlaceholdersSkipped exercises the
// placeholder path: every .shortcut file starts with the sentinel header,
// so none should be imported (calls=0) and the summary should report all
// as skipped.
func TestJarvisInstallBundledShortcuts_AllPlaceholdersSkipped(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	dir := filepath.Join(tmp, "shortcuts-dir")
	entries := []bundledShortcutEntry{
		{Name: "Take Note", Filename: "take-note.shortcut", Description: "x"},
		{Name: "Lock Screen", Filename: "lock-screen.shortcut", Description: "x"},
		{Name: "Sleep", Filename: "sleep.shortcut", Description: "x"},
	}
	bodies := map[string]string{
		"take-note.shortcut":   placeholderShortcutHeader + "\nname: Take Note\n",
		"lock-screen.shortcut": placeholderShortcutHeader + "\nname: Lock Screen\n",
		"sleep.shortcut":       placeholderShortcutHeader + "\nname: Sleep\n",
	}
	writeManifest(t, dir, entries, bodies)
	pinShortcutsDir(t, dir)

	var calls int
	stubShortcutsImport(t, &calls)

	a := &App{}
	msg, err := a.JarvisInstallBundledShortcuts()
	if err != nil {
		t.Fatalf("JarvisInstallBundledShortcuts() = %v; want nil", err)
	}
	if calls != 0 {
		t.Errorf("shortcuts import invoked %d times; want 0 (placeholders should never be handed to the CLI)", calls)
	}
	if !strings.Contains(msg, "0 installed") {
		t.Errorf("msg = %q; expected to contain %q", msg, "0 installed")
	}
	if !strings.Contains(msg, "3 placeholders skipped") {
		t.Errorf("msg = %q; expected to contain %q", msg, "3 placeholders skipped")
	}
	if !strings.Contains(msg, "0 failed") {
		t.Errorf("msg = %q; expected to contain %q", msg, "0 failed")
	}

	// Sentinel must exist so a second call returns the short-circuit message.
	sentinel := filepath.Join(paths.JarvisHome(), bundledShortcutsSentinelName)
	if _, err := os.Stat(sentinel); err != nil {
		t.Fatalf("sentinel not written after install: %v", err)
	}

	// Second call returns early — proves idempotence per the acceptance
	// criteria ("second run is a no-op").
	msg2, err := a.JarvisInstallBundledShortcuts()
	if err != nil {
		t.Fatalf("second JarvisInstallBundledShortcuts() = %v; want nil", err)
	}
	if !strings.Contains(msg2, "already installed") {
		t.Errorf("second-call msg = %q; expected to contain %q", msg2, "already installed")
	}
}

// TestJarvisInstallBundledShortcuts_RealEntriesImported exercises the
// success path: a manifest entry whose file lacks the placeholder header is
// handed to the stubbed `shortcuts import` invocation. The stub records the
// call so we can assert it ran exactly once.
func TestJarvisInstallBundledShortcuts_RealEntriesImported(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	dir := filepath.Join(tmp, "shortcuts-dir")
	entries := []bundledShortcutEntry{
		{Name: "Real One", Filename: "real-one.shortcut", Description: "x"},
		{Name: "Placeholder", Filename: "placeholder.shortcut", Description: "x"},
	}
	bodies := map[string]string{
		// Non-placeholder body — installer hands it to `shortcuts import`.
		"real-one.shortcut":    "bplist00\x00\x00\x00fake-binary-plist",
		"placeholder.shortcut": placeholderShortcutHeader + "\nname: Placeholder\n",
	}
	writeManifest(t, dir, entries, bodies)
	pinShortcutsDir(t, dir)

	var calls int
	stubShortcutsImport(t, &calls)

	a := &App{}
	msg, err := a.JarvisInstallBundledShortcuts()
	if err != nil {
		t.Fatalf("JarvisInstallBundledShortcuts() = %v; want nil", err)
	}
	if calls != 1 {
		t.Errorf("shortcuts import invoked %d times; want 1 (one real entry)", calls)
	}
	if !strings.Contains(msg, "1 installed") {
		t.Errorf("msg = %q; expected to contain %q", msg, "1 installed")
	}
	if !strings.Contains(msg, "1 placeholders skipped") {
		t.Errorf("msg = %q; expected to contain %q", msg, "1 placeholders skipped")
	}
}

// TestJarvisInstallBundledShortcuts_MissingDir asserts that a clean
// "nothing to install" path returns a non-error message when no shortcuts
// directory can be located. Real production hits this when the .app is
// built without the post-build copy step.
func TestJarvisInstallBundledShortcuts_MissingDir(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	// Override returns "" — installer should bail with a benign message.
	pinShortcutsDir(t, "")

	a := &App{}
	msg, err := a.JarvisInstallBundledShortcuts()
	if err != nil {
		t.Fatalf("JarvisInstallBundledShortcuts() = %v; want nil", err)
	}
	if !strings.Contains(msg, "No bundled shortcuts directory found") {
		t.Errorf("msg = %q; expected to contain %q", msg, "No bundled shortcuts directory found")
	}
}

// TestJarvisInstallBundledShortcuts_BadManifest asserts that a manifest
// file containing invalid JSON returns a wrapped error with the binding's
// name prefix — not a panic. This is the "clean error not panic" path
// from the spec.
func TestJarvisInstallBundledShortcuts_BadManifest(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	dir := filepath.Join(tmp, "shortcuts-dir")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "manifest.json"), []byte("{not-valid-json"), 0o644); err != nil {
		t.Fatalf("write bad manifest: %v", err)
	}
	pinShortcutsDir(t, dir)

	a := &App{}
	_, err := a.JarvisInstallBundledShortcuts()
	if err == nil {
		t.Fatalf("JarvisInstallBundledShortcuts() with bad manifest = nil; want error")
	}
	if !strings.Contains(err.Error(), "JarvisInstallBundledShortcuts:") {
		t.Errorf("error %q; expected the binding name prefix", err.Error())
	}
}

// TestJarvisInstallBundledShortcuts_MissingManifest asserts the same
// no-panic contract when the shortcuts dir exists but manifest.json does
// not. Returns a wrapped error rather than treating it as "nothing to do".
func TestJarvisInstallBundledShortcuts_MissingManifest(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	dir := filepath.Join(tmp, "shortcuts-dir")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// Intentionally do NOT write manifest.json.
	pinShortcutsDir(t, dir)

	a := &App{}
	_, err := a.JarvisInstallBundledShortcuts()
	if err == nil {
		t.Fatalf("JarvisInstallBundledShortcuts() with missing manifest = nil; want error")
	}
	if !strings.Contains(err.Error(), "JarvisInstallBundledShortcuts:") {
		t.Errorf("error %q; expected the binding name prefix", err.Error())
	}
}

// TestJarvisInstallBundledShortcuts_ImportFailureCounted asserts that a
// failing `shortcuts import` invocation increments the failed counter but
// does NOT abort the loop — the next entry still gets a chance to install.
// The sentinel is still written so the failure doesn't trigger an install
// retry on every launch.
func TestJarvisInstallBundledShortcuts_ImportFailureCounted(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	dir := filepath.Join(tmp, "shortcuts-dir")
	entries := []bundledShortcutEntry{
		{Name: "Fails", Filename: "fails.shortcut", Description: "x"},
		{Name: "Succeeds", Filename: "succeeds.shortcut", Description: "x"},
	}
	bodies := map[string]string{
		"fails.shortcut":    "fake-binary",
		"succeeds.shortcut": "fake-binary",
	}
	writeManifest(t, dir, entries, bodies)
	pinShortcutsDir(t, dir)

	// Stub that fails on the first call, succeeds on the second.
	var calls int
	prev := shortcutsImportCommandFn
	shortcutsImportCommandFn = func(name string, arg ...string) *exec.Cmd {
		calls++
		if calls == 1 {
			return exec.Command("/usr/bin/false")
		}
		return exec.Command("/usr/bin/true")
	}
	t.Cleanup(func() { shortcutsImportCommandFn = prev })

	a := &App{}
	msg, err := a.JarvisInstallBundledShortcuts()
	if err != nil {
		t.Fatalf("JarvisInstallBundledShortcuts() = %v; want nil", err)
	}
	if calls != 2 {
		t.Errorf("calls = %d; want 2 (loop must continue past the failed entry)", calls)
	}
	if !strings.Contains(msg, "1 installed") {
		t.Errorf("msg = %q; expected to contain %q", msg, "1 installed")
	}
	if !strings.Contains(msg, "1 failed") {
		t.Errorf("msg = %q; expected to contain %q", msg, "1 failed")
	}

	sentinel := filepath.Join(paths.JarvisHome(), bundledShortcutsSentinelName)
	if _, err := os.Stat(sentinel); err != nil {
		t.Fatalf("sentinel not written after install: %v", err)
	}
}

// TestJarvisInstallBundledShortcuts_MissingShortcutFile asserts that a
// manifest entry whose .shortcut file is absent on disk is counted as
// failed (not skipped, not panicked).
func TestJarvisInstallBundledShortcuts_MissingShortcutFile(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	dir := filepath.Join(tmp, "shortcuts-dir")
	entries := []bundledShortcutEntry{
		{Name: "Ghost", Filename: "ghost.shortcut", Description: "x"},
	}
	// No bodies map entry — the .shortcut file is never written.
	writeManifest(t, dir, entries, map[string]string{})
	pinShortcutsDir(t, dir)

	var calls int
	stubShortcutsImport(t, &calls)

	a := &App{}
	msg, err := a.JarvisInstallBundledShortcuts()
	if err != nil {
		t.Fatalf("JarvisInstallBundledShortcuts() = %v; want nil", err)
	}
	if calls != 0 {
		t.Errorf("calls = %d; want 0 (a missing file must not be handed to the CLI)", calls)
	}
	if !strings.Contains(msg, "1 failed") {
		t.Errorf("msg = %q; expected to contain %q", msg, "1 failed")
	}
}
