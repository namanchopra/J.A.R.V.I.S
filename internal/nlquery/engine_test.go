package nlquery

import (
	"errors"
	"testing"
)

// ---------------------------------------------------------------------------
// Helpers to build test callbacks
// ---------------------------------------------------------------------------

func testCallbacks() Callbacks {
	return Callbacks{
		GetIndicators: func() ([]interface{}, error) {
			return []interface{}{
				map[string]interface{}{
					"pid":         1001,
					"name":        "frontend-refactor",
					"hasQuestion": false,
				},
				map[string]interface{}{
					"pid":         1002,
					"name":        "api-bugfix",
					"hasQuestion": true,
				},
				map[string]interface{}{
					"pid":         1003,
					"name":        "docs-update",
					"hasQuestion": true,
				},
			}, nil
		},
		GetTotalSpend: func() (interface{}, error) {
			return map[string]float64{
				"allTime":   12.50,
				"thisMonth": 4.20,
				"today":     1.10,
			}, nil
		},
		GetRecordings: func() ([]interface{}, error) {
			return []interface{}{
				map[string]interface{}{"id": "rec-1", "name": "session-1"},
				map[string]interface{}{"id": "rec-2", "name": "session-2"},
			}, nil
		},
		StopSession: func(pid int) error {
			return nil
		},
		BroadcastAll: func(cmd string) (map[int]string, error) {
			return map[int]string{1001: "ok", 1002: "ok"}, nil
		},
	}
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

func TestIdleSessions(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		query string
	}{
		{"idle keyword", "show idle sessions"},
		{"waiting keyword", "what's waiting"},
		{"blocked keyword", "any blocked sessions?"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			res := Execute(tt.query, testCallbacks())
			if res.Action != ActionQuery {
				t.Fatalf("expected ActionQuery, got %s", res.Action)
			}
			data, ok := res.Data.([]interface{})
			if !ok {
				t.Fatalf("expected []interface{} data, got %T", res.Data)
			}
			// Two sessions have hasQuestion=true.
			if len(data) != 2 {
				t.Fatalf("expected 2 idle sessions, got %d", len(data))
			}
		})
	}
}

func TestActiveSessions(t *testing.T) {
	t.Parallel()

	res := Execute("show running sessions", testCallbacks())
	if res.Action != ActionQuery {
		t.Fatalf("expected ActionQuery, got %s", res.Action)
	}
	data, ok := res.Data.([]interface{})
	if !ok {
		t.Fatalf("expected []interface{} data, got %T", res.Data)
	}
	// One session has hasQuestion=false.
	if len(data) != 1 {
		t.Fatalf("expected 1 active session, got %d", len(data))
	}
}

func TestTotalSpend(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		query string
	}{
		{"cost", "how much does it cost"},
		{"spent", "how much have I spent"},
		{"money", "show me the money"},
		{"tokens", "tokens used"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			res := Execute(tt.query, testCallbacks())
			if res.Action != ActionQuery {
				t.Fatalf("expected ActionQuery, got %s", res.Action)
			}
			if res.Data == nil {
				t.Fatal("expected spend data, got nil")
			}
		})
	}
}

func TestCountSessions(t *testing.T) {
	t.Parallel()

	res := Execute("how many sessions are there", testCallbacks())
	if res.Action != ActionQuery {
		t.Fatalf("expected ActionQuery, got %s", res.Action)
	}
	data, ok := res.Data.(map[string]int)
	if !ok {
		t.Fatalf("expected map[string]int, got %T", res.Data)
	}
	if data["total"] != 3 {
		t.Fatalf("expected 3 total, got %d", data["total"])
	}
	if data["active"] != 1 {
		t.Fatalf("expected 1 active, got %d", data["active"])
	}
	if data["idle"] != 2 {
		t.Fatalf("expected 2 idle, got %d", data["idle"])
	}
}

func TestRecordings(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		query string
	}{
		{"history", "show history"},
		{"recordings", "list recordings"},
		{"what did", "what did my sessions do"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			res := Execute(tt.query, testCallbacks())
			if res.Action != ActionQuery {
				t.Fatalf("expected ActionQuery, got %s", res.Action)
			}
			data, ok := res.Data.([]interface{})
			if !ok {
				t.Fatalf("expected []interface{} data, got %T", res.Data)
			}
			if len(data) != 2 {
				t.Fatalf("expected 2 recordings, got %d", len(data))
			}
		})
	}
}

