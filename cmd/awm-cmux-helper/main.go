// awm-cmux-helper is a standalone binary that executes CMux RPC commands.
// It runs as a separate process outside the Wails WebView sandbox.
// Jarvis spawns this binary instead of calling cmux directly.
package main

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
)

func main() {
	if len(os.Args) < 3 {
		fmt.Fprintf(os.Stderr, "Usage: awm-cmux-helper <method> <json-params>\n")
		os.Exit(1)
	}

	method := os.Args[1]
	params := os.Args[2]

	cmuxBin := "/Applications/cmux.app/Contents/Resources/bin/cmux"
	if _, err := os.Stat(cmuxBin); err != nil {
		// Try PATH
		if p, lookErr := exec.LookPath("cmux"); lookErr == nil {
			cmuxBin = p
		} else {
			fmt.Fprintf(os.Stderr, "cmux not found\n")
			os.Exit(1)
		}
	}

	cmd := exec.Command(cmuxBin, "rpc", method, params)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		// If RPC fails, try the non-RPC cmux command for simple operations
		if method == "open" {
			cmd2 := exec.Command("open", "-a", "cmux", strings.Trim(params, "\""))
			cmd2.Run()
		}
		os.Exit(1)
	}
}
