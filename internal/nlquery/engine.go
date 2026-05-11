package nlquery

import (
	"fmt"
	"strconv"
	"strings"
)

// ActionType classifies the parsed intent as either a read-only query that
// returns data or a mutating command that requires user confirmation.
type ActionType string

const (
	ActionQuery   ActionType = "query"   // returns data
	ActionCommand ActionType = "command" // performs action (needs confirmation)
	ActionUnknown ActionType = "unknown"
)

// QueryResult is the unified response type for all parsed queries.
type QueryResult struct {
	Action       ActionType  `json:"action"`
	Intent       string      `json:"intent"`                // human-readable parsed intent
	Data         interface{} `json:"data"`                  // result data (for queries)
	NeedsConfirm bool       `json:"needsConfirm"`          // true for destructive actions
	Error        string      `json:"error,omitempty"`
}

// Callbacks provides access to Jarvis runtime data without importing app.go.
// Each function is optional -- if nil, the engine returns a helpful error
// when the user asks for something that requires it.
type Callbacks struct {
	GetIndicators func() ([]interface{}, error)
	GetTotalSpend func() (interface{}, error)
	GetRecordings func() ([]interface{}, error)
	StopSession   func(pid int) error
	BroadcastAll  func(cmd string) (map[int]string, error)
}

// Execute parses a natural language query string using keyword matching and
// returns a QueryResult. No LLM is involved -- this is pure string matching.
func Execute(query string, cb Callbacks) QueryResult {
	q := strings.ToLower(strings.TrimSpace(query))

	if q == "" {
		return unknownResult()
	}

	// Order matters: check more specific patterns before generic ones.

	// --- "stop all" / "kill all" -----------------------------------------------
	if contains(q, "stop all", "kill all") {
		return stopAll(q, cb)
	}

	// --- "stop <name>" ---------------------------------------------------------
	if hasPrefix(q, "stop ", "kill ") {
		return stopByName(q, cb)
	}

	// --- "broadcast" / "send all" ----------------------------------------------
	if contains(q, "broadcast", "send all") {
		return broadcast(q, cb)
	}

	// --- idle / waiting / blocked sessions -------------------------------------
	if contains(q, "idle", "waiting", "blocked") {
		return filterIndicators(q, cb, true, "idle/waiting sessions")
	}

	// --- running / active sessions ---------------------------------------------
	if contains(q, "running", "active") {
		return filterIndicators(q, cb, false, "running/active sessions")
	}

	// --- cost / spend / money / tokens -----------------------------------------
	if contains(q, "cost", "spent", "spend", "money", "tokens") {
		return totalSpend(cb)
	}

	// --- history / recordings --------------------------------------------------
	if contains(q, "history", "recordings", "what did") {
		return recordings(cb)
	}

	// --- how many / count ------------------------------------------------------
	if contains(q, "how many", "count") {
		return countSessions(cb)
	}

	return unknownResult()
}

// ---------------------------------------------------------------------------
// Intent handlers
// ---------------------------------------------------------------------------

func filterIndicators(q string, cb Callbacks, wantIdle bool, label string) QueryResult {
	if cb.GetIndicators == nil {
		return errResult("GetIndicators callback is not configured")
	}

	indicators, err := cb.GetIndicators()
	if err != nil {
		return errResult(fmt.Sprintf("failed to get indicators: %v", err))
	}

	var matched []interface{}
	for _, ind := range indicators {
		m, ok := toMap(ind)
		if !ok {
			continue
		}
		hasQ, _ := m["hasQuestion"].(bool)
		if wantIdle && hasQ {
			matched = append(matched, ind)
		} else if !wantIdle && !hasQ {
			matched = append(matched, ind)
		}
	}

	intent := fmt.Sprintf("Showing %d %s (out of %d total)", len(matched), label, len(indicators))
	return QueryResult{
		Action: ActionQuery,
		Intent: intent,
		Data:   matched,
	}
}

func totalSpend(cb Callbacks) QueryResult {
	if cb.GetTotalSpend == nil {
		return errResult("GetTotalSpend callback is not configured")
	}

	spend, err := cb.GetTotalSpend()
	if err != nil {
		return errResult(fmt.Sprintf("failed to get spend: %v", err))
	}

	return QueryResult{
		Action: ActionQuery,
		Intent: "Total spend across all sessions",
		Data:   spend,
	}
}

func recordings(cb Callbacks) QueryResult {
	if cb.GetRecordings == nil {
		return errResult("GetRecordings callback is not configured")
	}

	recs, err := cb.GetRecordings()
	if err != nil {
		return errResult(fmt.Sprintf("failed to get recordings: %v", err))
	}

	intent := fmt.Sprintf("Found %d recording(s)", len(recs))
	return QueryResult{
		Action: ActionQuery,
		Intent: intent,
		Data:   recs,
	}
}

func countSessions(cb Callbacks) QueryResult {
	if cb.GetIndicators == nil {
		return errResult("GetIndicators callback is not configured")
	}

	indicators, err := cb.GetIndicators()
	if err != nil {
		return errResult(fmt.Sprintf("failed to get indicators: %v", err))
	}

	idle := 0
	active := 0
	for _, ind := range indicators {
		m, ok := toMap(ind)
		if !ok {
			continue
		}
		if hasQ, _ := m["hasQuestion"].(bool); hasQ {
			idle++
		} else {
			active++
		}
	}

	intent := fmt.Sprintf("%d session(s) total: %d active, %d idle/waiting", len(indicators), active, idle)
	return QueryResult{
		Action: ActionQuery,
		Intent: intent,
		Data: map[string]int{
			"total":  len(indicators),
			"active": active,
			"idle":   idle,
		},
	}
}

