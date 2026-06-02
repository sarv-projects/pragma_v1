package pipeline

import (
	"encoding/json"
	"testing"
)

func TestPlanSlicesTopological(t *testing.T) {
	spec := map[string]any{
		"files": []map[string]any{
			{"path": "config.py", "depends_on": []string{}},
			{"path": "models.py", "depends_on": []string{"config.py"}},
			{"path": "routes.py", "depends_on": []string{"models.py", "config.py"}},
		},
		"tests": []map[string]any{{"path": "tests/test_routes.py"}},
	}
	raw, _ := json.Marshal(spec)
	s := &Service{}
	slices, total, testCount := s.planSlices(raw)

	if total != 4 {
		t.Fatalf("total files = %d, want 4", total)
	}
	if testCount != 1 {
		t.Errorf("test count = %d, want 1", testCount)
	}
	if len(slices) != 3 {
		t.Fatalf("slices = %d, want 3", len(slices))
	}
	if slices[0][0].Path != "config.py" && slices[0][1].Path != "config.py" {
		t.Errorf("first slice should contain config.py")
	}
	if slices[2][0].Path != "routes.py" {
		t.Errorf("last slice should be routes.py, got %s", slices[2][0].Path)
	}
	// Full contract must be carried through (not stripped to path/depends_on).
	if len(slices[0][0].Contract) == 0 {
		t.Errorf("file contract should be preserved for codegen")
	}
}

func TestEstimateTokensBounds(t *testing.T) {
	if got := estimateTokens(0, 0); got < 800 {
		t.Errorf("estimate floor not applied: %d", got)
	}
	if got := estimateTokens(100, 1_000_000); got > 8000 {
		t.Errorf("estimate ceiling not applied: %d", got)
	}
}

func TestDeriveProjectName(t *testing.T) {
	if got := deriveProjectName(`{"project_name":"task-api"}`); got != "task-api" {
		t.Errorf("got %q", got)
	}
	if got := deriveProjectName(`{"description":"a thing"}`); got != "a-thing" {
		t.Errorf("got %q", got)
	}
	if got := deriveProjectName(`not json`); got != "Pragma Project" {
		t.Errorf("fallback failed: %q", got)
	}
}

func TestRuneSafeTruncate(t *testing.T) {
	s := "héllo wörld" // multibyte runes
	for n := 0; n <= len(s); n++ {
		out := runeSafeTruncate(s, n)
		if len(out) > n {
			t.Fatalf("truncate(%d) returned longer string", n)
		}
		// Must remain valid UTF-8 (no split runes).
		if !validUTF8(out) {
			t.Fatalf("truncate(%d)=%q split a rune", n, out)
		}
	}
}

func validUTF8(s string) bool {
	for _, r := range s {
		if r == 0xFFFD {
			return false
		}
	}
	return true
}

func TestRemoveString(t *testing.T) {
	in := []string{"a", "b", "c"}
	out := removeString(in, "b")
	if len(out) != 2 || out[0] != "a" || out[1] != "c" {
		t.Errorf("removeString = %v", out)
	}
}
