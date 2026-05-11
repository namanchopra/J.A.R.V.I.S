package jarvis

import (
	"fmt"
	"log/slog"
	"regexp"
	"strings"
)

// maxTailLines is the number of trailing output lines analysed for errors.
const maxTailLines = 200

// ---------------------------------------------------------------------------
// SessionAnalysis
// ---------------------------------------------------------------------------

// SessionAnalysis holds the results of scanning a session's terminal output
// for errors, affected files, and a human-readable summary.
type SessionAnalysis struct {
	Errors        []string `json:"errors"`        // extracted error messages
	AffectedFiles []string `json:"affectedFiles"` // deduplicated file:line references
	Summary       string   `json:"summary"`       // one-line human-readable summary
	HasErrors     bool     `json:"hasErrors"`
}

// ---------------------------------------------------------------------------
// OutputAnalyzer
// ---------------------------------------------------------------------------

// OutputAnalyzer reads terminal output for a session and extracts structured
// error information. The getOutput function is injected at construction time
// so callers can wire it to App.GetSessionTerminalOutput or a test stub.
type OutputAnalyzer struct {
	getOutput func(pid int) (string, error)
}

// NewOutputAnalyzer creates an OutputAnalyzer that reads terminal output via
// the supplied function.
func NewOutputAnalyzer(getOutput func(pid int) (string, error)) *OutputAnalyzer {
	return &OutputAnalyzer{getOutput: getOutput}
}

// ---------------------------------------------------------------------------
// Error patterns
// ---------------------------------------------------------------------------

// errorPatterns are compiled once at package init and reused on every call.
var errorPatterns = compileErrorPatterns()

func compileErrorPatterns() []*regexp.Regexp {
	raw := []string{
		// TypeScript / JavaScript
		`error TS\d+:`,
		`TypeError:`,
		`SyntaxError:`,
		`Cannot find module`,
		`ReferenceError:`,

		// Go
		`cannot use `,
		`undefined:`,
		`syntax error`,
		`panic:`,
		`fatal error:`,

		// Python
		`Traceback \(most recent call last\)`,
		`(\w+Error):`,
		`(\w+Exception):`,

		// npm / node
		`ERR!`,
		`npm error`,

		// General (case-insensitive handled via (?i))
		`(?i)^.*error:`,
		`(?i)\bFAIL\b`,
		`(?i)\bfailed\b`,
		`(?i)\bfatal\b`,
	}

	patterns := make([]*regexp.Regexp, 0, len(raw))
	for _, r := range raw {
		patterns = append(patterns, regexp.MustCompile(r))
	}
	return patterns
}

// fileRefPattern matches file paths with a recognised extension followed by a
// line number, for example:
//
//	src/components/Cart.tsx:42:10
//	/Users/dev/project/main.go:17
//	at handler.go:55
var fileRefPattern = regexp.MustCompile(
	`(?:^|[\s(])([^\s()"']+\.(?:ts|tsx|js|jsx|go|py)):(\d+)`,
)

// ---------------------------------------------------------------------------
// AnalyzeSession
// ---------------------------------------------------------------------------

// AnalyzeSession reads terminal output for the given PID, takes the last
// maxTailLines lines, and scans for error patterns and file references.
func (a *OutputAnalyzer) AnalyzeSession(pid int) (SessionAnalysis, error) {
	output, err := a.getOutput(pid)
	if err != nil {
		return SessionAnalysis{}, fmt.Errorf("reading session output for PID %d: %w", pid, err)
	}

	if strings.TrimSpace(output) == "" {
		slog.Debug("analysis: empty output", "pid", pid)
		return SessionAnalysis{
			Errors:        []string{},
			AffectedFiles: []string{},
			Summary:       "No output available",
			HasErrors:     false,
		}, nil
	}

	lines := tailLines(output, maxTailLines)

	errors := extractErrors(lines)
	files := extractFileRefs(lines)

	summary := buildSummary(errors, files)

	slog.Debug("analysis complete",
		"pid", pid,
		"errorCount", len(errors),
		"fileCount", len(files),
	)

	return SessionAnalysis{
		Errors:        errors,
		AffectedFiles: files,
		Summary:       summary,
		HasErrors:     len(errors) > 0,
	}, nil
}

// ---------------------------------------------------------------------------
// Internal helpers
// ---------------------------------------------------------------------------

// tailLines returns the last n lines of text. If there are fewer than n lines,
// all lines are returned.
func tailLines(text string, n int) []string {
	all := strings.Split(text, "\n")
	if len(all) <= n {
		return all
	}
	return all[len(all)-n:]
}

// extractErrors scans lines for known error patterns and returns the matching
// lines, trimmed and deduplicated.
func extractErrors(lines []string) []string {
	seen := make(map[string]struct{})
	var result []string

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if _, dup := seen[trimmed]; dup {
			continue
		}

		for _, pat := range errorPatterns {
			if pat.MatchString(trimmed) {
				seen[trimmed] = struct{}{}
				result = append(result, trimmed)
				break // one match per line is enough
			}
		}
	}

	if result == nil {
		return []string{}
	}
	return result
}

// extractFileRefs finds file:line references in the output and returns them
// deduplicated.
func extractFileRefs(lines []string) []string {
	seen := make(map[string]struct{})
	var result []string

	for _, line := range lines {
		matches := fileRefPattern.FindAllStringSubmatch(line, -1)
		for _, m := range matches {
			// m[1] = file path, m[2] = line number
			ref := m[1] + ":" + m[2]
			if _, dup := seen[ref]; dup {
				continue
			}
			seen[ref] = struct{}{}
			result = append(result, ref)
		}
	}

	if result == nil {
		return []string{}
	}
	return result
}

// buildSummary produces a one-line human-readable summary from the extracted
// errors and file references.
func buildSummary(errors []string, files []string) string {
	if len(errors) == 0 {
		return "No errors detected in recent output"
	}

	// Start with error count and first error (truncated).
	first := errors[0]
	if len(first) > 80 {
		first = first[:77] + "..."
	}
	summary := fmt.Sprintf("%d error(s) detected: %s", len(errors), first)

	// Append affected files if any.
	if len(files) > 0 {
		// Show up to 3 files to keep the summary concise.
		shown := files
		if len(shown) > 3 {
			shown = shown[:3]
		}
		summary += " in " + strings.Join(shown, ", ")
		if len(files) > 3 {
			summary += fmt.Sprintf(" (+%d more)", len(files)-3)
		}
	}

	return summary
}
