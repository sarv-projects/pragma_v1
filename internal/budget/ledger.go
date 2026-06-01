package budget

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// PhaseCost records the cost for a single phase execution.
type PhaseCost struct {
	Phase string  `json:"phase"`
	Cost  float64 `json:"cost"`
}

// ProjectCost records the total cost for a completed project run.
type ProjectCost struct {
	RunID       string    `json:"run_id"`
	ProjectName string    `json:"project_name"`
	TotalCost   float64   `json:"total_cost"`
	Timestamp   time.Time `json:"timestamp"`
}

// Ledger tracks fine-grained cost breakdowns per phase and per project.
type Ledger struct {
	mu             sync.Mutex
	path           string
	Phases         []PhaseCost   `json:"phases"`
	Projects       []ProjectCost `json:"projects"`
	LifetimeCost   float64       `json:"lifetime_cost"`
	RollingAverage float64       `json:"rolling_average"`
}

// NewLedger creates or loads a Ledger from the given path.
func NewLedger(path string) *Ledger {
	l := &Ledger{path: path}
	l.load()
	return l
}

// RecordPhase records a cost incurred during a specific phase.
func (l *Ledger) RecordPhase(phase string, cost float64) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.Phases = append(l.Phases, PhaseCost{Phase: phase, Cost: cost})
	// Cap phases at 1000 entries; trim to most recent 500
	if len(l.Phases) > 1000 {
		l.Phases = l.Phases[len(l.Phases)-500:]
	}
	l.LifetimeCost += cost
	l.updateRollingAverage()
	l.persist()
}

// RecordProject records the total cost for a completed project.
func (l *Ledger) RecordProject(runID, name string, cost float64) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.Projects = append(l.Projects, ProjectCost{
		RunID:       runID,
		ProjectName: name,
		TotalCost:   cost,
		Timestamp:   time.Now(),
	})
	// Cap projects at 200 entries; trim to most recent 100
	if len(l.Projects) > 200 {
		l.Projects = l.Projects[len(l.Projects)-100:]
	}
	l.updateRollingAverage()
	l.persist()
}

// Summary returns a map suitable for JSON serialization of the ledger state.
// It returns copies of slices to prevent data races when the caller accesses them.
func (l *Ledger) Summary() map[string]any {
	l.mu.Lock()
	defer l.mu.Unlock()
	
	// Copy slices to prevent data races
	phases := make([]PhaseCost, len(l.Phases))
	copy(phases, l.Phases)
	
	projects := make([]ProjectCost, len(l.Projects))
	copy(projects, l.Projects)
	
	return map[string]any{
		"phases":          phases,
		"projects":        projects,
		"lifetime_cost":   l.LifetimeCost,
		"rolling_average": l.RollingAverage,
	}
}

func (l *Ledger) updateRollingAverage() {
	if len(l.Projects) == 0 {
		l.RollingAverage = 0
		return
	}
	total := 0.0
	for _, p := range l.Projects {
		total += p.TotalCost
	}
	l.RollingAverage = total / float64(len(l.Projects))
}

func (l *Ledger) persist() {
	data, err := json.MarshalIndent(l, "", "  ")
	if err != nil {
		return
	}
	dir := filepath.Dir(l.path)
	_ = os.MkdirAll(dir, 0755)
	tmpPath := l.path + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0644); err != nil {
		return
	}
	_ = os.Rename(tmpPath, l.path)
}

func (l *Ledger) load() {
	data, err := os.ReadFile(l.path)
	if err != nil {
		return
	}
	_ = json.Unmarshal(data, l)
}
