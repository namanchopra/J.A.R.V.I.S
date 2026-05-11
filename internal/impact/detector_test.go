package impact

import (
	"testing"
)

func TestDetectImpacts_LessThanTwoSessions(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		sessions []SessionChanges
	}{
		{"nil sessions", nil},
		{"empty sessions", []SessionChanges{}},
		{"single session", []SessionChanges{
			{Name: "session-a", ChangedFiles: []string{"package.json"}},
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			warnings := DetectImpacts(tt.sessions)
			if len(warnings) != 0 {
				t.Errorf("expected 0 warnings, got %d", len(warnings))
			}
		})
	}
}

func TestDetectImpacts_NoConflicts(t *testing.T) {
	t.Parallel()

	sessions := []SessionChanges{
		{
			Name:         "repo-a",
			ChangedFiles: []string{"src/main.go", "README.md"},
		},
		{
			Name:         "repo-b",
			ChangedFiles: []string{"cmd/server/main.go", "Makefile"},
		},
	}

	warnings := DetectImpacts(sessions)
	if len(warnings) != 0 {
		t.Errorf("expected 0 warnings, got %d: %+v", len(warnings), warnings)
	}
}

func TestDetectImpacts_SharedDependency_PackageJSON(t *testing.T) {
	t.Parallel()

	sessions := []SessionChanges{
		{
			Name:         "frontend",
			ChangedFiles: []string{"package.json", "src/App.tsx"},
		},
		{
			Name:         "backend",
			ChangedFiles: []string{"package.json", "server/index.ts"},
		},
	}

	warnings := DetectImpacts(sessions)

	found := findWarning(warnings, "shared-dependency", "package.json")
	if found == nil {
		t.Fatalf("expected shared-dependency warning for package.json, got warnings: %+v", warnings)
	}
	if found.Severity != SeverityHigh {
		t.Errorf("expected severity %q, got %q", SeverityHigh, found.Severity)
	}
	if found.SessionA != "frontend" || found.SessionB != "backend" {
		t.Errorf("unexpected sessions: A=%q B=%q", found.SessionA, found.SessionB)
	}
}

func TestDetectImpacts_SharedDependency_GoMod(t *testing.T) {
	t.Parallel()

	sessions := []SessionChanges{
		{
			Name:         "service-a",
			ChangedFiles: []string{"go.mod", "internal/handler.go"},
		},
		{
			Name:         "service-b",
			ChangedFiles: []string{"go.mod", "go.sum", "cmd/main.go"},
		},
	}

	warnings := DetectImpacts(sessions)

	goModWarning := findWarning(warnings, "shared-dependency", "go.mod")
	if goModWarning == nil {
		t.Fatalf("expected shared-dependency warning for go.mod, got warnings: %+v", warnings)
	}
	if goModWarning.Severity != SeverityHigh {
		t.Errorf("expected severity %q, got %q", SeverityHigh, goModWarning.Severity)
	}
}

func TestDetectImpacts_SharedMonorepoFile(t *testing.T) {
	t.Parallel()

	sessions := []SessionChanges{
		{
			Name:         "app-one",
			ChangedFiles: []string{"packages/shared/utils.ts", "apps/web/page.tsx"},
		},
		{
			Name:         "app-two",
			ChangedFiles: []string{"packages/shared/types.ts", "apps/mobile/screen.tsx"},
		},
	}

	warnings := DetectImpacts(sessions)

	found := findWarning(warnings, "shared-file", "packages/shared")
	if found == nil {
		t.Fatalf("expected shared-file warning for packages/shared, got warnings: %+v", warnings)
	}
	if found.Severity != SeverityMedium {
		t.Errorf("expected severity %q, got %q", SeverityMedium, found.Severity)
	}
}

func TestDetectImpacts_SharedMonorepoFile_Libs(t *testing.T) {
	t.Parallel()

	sessions := []SessionChanges{
		{
			Name:         "feature-x",
			ChangedFiles: []string{"libs/auth/middleware.go"},
		},
		{
			Name:         "feature-y",
			ChangedFiles: []string{"libs/db/pool.go"},
		},
	}

	warnings := DetectImpacts(sessions)

	found := findWarning(warnings, "shared-file", "libs")
	if found == nil {
		t.Fatalf("expected shared-file warning for libs, got warnings: %+v", warnings)
	}
}

func TestDetectImpacts_APIContract(t *testing.T) {
	t.Parallel()

	sessions := []SessionChanges{
		{
			Name:         "team-a",
			ChangedFiles: []string{"src/api/users.ts", "src/components/UserList.tsx"},
		},
		{
			Name:         "team-b",
			ChangedFiles: []string{"src/api/auth.ts", "src/pages/Login.tsx"},
		},
	}

	warnings := DetectImpacts(sessions)

	found := findWarning(warnings, "api-change", "api")
	if found == nil {
		t.Fatalf("expected api-change warning for api, got warnings: %+v", warnings)
	}
	if found.Severity != SeverityMedium {
		t.Errorf("expected severity %q, got %q", SeverityMedium, found.Severity)
	}
}

