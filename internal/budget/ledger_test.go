package budget

import (
	"math"
	"os"
	"path/filepath"
	"testing"
)

func approxEqual(a, b float64) bool {
	return math.Abs(a-b) < 1e-9
}

func TestLedgerRecordPhase(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "ledger.json")
	l := NewLedger(path)

	l.RecordPhase("research", 0.01)
	l.RecordPhase("generation", 0.05)

	if !approxEqual(l.LifetimeCost, 0.06) {
		t.Errorf("expected LifetimeCost~0.06, got %f", l.LifetimeCost)
	}
	if len(l.Phases) != 2 {
		t.Errorf("expected 2 phases, got %d", len(l.Phases))
	}

	// Verify persistence
	if _, err := os.Stat(path); os.IsNotExist(err) {
		t.Fatal("ledger file was not persisted")
	}

	// Reload and verify
	l2 := NewLedger(path)
	if !approxEqual(l2.LifetimeCost, 0.06) {
		t.Errorf("reloaded LifetimeCost: expected ~0.06, got %f", l2.LifetimeCost)
	}
}

func TestLedgerRecordProject(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "ledger.json")
	l := NewLedger(path)

	l.RecordProject("run-1", "MyApp", 0.10)
	l.RecordProject("run-2", "OtherApp", 0.20)

	if len(l.Projects) != 2 {
		t.Fatalf("expected 2 projects, got %d", len(l.Projects))
	}
	if !approxEqual(l.RollingAverage, 0.15) {
		t.Errorf("expected rolling average ~0.15, got %f", l.RollingAverage)
	}
}

func TestLedgerSummary(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "ledger.json")
	l := NewLedger(path)

	l.RecordPhase("test", 0.01)
	summary := l.Summary()

	cost, ok := summary["lifetime_cost"].(float64)
	if !ok || !approxEqual(cost, 0.01) {
		t.Errorf("expected lifetime_cost~0.01, got %v", summary["lifetime_cost"])
	}
}
