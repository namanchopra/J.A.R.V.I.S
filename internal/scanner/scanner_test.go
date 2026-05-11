package scanner

import (
	"os"
	"path/filepath"
	"testing"
)

// ---------------------------------------------------------------------------
// matchClaude
// ---------------------------------------------------------------------------

func TestMatchClaude(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		procName     string
		cmdline      string
		wantMatch    bool
		description  string
	}{
		// --- True positives ---
		{
			name:      "exact_process_name",
			procName:  "claude",
			cmdline:   "claude -p 'do something'",
			wantMatch: true,
			description: "exact process name 'claude' should always match",
		},
		{
			name:      "exact_name_no_cmdline",
			procName:  "claude",
			cmdline:   "",
			wantMatch: true,
			description: "exact process name match even with empty cmdline",
		},
		{
			name:      "cmdline_with_print_flag",
			procName:  "node",
			cmdline:   "/usr/local/bin/claude -p 'write tests'",
			wantMatch: true,
			description: "cmdline with standalone claude and -p flag",
		},
		{
			name:      "cmdline_with_long_print_flag",
			procName:  "node",
			cmdline:   "claude --print 'hello world'",
			wantMatch: true,
			description: "cmdline with claude and --print flag",
		},
		{
			name:      "cmdline_with_output_format",
			procName:  "node",
			cmdline:   "/home/user/.local/bin/claude --output-format json",
			wantMatch: true,
			description: "cmdline with claude and --output-format flag",
		},

		// --- True negatives (false positive prevention) ---
		{
			name:      "claude_code_guide",
			procName:  "claude-code-guide",
			cmdline:   "claude-code-guide --serve",
			wantMatch: false,
			description: "claude-code-guide helper should not match",
		},
		{
			name:      "claude_launcher",
			procName:  "claude-launcher",
			cmdline:   "claude-launcher --start",
			wantMatch: false,
			description: "claude-launcher should not match",
		},
		{
			name:      "electron_helper",
			procName:  "claude Helper",
			cmdline:   "/Applications/Claude.app/Contents/Frameworks/Electron Helper.app/claude Helper",
			wantMatch: false,
			description: "Electron helper process should not match",
		},
		{
			name:      "electron_in_cmdline",
			procName:  "claude",
			cmdline:   "/path/to/Electron --claude",
			wantMatch: false,
			description: "Electron process with claude in args should not match",
		},
		{
			name:      "app_contents_bundle",
			procName:  "someproc",
			cmdline:   "/Applications/Claude.app/Contents/MacOS/Claude",
			wantMatch: false,
			description: "macOS .app bundle path should not match",
		},
		{
			name:      "no_headless_flags",
			procName:  "node",
			cmdline:   "/usr/bin/claude --version",
			wantMatch: false,
			description: "standalone claude without headless flags should not match",
		},
		{
			name:      "claude_substring",
			procName:  "node",
			cmdline:   "some-claude-thing -p foo",
			wantMatch: false,
			description: "claude as substring of another command should not match",
		},
		{
			name:      "unrelated_process",
			procName:  "nginx",
			cmdline:   "nginx -g daemon off;",
			wantMatch: false,
			description: "completely unrelated process should not match",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := matchClaude(tt.procName, tt.cmdline)
			if got != tt.wantMatch {
				t.Errorf("matchClaude(%q, %q) = %v, want %v (%s)",
					tt.procName, tt.cmdline, got, tt.wantMatch, tt.description)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// matchExactAgent
// ---------------------------------------------------------------------------

func TestMatchExactAgent(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		procName  string
		cmdline   string
		agent     string
		wantMatch bool
	}{
		// --- Kiro ---
		{name: "kiro_exact_name", procName: "kiro-cli", cmdline: "", agent: "kiro-cli", wantMatch: true},
		{name: "kiro_vscode_extension", procName: "kiro", cmdline: "kiro --extension-mode", agent: "kiro-cli", wantMatch: false},
		{name: "kiro_cmdline_starts", procName: "node", cmdline: "kiro-cli run task", agent: "kiro-cli", wantMatch: true},
		{name: "kiro_cmdline_path", procName: "node", cmdline: "/usr/local/bin/kiro-cli run", agent: "kiro-cli", wantMatch: true},
		{name: "kiro_substring", procName: "node", cmdline: "some-kiro-cli-thing", agent: "kiro-cli", wantMatch: false},

		// --- Gemini ---
		{name: "gemini_exact_name", procName: "gemini", cmdline: "", agent: "gemini", wantMatch: true},
		{name: "gemini_cmdline_starts", procName: "node", cmdline: "gemini chat", agent: "gemini", wantMatch: true},
		{name: "gemini_cmdline_path", procName: "node", cmdline: "/usr/bin/gemini ask", agent: "gemini", wantMatch: true},
		{name: "gemini_substring", procName: "node", cmdline: "gemini-pro-extension", agent: "gemini", wantMatch: false},
		{name: "gemini_unrelated", procName: "nginx", cmdline: "nginx", agent: "gemini", wantMatch: false},

		// --- Codex ---
		{name: "codex_exact_name", procName: "codex", cmdline: "", agent: "codex", wantMatch: true},
		{name: "codex_cmdline_starts", procName: "node", cmdline: "codex generate", agent: "codex", wantMatch: true},
		{name: "codex_cmdline_path", procName: "node", cmdline: "/opt/bin/codex run", agent: "codex", wantMatch: true},
		{name: "codex_substring", procName: "node", cmdline: "codex-runner start", agent: "codex", wantMatch: false},

		// --- Aider ---
		{name: "aider_exact_name", procName: "aider", cmdline: "", agent: "aider", wantMatch: true},
		{name: "aider_cmdline_starts", procName: "python", cmdline: "aider --model gpt-4", agent: "aider", wantMatch: true},
		{name: "aider_cmdline_path", procName: "python", cmdline: "/home/user/.local/bin/aider --yes", agent: "aider", wantMatch: true},
		{name: "aider_substring", procName: "python", cmdline: "aider-wrapper start", agent: "aider", wantMatch: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := matchExactAgent(tt.procName, tt.cmdline, tt.agent)
			if got != tt.wantMatch {
				t.Errorf("matchExactAgent(%q, %q, %q) = %v, want %v",
					tt.procName, tt.cmdline, tt.agent, got, tt.wantMatch)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// isStandaloneCommand
// ---------------------------------------------------------------------------

func TestIsStandaloneCommand(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		cmdline string
		cmd     string
		want    bool
	}{
		{name: "starts_with_cmd_space", cmdline: "claude -p hello", cmd: "claude", want: true},
		{name: "path_qualified", cmdline: "/usr/local/bin/claude -p hi", cmd: "claude", want: true},
		{name: "exact_match", cmdline: "claude", cmd: "claude", want: true},
		{name: "ends_with_path", cmdline: "/usr/local/bin/claude", cmd: "claude", want: true},
		{name: "substring_only", cmdline: "some-claude-thing", cmd: "claude", want: false},
		{name: "prefix_match", cmdline: "claudex run", cmd: "claude", want: false},
		{name: "embedded_in_path", cmdline: "/opt/claude-tools/run", cmd: "claude", want: false},
		{name: "empty_cmdline", cmdline: "", cmd: "claude", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := isStandaloneCommand(tt.cmdline, tt.cmd)
			if got != tt.want {
				t.Errorf("isStandaloneCommand(%q, %q) = %v, want %v",
					tt.cmdline, tt.cmd, got, tt.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// containsAnyPattern
// ---------------------------------------------------------------------------

func TestContainsAnyPattern(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		s        string
		patterns []string
		want     bool
	}{
		{name: "match_first", s: "Electron Helper", patterns: excludePatterns, want: true},
		{name: "match_renderer", s: "/path/to/renderer process", patterns: excludePatterns, want: true},
		{name: "match_gpu", s: "some gpu-process --flag", patterns: excludePatterns, want: true},
		{name: "match_app_contents", s: "/Applications/Foo.app/Contents/MacOS/bar", patterns: excludePatterns, want: true},
		{name: "case_insensitive", s: "electron helper", patterns: excludePatterns, want: true},
		{name: "no_match", s: "aider --model gpt-4", patterns: excludePatterns, want: false},
		{name: "empty_string", s: "", patterns: excludePatterns, want: false},
		{name: "empty_patterns", s: "anything", patterns: nil, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := containsAnyPattern(tt.s, tt.patterns)
			if got != tt.want {
				t.Errorf("containsAnyPattern(%q, %v) = %v, want %v",
					tt.s, tt.patterns, got, tt.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// hasGitDir
// ---------------------------------------------------------------------------

func TestHasGitDir(t *testing.T) {
	t.Parallel()

	t.Run("git_dir_in_current", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		if err := os.Mkdir(filepath.Join(dir, ".git"), 0o755); err != nil {
			t.Fatal(err)
		}
		if !hasGitDir(dir, 5) {
			t.Error("expected true for directory with .git")
		}
	})

	t.Run("git_dir_in_parent", func(t *testing.T) {
		t.Parallel()
		root := t.TempDir()
		if err := os.Mkdir(filepath.Join(root, ".git"), 0o755); err != nil {
			t.Fatal(err)
		}
		child := filepath.Join(root, "src", "pkg")
		if err := os.MkdirAll(child, 0o755); err != nil {
			t.Fatal(err)
		}
		if !hasGitDir(child, 5) {
			t.Error("expected true for subdirectory of git repo")
		}
	})

	t.Run("no_git_dir", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		if hasGitDir(dir, 5) {
			t.Error("expected false for directory without .git")
		}
	})

	t.Run("git_dir_beyond_max_depth", func(t *testing.T) {
		t.Parallel()
		root := t.TempDir()
		if err := os.Mkdir(filepath.Join(root, ".git"), 0o755); err != nil {
			t.Fatal(err)
		}
		// Create a deeply nested child beyond maxDepth=2
		child := filepath.Join(root, "a", "b", "c", "d")
		if err := os.MkdirAll(child, 0o755); err != nil {
			t.Fatal(err)
		}
		// With maxDepth 2, from "d" we check d, c, b -- that's 3 levels, and
		// root is 4 levels up, so it should not be found.
		if hasGitDir(child, 2) {
			t.Error("expected false when .git is beyond max depth")
		}
	})

	t.Run("git_dir_at_exact_max_depth", func(t *testing.T) {
		t.Parallel()
		root := t.TempDir()
		if err := os.Mkdir(filepath.Join(root, ".git"), 0o755); err != nil {
			t.Fatal(err)
		}
		// root/a/b -- depth 2 from root means checking b(0), a(1), root(2)
		child := filepath.Join(root, "a", "b")
		if err := os.MkdirAll(child, 0o755); err != nil {
			t.Fatal(err)
		}
		if !hasGitDir(child, 2) {
			t.Error("expected true when .git is at exact max depth")
		}
	})
}

// ---------------------------------------------------------------------------
// truncate
// ---------------------------------------------------------------------------

func TestTruncate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		input  string
		maxLen int
		want   string
	}{
		{name: "short", input: "hello", maxLen: 10, want: "hello"},
		{name: "exact", input: "hello", maxLen: 5, want: "hello"},
		{name: "long", input: "hello world", maxLen: 5, want: "hello"},
		{name: "empty", input: "", maxLen: 5, want: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := truncate(tt.input, tt.maxLen)
			if got != tt.want {
				t.Errorf("truncate(%q, %d) = %q, want %q",
					tt.input, tt.maxLen, got, tt.want)
			}
		})
	}
}