func TestDetectImpacts_APIContract_Routes(t *testing.T) {
	t.Parallel()

	sessions := []SessionChanges{
		{
			Name:         "svc-a",
			ChangedFiles: []string{"server/routes/v1/users.go"},
		},
		{
			Name:         "svc-b",
			ChangedFiles: []string{"server/routes/v1/products.go"},
		},
	}

	warnings := DetectImpacts(sessions)

	found := findWarning(warnings, "api-change", "routes")
	if found == nil {
		t.Fatalf("expected api-change warning for routes, got warnings: %+v", warnings)
	}
}

func TestDetectImpacts_APIContract_Types(t *testing.T) {
	t.Parallel()

	sessions := []SessionChanges{
		{
			Name:         "svc-a",
			ChangedFiles: []string{"internal/types/user.go"},
		},
		{
			Name:         "svc-b",
			ChangedFiles: []string{"internal/types/product.go"},
		},
	}

	warnings := DetectImpacts(sessions)

	found := findWarning(warnings, "api-change", "types")
	if found == nil {
		t.Fatalf("expected api-change warning for types, got warnings: %+v", warnings)
	}
}

func TestDetectImpacts_MultipleConflictTypes(t *testing.T) {
	t.Parallel()

	sessions := []SessionChanges{
		{
			Name:         "session-one",
			ChangedFiles: []string{"package.json", "packages/shared/config.ts", "src/api/index.ts"},
		},
		{
			Name:         "session-two",
			ChangedFiles: []string{"package.json", "packages/shared/logger.ts", "src/api/auth.ts"},
		},
	}

	warnings := DetectImpacts(sessions)

	depWarning := findWarning(warnings, "shared-dependency", "package.json")
	sharedWarning := findWarning(warnings, "shared-file", "packages/shared")
	apiWarning := findWarning(warnings, "api-change", "api")

	if depWarning == nil {
		t.Error("expected shared-dependency warning for package.json")
	}
	if sharedWarning == nil {
		t.Error("expected shared-file warning for packages/shared")
	}
	if apiWarning == nil {
		t.Error("expected api-change warning for api")
	}

	if len(warnings) < 3 {
		t.Errorf("expected at least 3 warnings, got %d: %+v", len(warnings), warnings)
	}
}

func TestDetectImpacts_ThreeSessions(t *testing.T) {
	t.Parallel()

	sessions := []SessionChanges{
		{Name: "a", ChangedFiles: []string{"go.mod"}},
		{Name: "b", ChangedFiles: []string{"go.mod"}},
		{Name: "c", ChangedFiles: []string{"go.mod"}},
	}

	warnings := DetectImpacts(sessions)

	// Three pairs: (a,b), (a,c), (b,c) — each should produce a go.mod warning.
	if len(warnings) != 3 {
		t.Errorf("expected 3 warnings for 3 pairs, got %d: %+v", len(warnings), warnings)
	}
}

func TestDetectImpacts_UniqueIDs(t *testing.T) {
	t.Parallel()

	sessions := []SessionChanges{
		{
			Name:         "s1",
			ChangedFiles: []string{"package.json", "packages/shared/a.ts", "src/api/x.ts"},
		},
		{
			Name:         "s2",
			ChangedFiles: []string{"package.json", "packages/shared/b.ts", "src/api/y.ts"},
		},
	}

	warnings := DetectImpacts(sessions)

	ids := map[string]bool{}
	for _, w := range warnings {
		if ids[w.ID] {
			t.Errorf("duplicate warning ID: %s", w.ID)
		}
		ids[w.ID] = true
	}
}

func TestMatchingSharedSegment(t *testing.T) {
	t.Parallel()

	tests := []struct {
		file string
		want string
	}{
		{"packages/shared/utils.ts", "packages/shared"},
		{"packages/common/types.ts", "packages/common"},
		{"libs/auth/middleware.go", "libs"},
		{"shared/config.json", "shared"},
		{"common/helpers.ts", "common"},
		{"src/components/Button.tsx", ""},
		{"internal/handler.go", ""},
	}

	for _, tt := range tests {
		t.Run(tt.file, func(t *testing.T) {
			t.Parallel()
			got := matchingSharedSegment(tt.file)
			if got != tt.want {
				t.Errorf("matchingSharedSegment(%q) = %q, want %q", tt.file, got, tt.want)
			}
		})
	}
}

func TestMatchingAPISegment(t *testing.T) {
	t.Parallel()

	tests := []struct {
		file string
		want string
	}{
		{"src/api/users.ts", "api"},
		{"server/routes/v1/users.go", "routes"},
		{"internal/types/user.go", "types"},
		{"src/components/Button.tsx", ""},
		{"cmd/main.go", ""},
	}

	for _, tt := range tests {
		t.Run(tt.file, func(t *testing.T) {
			t.Parallel()
			got := matchingAPISegment(tt.file)
			if got != tt.want {
				t.Errorf("matchingAPISegment(%q) = %q, want %q", tt.file, got, tt.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Test helpers
// ---------------------------------------------------------------------------

// findWarning searches for a warning matching the given conflictType and
// details substring.
func findWarning(warnings []ImpactWarning, conflictType, details string) *ImpactWarning {
	for i := range warnings {
		if warnings[i].ConflictType == conflictType && warnings[i].Details == details {
			return &warnings[i]
		}
	}
	return nil
}
