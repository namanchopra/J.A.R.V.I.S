package agent

import (
	"testing"
)

// ---------------------------------------------------------------------------
// detectCycle tests
// ---------------------------------------------------------------------------

func TestDetectCycle_NoCycle(t *testing.T) {
	t.Parallel()

	// A -> B -> C (linear chain, no cycle)
	graph := map[string][]string{
		"A": {"B"},
		"B": {"C"},
		"C": {},
	}

	if detectCycle(graph, "A") {
		t.Error("detectCycle reported a cycle in a linear chain")
	}
}

func TestDetectCycle_SelfLoop(t *testing.T) {
	t.Parallel()

	graph := map[string][]string{
		"A": {"A"},
	}

	if !detectCycle(graph, "A") {
		t.Error("detectCycle missed a self-loop")
	}
}

func TestDetectCycle_DirectCycle(t *testing.T) {
	t.Parallel()

	// A -> B -> A
	graph := map[string][]string{
		"A": {"B"},
		"B": {"A"},
	}

	if !detectCycle(graph, "A") {
		t.Error("detectCycle missed a direct cycle A -> B -> A")
	}
}

func TestDetectCycle_IndirectCycle(t *testing.T) {
	t.Parallel()

	// A -> B -> C -> A
	graph := map[string][]string{
		"A": {"B"},
		"B": {"C"},
		"C": {"A"},
	}

	if !detectCycle(graph, "A") {
		t.Error("detectCycle missed an indirect cycle A -> B -> C -> A")
	}
}

func TestDetectCycle_DiamondNoCycle(t *testing.T) {
	t.Parallel()

	// D depends on B and C; both B and C depend on A. Diamond shape, no cycle.
	graph := map[string][]string{
		"D": {"B", "C"},
		"B": {"A"},
		"C": {"A"},
		"A": {},
	}

	if detectCycle(graph, "D") {
		t.Error("detectCycle reported a cycle in a diamond-shaped DAG")
	}
}

func TestDetectCycle_ComplexWithCycle(t *testing.T) {
	t.Parallel()

	// A -> B -> C -> D -> B (cycle at B -> C -> D -> B)
	graph := map[string][]string{
		"A": {"B"},
		"B": {"C"},
		"C": {"D"},
		"D": {"B"},
	}

	if !detectCycle(graph, "A") {
		t.Error("detectCycle missed a complex cycle A -> B -> C -> D -> B")
	}
}

func TestDetectCycle_EmptyGraph(t *testing.T) {
	t.Parallel()

	graph := map[string][]string{
		"A": {},
	}

	if detectCycle(graph, "A") {
		t.Error("detectCycle reported a cycle in an empty graph")
	}
}

func TestDetectCycle_UnknownDeps(t *testing.T) {
	t.Parallel()

	// A depends on "X" which is not in the graph. Should not panic or
	// report a cycle.
	graph := map[string][]string{
		"A": {"X"},
	}

	if detectCycle(graph, "A") {
		t.Error("detectCycle reported a cycle when dependency is not in graph")
	}
}

func TestDetectCycle_MultipleBranches(t *testing.T) {
	t.Parallel()

	// E depends on C and D. C depends on A. D depends on B. A depends on nothing.
	// B depends on nothing. No cycle.
	graph := map[string][]string{
		"E": {"C", "D"},
		"C": {"A"},
		"D": {"B"},
		"A": {},
		"B": {},
	}

	if detectCycle(graph, "E") {
		t.Error("detectCycle reported a cycle in a multi-branch DAG")
	}
}
