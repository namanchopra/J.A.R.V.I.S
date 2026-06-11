package cmux

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"
)

func base64Decode(s string) (string, error) {
	b, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

const defaultBinPath = "/Applications/cmux.app/Contents/Resources/bin/cmux"

// Client interfaces with CMux via its Unix socket (JSON-RPC v2) and CLI.
type Client struct {
	binPath        string
	socketPath     string
	socketPassword string
	rpcID          atomic.Int64
}

// Workspace represents a CMux workspace (tab group).
type Workspace struct {
	ID               string `json:"id"`
	Ref              string `json:"ref"`
	Title            string `json:"title"`
	CurrentDirectory string `json:"current_directory"`
	Index            int    `json:"index"`
	Selected         bool   `json:"selected"`
}

// Surface represents a CMux terminal tab.
type Surface struct {
	ID             string `json:"id"`
	Ref            string `json:"ref"`
	Title          string `json:"title"`
	TTY            string `json:"tty"`
	Type           string `json:"type"`
	PaneRef        string `json:"pane_ref"`
	Selected       bool   `json:"selected"`
	SelectedInPane bool   `json:"selected_in_pane"`
	Focused        bool   `json:"focused"`
	Index          int    `json:"index"`
}

// NewClient creates a CMux client. Returns nil if CMux is not installed.
func NewClient() *Client {
	binPath := defaultBinPath
	if _, err := exec.LookPath(binPath); err != nil {
		if p, err2 := exec.LookPath("cmux"); err2 == nil {
			binPath = p
		} else {
			return nil
		}
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}

	// Find the socket path.
	socketPath := filepath.Join(home, "Library", "Application Support", "cmux", "cmux.sock")
	if _, err := os.Stat(socketPath); err != nil {
		socketPath = ""
	}

	// Read socket password from CMux settings (JSONC).
	password := readSocketPassword(home)

	return &Client{binPath: binPath, socketPath: socketPath, socketPassword: password}
}

// readSocketPassword extracts the socketPassword from CMux's settings.json (JSONC).
func readSocketPassword(home string) string {
	paths := []string{
		filepath.Join(home, ".config", "cmux", "settings.json"),
		filepath.Join(home, "Library", "Application Support", "com.cmuxterm.app", "settings.json"),
	}
	for _, p := range paths {
		data, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		// Simple extraction: find "socketPassword" : "value" in the file.
		const key = `"socketPassword"`
		idx := bytes.Index(data, []byte(key))
		if idx < 0 {
			continue
		}
		// Find the value after the colon.
		rest := data[idx+len(key):]
		// Skip whitespace and colon.
		for len(rest) > 0 && (rest[0] == ' ' || rest[0] == '\t' || rest[0] == ':') {
			rest = rest[1:]
		}
		if len(rest) == 0 || rest[0] != '"' {
			continue
		}
		// Extract quoted string.
		rest = rest[1:] // skip opening quote
		end := bytes.IndexByte(rest, '"')
		if end < 0 {
			continue
		}
		pw := string(rest[:end])
		if pw != "" {
			return pw
		}
	}
	return os.Getenv("CMUX_SOCKET_PASSWORD")
}

// IsAvailable returns true if the CMux binary exists.
func (c *Client) IsAvailable() bool {
	return c != nil
}

// rpcRequest is sent to CMux over the Unix socket.
type rpcRequest struct {
	JSONRPC string      `json:"jsonrpc"`
	ID      int64       `json:"id"`
	Method  string      `json:"method"`
	Params  interface{} `json:"params"`
}

// rpcResponse is the CMux socket response format: {id, ok, result, error?}.
type rpcResponse struct {
	ID     int64           `json:"id"`
	OK     bool            `json:"ok"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  json.RawMessage `json:"error,omitempty"`
}

// rpc calls a CMux RPC method via direct Unix socket connection.
// Connects directly to the CMux socket from Go, bypassing the cmux CLI.
// This avoids macOS sandbox issues that block child processes from accessing
// the socket when launched from a Wails .app bundle.
func (c *Client) rpc(method string, params interface{}) (json.RawMessage, error) {
	if c == nil {
		return nil, fmt.Errorf("cmux not available")
	}
	if c.socketPath == "" {
		return nil, fmt.Errorf("cmux socket path not set")
	}

	conn, err := net.Dial("unix", c.socketPath)
	if err != nil {
		return nil, fmt.Errorf("cmux rpc %s: connect: %w", method, err)
	}
	defer conn.Close()

	conn.SetDeadline(time.Now().Add(5 * time.Second))

	// Authenticate if a socket password is configured. CMux's default
	// socketControlMode is "cmuxOnly" which blocks non-CMux processes.
	// Setting it to "password" allows any process with the correct password.
	if c.socketPassword != "" {
		authCmd := fmt.Sprintf("auth %s\n", c.socketPassword)
		if _, err := conn.Write([]byte(authCmd)); err != nil {
			return nil, fmt.Errorf("cmux rpc %s: auth write: %w", method, err)
		}
		// Read auth response (e.g. "OK: Authenticated" or "ERROR: ...").
		authBuf := make([]byte, 512)
		n, err := conn.Read(authBuf)
		if err != nil {
			return nil, fmt.Errorf("cmux rpc %s: auth read: %w", method, err)
		}
		authResp := strings.TrimSpace(string(authBuf[:n]))
		if !strings.HasPrefix(authResp, "OK") {
			return nil, fmt.Errorf("cmux rpc %s: auth failed: %s", method, authResp)
		}
	}

	req := rpcRequest{
		JSONRPC: "2.0",
		ID:      c.rpcID.Add(1),
		Method:  method,
		Params:  params,
	}

	reqBytes, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("cmux rpc %s: marshal: %w", method, err)
	}

	// Send request with newline delimiter.
	reqBytes = append(reqBytes, '\n')
	if _, err := conn.Write(reqBytes); err != nil {
		return nil, fmt.Errorf("cmux rpc %s: write: %w", method, err)
	}

	// Read response until we get valid JSON.
	var buf bytes.Buffer
	tmp := make([]byte, 16384)
	for {
		n, readErr := conn.Read(tmp)
		if n > 0 {
			buf.Write(tmp[:n])
			if json.Valid(bytes.TrimSpace(buf.Bytes())) {
				break
			}
		}
		if readErr != nil {
			if buf.Len() > 0 {
				break
			}
			return nil, fmt.Errorf("cmux rpc %s: read: %w", method, readErr)
		}
	}

	var resp rpcResponse
	if err := json.Unmarshal(bytes.TrimSpace(buf.Bytes()), &resp); err != nil {
		return nil, fmt.Errorf("cmux rpc %s: decode: %w (raw: %.200s)", method, err, buf.String())
	}

	if !resp.OK {
		// Error can be a string or {"code":"...","message":"..."}.
		var errObj struct {
			Message string `json:"message"`
		}
		if json.Unmarshal(resp.Error, &errObj) == nil && errObj.Message != "" {
			return nil, fmt.Errorf("cmux rpc %s: %s", method, errObj.Message)
		}
		return nil, fmt.Errorf("cmux rpc %s: %s", method, string(resp.Error))
	}

	return resp.Result, nil
}

// cliExec runs a cmux CLI command and returns stdout. Passes --password
// if a socket password is configured.
func (c *Client) cliExec(ctx context.Context, args ...string) ([]byte, error) {
	fullArgs := args
	if c.socketPassword != "" {
		fullArgs = append([]string{"--password", c.socketPassword}, args...)
	}
	cmd := exec.CommandContext(ctx, c.binPath, fullArgs...)
	setDetached(cmd)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		errMsg := strings.TrimSpace(stderr.String())
		if errMsg != "" {
			return nil, fmt.Errorf("cmux %s: %s", args[0], errMsg)
		}
		return nil, fmt.Errorf("cmux %s: %w", args[0], err)
	}
	return out, nil
}

// SurfaceLocation identifies a surface's position in the CMux hierarchy.
type SurfaceLocation struct {
	WorkspaceRef string
	WorkspaceID  string // UUID — needed for RPC calls
	SurfaceRef   string
	SurfaceID    string // UUID — needed for read_text
	TTY          string
}

// FindSurfaceByTTY searches all workspaces for a surface with the given TTY.
// Uses `cmux tree --all` for TTY mapping (not in RPC), then resolves UUIDs
// via RPC since surface.read_text requires UUIDs for cross-workspace reads.
func (c *Client) FindSurfaceByTTY(tty string) (*SurfaceLocation, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	out, err := c.cliExec(ctx, "tree", "--all")
	if err != nil {
		return nil, fmt.Errorf("find surface by tty: %w", err)
	}

	ttyShort := strings.TrimPrefix(tty, "/dev/")

	// Parse tree to find workspace ref + surface ref for the TTY.
	var wsRef, surfRef string
	var currentWorkspace string
	for _, line := range strings.Split(string(out), "\n") {
		if strings.Contains(line, "workspace ") {
			for _, p := range strings.Fields(line) {
				if strings.HasPrefix(p, "workspace:") {
					currentWorkspace = p
					break
				}
			}
		}
		if strings.Contains(line, "tty="+ttyShort) {
			for _, p := range strings.Fields(line) {
				if strings.HasPrefix(p, "surface:") {
					wsRef = currentWorkspace
					surfRef = p
					break
				}
			}
			if surfRef != "" {
				break
			}
		}
	}
	if surfRef == "" {
		return nil, fmt.Errorf("no surface found with tty %s", tty)
	}

	// Resolve UUIDs via RPC.
	loc := &SurfaceLocation{WorkspaceRef: wsRef, SurfaceRef: surfRef, TTY: ttyShort}

	// Workspace UUID.
	workspaces, err := c.ListWorkspaces()
	if err == nil {
		for _, ws := range workspaces {
			if ws.Ref == wsRef {
				loc.WorkspaceID = ws.ID
				break
			}
		}
	}

	// Surface UUID.
	if loc.WorkspaceID != "" {
		surfaces, err := c.ListSurfacesInWorkspace(loc.WorkspaceID)
		if err == nil {
			for _, s := range surfaces {
				if s.Ref == surfRef {
					loc.SurfaceID = s.ID
					break
				}
			}
		}
	}

	return loc, nil
}

// FocusSurfaceByTTY finds and focuses the surface matching the given TTY,
// switching workspace if needed.
func (c *Client) FocusSurfaceByTTY(tty string) error {
	loc, err := c.FindSurfaceByTTY(tty)
	if err != nil {
		return err
	}

	// Switch workspace.
	if _, err := c.rpc("workspace.select", map[string]interface{}{
		"workspace_id": loc.WorkspaceRef,
	}); err != nil {
		return fmt.Errorf("select workspace %s: %w", loc.WorkspaceRef, err)
	}

	time.Sleep(100 * time.Millisecond)

	// Focus the surface.
	if err := c.FocusSurface(loc.SurfaceRef); err != nil {
		return fmt.Errorf("focus surface %s: %w", loc.SurfaceRef, err)
	}

	return c.ActivateAndSwitchTab()
}

// ListWorkspaces returns all CMux workspaces.
func (c *Client) ListWorkspaces() ([]Workspace, error) {
	raw, err := c.rpc("workspace.list", map[string]interface{}{})
	if err != nil {
		return nil, err
	}

	var resp struct {
		Workspaces []Workspace `json:"workspaces"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil, fmt.Errorf("parse workspaces: %w", err)
	}
	return resp.Workspaces, nil
}