func stopAll(_ string, cb Callbacks) QueryResult {
	if cb.GetIndicators == nil {
		return errResult("GetIndicators callback is not configured")
	}

	indicators, err := cb.GetIndicators()
	if err != nil {
		return errResult(fmt.Sprintf("failed to get indicators: %v", err))
	}

	pids := extractPIDs(indicators)
	intent := fmt.Sprintf("Stop all %d session(s)", len(pids))
	return QueryResult{
		Action:       ActionCommand,
		Intent:       intent,
		Data:         map[string]interface{}{"action": "stop_all", "pids": pids},
		NeedsConfirm: true,
	}
}

func stopByName(q string, cb Callbacks) QueryResult {
	if cb.GetIndicators == nil {
		return errResult("GetIndicators callback is not configured")
	}

	// Extract the target name: strip the leading "stop " or "kill " prefix.
	target := q
	for _, prefix := range []string{"stop ", "kill "} {
		if strings.HasPrefix(q, prefix) {
			target = strings.TrimSpace(q[len(prefix):])
			break
		}
	}

	if target == "" {
		return errResult("please specify a session name or PID to stop")
	}

	indicators, err := cb.GetIndicators()
	if err != nil {
		return errResult(fmt.Sprintf("failed to get indicators: %v", err))
	}

	// Try matching by name (substring) or exact PID.
	for _, ind := range indicators {
		m, ok := toMap(ind)
		if !ok {
			continue
		}

		name, _ := m["name"].(string)
		pidF, _ := m["pid"].(float64) // JSON numbers decode as float64
		pid := int(pidF)

		// Also try the raw int form in case the callback returns native types.
		if pid == 0 {
			if pidInt, ok := m["pid"].(int); ok {
				pid = pidInt
			}
		}

		nameLower := strings.ToLower(name)
		targetPID, _ := strconv.Atoi(target)

		if strings.Contains(nameLower, target) || (targetPID != 0 && pid == targetPID) {
			intent := fmt.Sprintf("Stop session %q (PID %d)", name, pid)
			return QueryResult{
				Action:       ActionCommand,
				Intent:       intent,
				Data:         map[string]interface{}{"action": "stop", "pid": pid, "name": name},
				NeedsConfirm: true,
			}
		}
	}

	return errResult(fmt.Sprintf("no session found matching %q", target))
}

func broadcast(q string, cb Callbacks) QueryResult {
	// Extract the command text after the keyword.
	cmd := ""
	for _, kw := range []string{"broadcast ", "send all "} {
		if idx := strings.Index(q, kw); idx != -1 {
			cmd = strings.TrimSpace(q[idx+len(kw):])
			break
		}
	}

	if cmd == "" {
		return errResult("please specify a command to broadcast, e.g. \"broadcast update dependencies\"")
	}

	intent := fmt.Sprintf("Broadcast %q to all sessions", cmd)
	return QueryResult{
		Action:       ActionCommand,
		Intent:       intent,
		Data:         map[string]interface{}{"action": "broadcast", "command": cmd},
		NeedsConfirm: true,
	}
}

func unknownResult() QueryResult {
	return QueryResult{
		Action: ActionUnknown,
		Intent: "Unrecognized query",
		Error: strings.Join([]string{
			"I didn't understand that. Try one of these:",
			"  - \"show idle sessions\" or \"what's waiting\"",
			"  - \"show active sessions\" or \"what's running\"",
			"  - \"how much have I spent\" or \"total cost\"",
			"  - \"how many sessions\"",
			"  - \"stop <session-name>\" or \"stop all\"",
			"  - \"broadcast <command>\"",
			"  - \"show history\" or \"recordings\"",
		}, "\n"),
	}
}

func errResult(msg string) QueryResult {
	return QueryResult{
		Action: ActionUnknown,
		Intent: "Error",
		Error:  msg,
	}
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// contains returns true if q contains any of the given substrings.
func contains(q string, substrs ...string) bool {
	for _, s := range substrs {
		if strings.Contains(q, s) {
			return true
		}
	}
	return false
}

// hasPrefix returns true if q starts with any of the given prefixes.
func hasPrefix(q string, prefixes ...string) bool {
	for _, p := range prefixes {
		if strings.HasPrefix(q, p) {
			return true
		}
	}
	return false
}

// toMap attempts a type assertion to map[string]interface{}. This handles the
// common case where callbacks return JSON-decoded data or struct-to-map
// converted values.
func toMap(v interface{}) (map[string]interface{}, bool) {
	m, ok := v.(map[string]interface{})
	return m, ok
}

// extractPIDs pulls PID values out of a slice of indicator maps.
func extractPIDs(indicators []interface{}) []int {
	var pids []int
	for _, ind := range indicators {
		m, ok := toMap(ind)
		if !ok {
			continue
		}
		if pidF, ok := m["pid"].(float64); ok {
			pids = append(pids, int(pidF))
		} else if pidInt, ok := m["pid"].(int); ok {
			pids = append(pids, pidInt)
		}
	}
	return pids
}
