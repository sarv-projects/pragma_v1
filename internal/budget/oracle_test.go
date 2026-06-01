package budget

import (
	"path/filepath"
	"testing"
)

func TestOracle(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "budget.json")

	o := New(2.0, 0.25, path)

	// Base state
	if !o.CanSpend(100) {
		t.Errorf("Should be able to spend $0.000028")
	}

	// Spend 1M input, 1M output tokens ($0.14 + $0.28 = $0.42)
	o.Record(1000000, 1000000, 0)
	status := o.Status()

	diff := status.TotalSpent - 0.42
	if diff < -0.0001 || diff > 0.0001 {
		t.Errorf("Expected $0.42 spent, got %f", status.TotalSpent)
	}

	// Test reload persistence
	o2 := New(2.0, 0.25, path)
	diff2 := o2.Status().TotalSpent - 0.42
	if diff2 < -0.0001 || diff2 > 0.0001 {
		t.Errorf("Expected persistence to reload $0.42 spent, got %f", o2.Status().TotalSpent)
	}
}