// ListSurfaces returns all CMux surfaces (terminal tabs) in the current workspace.
func (c *Client) ListSurfaces() ([]Surface, error) {
	return c.ListSurfacesInWorkspace("")
}

// ListSurfacesInWorkspace returns surfaces for a specific workspace (by UUID).
// If workspaceID is empty, returns surfaces for the current workspace.
func (c *Client) ListSurfacesInWorkspace(workspaceID string) ([]Surface, error) {
	params := map[string]interface{}{}
	if workspaceID != "" {
		params["workspace_id"] = workspaceID
	}
	raw, err := c.rpc("surface.list", params)
	if err != nil {
		return nil, err
	}

	var resp struct {
		Surfaces []Surface `json:"surfaces"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil, fmt.Errorf("parse surfaces: %w", err)
	}
	return resp.Surfaces, nil
}

// FindWorkspaceIDByCWD returns the workspace UUID matching the given CWD
// without switching to it.
func (c *Client) FindWorkspaceIDByCWD(cwd string) (string, error) {
	workspaces, err := c.ListWorkspaces()
	if err != nil {
		return "", fmt.Errorf("list workspaces: %w", err)
	}
	cwd = strings.TrimRight(cwd, "/")
	for _, ws := range workspaces {
		wsCWD := strings.TrimRight(ws.CurrentDirectory, "/")
		if wsCWD == cwd || strings.HasPrefix(cwd, wsCWD+"/") {
			return ws.ID, nil
		}
	}
	return "", fmt.Errorf("no CMux workspace found for %s", cwd)
}

// SendText sends text to a CMux terminal surface.
func (c *Client) SendText(surfaceRef, text string) error {
	_, err := c.rpc("surface.send_text", map[string]interface{}{
		"surface": surfaceRef,
		"text":    text,
	})
	return err
}

// ReadText reads the current text content from a CMux terminal surface.
// Accepts either a surface UUID (preferred for cross-workspace reads)
// or a surface ref like "surface:1" (only works for current workspace).
func (c *Client) ReadText(surfaceID string) (string, error) {
	raw, err := c.rpc("surface.read_text", map[string]interface{}{
		"surface_id": surfaceID,
	})
	if err != nil {
		return "", err
	}

	var resp struct {
		Text   string `json:"text"`
		Base64 string `json:"base64"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		return string(raw), nil
	}
	if resp.Base64 != "" {
		decoded, err := base64Decode(resp.Base64)
		if err == nil {
			return decoded, nil
		}
	}
	return resp.Text, nil
}

