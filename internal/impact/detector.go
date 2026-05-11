package impact

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/namanchopra/jarvis/internal/git"
)

// ImpactSeverity classifies the urgency of a cross-session conflict.
type ImpactSeverity string

const (
	SeverityLow    ImpactSeverity = "low"
	SeverityMedium ImpactSeverity = "medium"
	SeverityHigh   ImpactSeverity = "high"
)

// ImpactWarning describes a potential conflict between two active sessions.
type ImpactWarning struct {
	ID           string         `json:"id"`
	Severity     ImpactSeverity `json:"severity"`
	Description  string         `json:"description"`
	SessionA     string         `json:"sessionA"`     // repo/session name
	SessionB     string         `json:"sessionB"`     // repo/session name
	ConflictType string         `json:"conflictType"` // "shared-dependency", "shared-file", "api-change"
	Details      string         `json:"details"`      // specific file or package name
}

// SessionChanges captures the changed files and dependency modifications for
// a single active session.
type SessionChanges struct {
	Name         string
	RepoPath     string
	ChangedFiles []string // relative paths from git diff --name-only
	ChangedDeps  []string // package names from package.json/go.mod changes
}

// depFiles lists the dependency manifest files we recognise. When any of these
// appear in a session's changed files, we flag a shared-dependency conflict.
var depFiles = map[string]bool{
	"package.json": true,
	"go.mod":       true,
	"go.sum":       true,
}

// sharedPathSegments are directory prefixes that indicate files belong to a
// shared/common package within a monorepo.
var sharedPathSegments = []string{
	"packages/shared/",
	"packages/common/",
	"libs/",
	"shared/",
	"common/",
}

// apiPathSegments are path components that signal API contract surface area.
var apiPathSegments = []string{
	"/api/",
	"/routes/",
	"/types/",
}

// GetSessionChanges returns the changed files and dependency modifications for
// the repository at repoPath. It uses the existing git package to run
// `git diff --name-only HEAD` and then inspects the results for dependency
// manifest changes.
func GetSessionChanges(repoPath string) (SessionChanges, error) {
	if !git.IsGitRepo(repoPath) {
		return SessionChanges{}, fmt.Errorf("path %q is not a git repository", repoPath)
	}

	out, err := git.RunGitCommand(repoPath, "diff", "--name-only", "HEAD")
	if err != nil {
		return SessionChanges{}, fmt.Errorf("GetSessionChanges: %w", err)
	}

	sc := SessionChanges{
		RepoPath:     repoPath,
		Name:         filepath.Base(repoPath),
		ChangedFiles: []string{},
		ChangedDeps:  []string{},
	}

	trimmed := strings.TrimSpace(out)
	if trimmed == "" {
		return sc, nil
	}

	files := strings.Split(trimmed, "\n")
	for _, f := range files {
		f = strings.TrimSpace(f)
		if f == "" {
			continue
		}
		sc.ChangedFiles = append(sc.ChangedFiles, f)

		// If the file is a dependency manifest, record it as a changed dep.
		base := filepath.Base(f)
		if depFiles[base] {
			sc.ChangedDeps = append(sc.ChangedDeps, f)
		}
	}

	return sc, nil
}

// DetectImpacts compares changed files across active sessions and returns
// warnings for potential cross-session conflicts. It returns an empty slice
// when fewer than two sessions are provided or no conflicts are found.
func DetectImpacts(sessions []SessionChanges) []ImpactWarning {
	warnings := []ImpactWarning{}

	if len(sessions) < 2 {
		return warnings
	}

	var idCounter int

	// Compare every unique pair of sessions.
	for i := 0; i < len(sessions); i++ {
		for j := i + 1; j < len(sessions); j++ {
			a := sessions[i]
			b := sessions[j]

			// Rule 1: shared dependency — both sessions modify a dep manifest.
			idCounter = checkSharedDependency(a, b, &warnings, idCounter)

			// Rule 2: shared monorepo file — both sessions touch files in a
			// shared package directory.
			idCounter = checkSharedMonorepoFile(a, b, &warnings, idCounter)

			// Rule 3: API contract — both sessions modify files in API
			// surface-area directories.
			idCounter = checkAPIContract(a, b, &warnings, idCounter)
		}
	}

	return warnings
}

// ---------------------------------------------------------------------------
// Detection rule helpers
// ---------------------------------------------------------------------------