func TestStopAll(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		query string
	}{
		{"stop all", "stop all sessions"},
		{"kill all", "kill all"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			res := Execute(tt.query, testCallbacks())
			if res.Action != ActionCommand {
				t.Fatalf("expected ActionCommand, got %s", res.Action)
			}
			if !res.NeedsConfirm {
				t.Fatal("expected NeedsConfirm=true")
			}
			data, ok := res.Data.(map[string]interface{})
			if !ok {
				t.Fatalf("expected map data, got %T", res.Data)
			}
			if data["action"] != "stop_all" {
				t.Fatalf("expected action=stop_all, got %v", data["action"])
			}
			pids, ok := data["pids"].([]int)
			if !ok {
				t.Fatalf("expected []int pids, got %T", data["pids"])
			}
			if len(pids) != 3 {
				t.Fatalf("expected 3 pids, got %d", len(pids))
			}
		})
	}
}

func TestStopByName(t *testing.T) {
	t.Parallel()

	res := Execute("stop api-bugfix", testCallbacks())
	if res.Action != ActionCommand {
		t.Fatalf("expected ActionCommand, got %s", res.Action)
	}
	if !res.NeedsConfirm {
		t.Fatal("expected NeedsConfirm=true")
	}
	data, ok := res.Data.(map[string]interface{})
	if !ok {
		t.Fatalf("expected map data, got %T", res.Data)
	}
	if data["pid"] != 1002 {
		t.Fatalf("expected pid=1002, got %v", data["pid"])
	}
}

func TestStopByPID(t *testing.T) {
	t.Parallel()

	res := Execute("stop 1003", testCallbacks())
	if res.Action != ActionCommand {
		t.Fatalf("expected ActionCommand, got %s", res.Action)
	}
	data, ok := res.Data.(map[string]interface{})
	if !ok {
		t.Fatalf("expected map data, got %T", res.Data)
	}
	if data["pid"] != 1003 {
		t.Fatalf("expected pid=1003, got %v", data["pid"])
	}
}

func TestStopNotFound(t *testing.T) {
	t.Parallel()

	res := Execute("stop nonexistent", testCallbacks())
	if res.Error == "" {
		t.Fatal("expected error for unknown session")
	}
}

func TestBroadcast(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		query   string
		wantCmd string
	}{
		{"broadcast", "broadcast update dependencies", "update dependencies"},
		{"send all", "send all run tests", "run tests"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			res := Execute(tt.query, testCallbacks())
			if res.Action != ActionCommand {
				t.Fatalf("expected ActionCommand, got %s", res.Action)
			}
			if !res.NeedsConfirm {
				t.Fatal("expected NeedsConfirm=true")
			}
			data, ok := res.Data.(map[string]interface{})
			if !ok {
				t.Fatalf("expected map data, got %T", res.Data)
			}
			if data["command"] != tt.wantCmd {
				t.Fatalf("expected command=%q, got %v", tt.wantCmd, data["command"])
			}
		})
	}
}

func TestBroadcastEmpty(t *testing.T) {
	t.Parallel()

	res := Execute("broadcast", testCallbacks())
	if res.Error == "" {
		t.Fatal("expected error for empty broadcast command")
	}
}

func TestUnknownQuery(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		query string
	}{
		{"gibberish", "asdfghjkl"},
		{"empty", ""},
		{"unrelated", "what is the meaning of life"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			res := Execute(tt.query, testCallbacks())
			if res.Action != ActionUnknown {
				t.Fatalf("expected ActionUnknown, got %s", res.Action)
			}
			if res.Error == "" {
				t.Fatal("expected non-empty error message")
			}
		})
	}
}

func TestNilCallback(t *testing.T) {
	t.Parallel()

	empty := Callbacks{}
	res := Execute("show idle sessions", empty)
	if res.Error == "" {
		t.Fatal("expected error when callback is nil")
	}
}

func TestCallbackError(t *testing.T) {
	t.Parallel()

	failing := Callbacks{
		GetIndicators: func() ([]interface{}, error) {
			return nil, errors.New("connection refused")
		},
	}
	res := Execute("show active sessions", failing)
	if res.Error == "" {
		t.Fatal("expected error to propagate")
	}
	if res.Action != ActionUnknown {
		t.Fatalf("expected ActionUnknown on error, got %s", res.Action)
	}
}
