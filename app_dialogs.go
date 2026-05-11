package main

import (
	"github.com/wailsapp/wails/v2/pkg/runtime"
)

// BrowseForDirectory opens a native folder picker dialog and returns the
// user's selection as an absolute path. Returns "" if the user cancels or
// an error occurs.
//
// Used by Settings → Behavior → Scanner roots Browse button (TASK-021)
// and Settings → Advanced → dotClaudeSource Browse (TASK-023, which will
// reuse this binding rather than duplicate it).
func (a *App) BrowseForDirectory(title string) string {
	path, err := runtime.OpenDirectoryDialog(a.ctx, runtime.OpenDialogOptions{
		Title:                      title,
		DefaultDirectory:           "",
		TreatPackagesAsDirectories: false,
	})
	if err != nil || path == "" {
		return ""
	}
	return path
}