// checkSharedDependency flags a high-severity warning when two sessions both
// modify a dependency manifest file (package.json, go.mod, go.sum).
func checkSharedDependency(a, b SessionChanges, warnings *[]ImpactWarning, id int) int {
	aDeps := depFilesSet(a.ChangedFiles)
	bDeps := depFilesSet(b.ChangedFiles)

	for dep := range aDeps {
		if bDeps[dep] {
			id++
			*warnings = append(*warnings, ImpactWarning{
				ID:           fmt.Sprintf("impact-%d", id),
				Severity:     SeverityHigh,
				Description:  fmt.Sprintf("Both sessions modify %s — potential dependency conflict", dep),
				SessionA:     a.Name,
				SessionB:     b.Name,
				ConflictType: "shared-dependency",
				Details:      dep,
			})
		}
	}

	return id
}

// checkSharedMonorepoFile flags a medium-severity warning when two sessions
// both modify files inside a recognised shared/common package directory.
func checkSharedMonorepoFile(a, b SessionChanges, warnings *[]ImpactWarning, id int) int {
	aShared := sharedFiles(a.ChangedFiles)
	bShared := sharedFiles(b.ChangedFiles)

	// Check for overlapping shared directories (not exact file match — the
	// fact that both sessions touch the same shared area is the signal).
	seen := map[string]bool{}
	for _, f := range aShared {
		seg := matchingSharedSegment(f)
		if seg != "" {
			seen[seg] = true
		}
	}

	for _, f := range bShared {
		seg := matchingSharedSegment(f)
		if seg != "" && seen[seg] {
			id++
			*warnings = append(*warnings, ImpactWarning{
				ID:           fmt.Sprintf("impact-%d", id),
				Severity:     SeverityMedium,
				Description:  fmt.Sprintf("Both sessions modify files in shared package %q", seg),
				SessionA:     a.Name,
				SessionB:     b.Name,
				ConflictType: "shared-file",
				Details:      seg,
			})
			// One warning per shared segment per pair is sufficient.
			delete(seen, seg)
		}
	}

	return id
}

// checkAPIContract flags a medium-severity warning when two sessions both
// modify files that match API surface-area path patterns.
func checkAPIContract(a, b SessionChanges, warnings *[]ImpactWarning, id int) int {
	aAPI := apiFiles(a.ChangedFiles)
	bAPI := apiFiles(b.ChangedFiles)

	if len(aAPI) == 0 || len(bAPI) == 0 {
		return id
	}

	// Build set of matching API segments from session A.
	aSegments := map[string]bool{}
	for _, f := range aAPI {
		seg := matchingAPISegment(f)
		if seg != "" {
			aSegments[seg] = true
		}
	}

	for _, f := range bAPI {
		seg := matchingAPISegment(f)
		if seg != "" && aSegments[seg] {
			id++
			*warnings = append(*warnings, ImpactWarning{
				ID:           fmt.Sprintf("impact-%d", id),
				Severity:     SeverityMedium,
				Description:  fmt.Sprintf("Both sessions modify files in %s — possible API contract conflict", seg),
				SessionA:     a.Name,
				SessionB:     b.Name,
				ConflictType: "api-change",
				Details:      seg,
			})
			// One warning per API segment per pair.
			delete(aSegments, seg)
		}
	}

	return id
}

// ---------------------------------------------------------------------------
// Small helpers
// ---------------------------------------------------------------------------

// depFilesSet returns the set of dependency manifest basenames present in files.
func depFilesSet(files []string) map[string]bool {
	result := map[string]bool{}
	for _, f := range files {
		base := filepath.Base(f)
		if depFiles[base] {
			result[base] = true
		}
	}
	return result
}

// sharedFiles returns the subset of files that live under a recognised shared
// package directory.
func sharedFiles(files []string) []string {
	var result []string
	for _, f := range files {
		if matchingSharedSegment(f) != "" {
			result = append(result, f)
		}
	}
	return result
}

// matchingSharedSegment returns the shared path segment that f matches, or ""
// if it does not match any.
func matchingSharedSegment(f string) string {
	normalised := filepath.ToSlash(f)
	for _, seg := range sharedPathSegments {
		if strings.Contains(normalised, seg) {
			return strings.TrimSuffix(seg, "/")
		}
	}
	return ""
}

// apiFiles returns the subset of files whose path contains an API surface-area
// segment.
func apiFiles(files []string) []string {
	var result []string
	for _, f := range files {
		if matchingAPISegment(f) != "" {
			result = append(result, f)
		}
	}
	return result
}

// matchingAPISegment returns the API path segment that f matches, or "" if it
// does not match any.
func matchingAPISegment(f string) string {
	normalised := filepath.ToSlash(f)
	for _, seg := range apiPathSegments {
		if strings.Contains(normalised, seg) {
			return strings.Trim(seg, "/")
		}
	}
	return ""
}