// FocusSurface focuses a CMux terminal surface.
func (c *Client) FocusSurface(surfaceRef string) error {
	_, err := c.rpc("surface.focus", map[string]interface{}{
		"surface_id": surfaceRef,
	})
	return err
}

// FocusWorkspace switches CMux to a specific workspace by UUID.
func (c *Client) FocusWorkspace(workspaceID string) error {
	_, err := c.rpc("workspace.select", map[string]interface{}{
		"workspace_id": workspaceID,
	})
	return err
}

// FocusWorkspaceByCWD finds a workspace whose current_directory matches the
// given path and switches to it. Returns an error if no matching workspace is found.
func (c *Client) FocusWorkspaceByCWD(cwd string) error {
	workspaces, err := c.ListWorkspaces()
	if err != nil {
		return fmt.Errorf("list workspaces: %w", err)
	}

	// Normalize the target path.
	cwd = strings.TrimRight(cwd, "/")

	for _, ws := range workspaces {
		wsCWD := strings.TrimRight(ws.CurrentDirectory, "/")
		// Exact match or the workspace CWD is a parent of the target.
		if wsCWD == cwd || strings.HasPrefix(cwd, wsCWD+"/") {
			return c.FocusWorkspace(ws.ID)
		}
	}

	return fmt.Errorf("no CMux workspace found for %s", cwd)
}

// FocusDirectory uses `open -a cmux <dir>` to open/focus a directory in CMux.
func (c *Client) FocusDirectory(dir string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "open", "-a", "cmux", dir)
	return cmd.Run()
}

// SendTextViaAppleScript sends text to the focused terminal in CMux using AppleScript.
// This bypasses the CMux socket entirely and works from any process, including
// the Wails WebView. Uses Ghostty's "text:" action via CMux's AppleScript API.
func (c *Client) SendTextViaAppleScript(text string) error {
	// Escape backslashes, quotes, and backticks for AppleScript.
	escaped := strings.ReplaceAll(text, `\`, `\\`)
	escaped = strings.ReplaceAll(escaped, `"`, `\"`)
	escaped = strings.ReplaceAll(escaped, "`", "'")

	script := fmt.Sprintf(`tell application "cmux"
    set t to focused terminal of selected tab of front window
    perform action "text:%s" on t
    perform action "text:\\x0d" on t
end tell`, escaped)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "osascript", "-e", script)
	return cmd.Run()
}

// ActivateAndSwitchTab brings CMux to the foreground and switches to the nth tab.
// Tab index is 1-based. Works via AppleScript — no socket needed.
func (c *Client) ActivateAndSwitchTab() error {
	script := `tell application "cmux" to activate`
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return exec.CommandContext(ctx, "osascript", "-e", script).Run()
}

// OpenDirectory opens a directory in CMux via RPC (creates a new workspace).
// The `cmux <path>` CLI form does not honour --password / socket auth, so we
// use the workspace.create RPC method instead.
func (c *Client) OpenDirectory(dir string) error {
	if c == nil {
		return fmt.Errorf("cmux not available")
	}
	_, err := c.rpc("workspace.create", map[string]interface{}{"path": dir})
	return err
}

// OpenWorkspace creates a new CMux workspace at the given directory and
// immediately starts the provided command in it. Uses `new-workspace --cwd
// --command` which is reliable, auth-aware, and requires no post-launch sleep.
// If command is empty, the workspace is opened without starting a command.
func (c *Client) OpenWorkspace(dir, command string) error {
	if c == nil {
		return fmt.Errorf("cmux not available")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	args := []string{"new-workspace", "--cwd", dir}
	if command != "" {
		args = append(args, "--command", command)
	}
	_, err := c.cliExec(ctx, args...)
	return err
}

