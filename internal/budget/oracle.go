package budget

import (
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"time"
)

// DeepSeek V4-Flash pricing (per token). Pragma is DeepSeek-only, so these
// rates apply to every call. Cached input is billed at the discounted rate.
const (
	inputCostPerToken  = 0.14 / 1_000_000.0
	cachedCostPerToken = 0.0028 / 1_000_000.0
	outputCostPerToken = 0.28 / 1_000_000.0
)

// Status represents the current state of the budget.
type Status struct {
	Mode         string     `json:"mode"`
	LifetimeCap  float64    `json:"lifetime_cap"`
	PerRunCap    float64    `json:"per_run_cap"`
	TotalSpent   float64    `json:"total_spent"`
	RunSpent     float64    `json:"run_spent"`
	RunsComplete int        `json:"runs_complete"`
	Runs         []RunEntry `json:"runs"`
}

type RunEntry struct {
	Timestamp string  `json:"timestamp"`
	Spent     float64 `json:"spent"`
}

// Oracle manages the spending limit.
type Oracle struct {
	mu           sync.Mutex
	lifetime     float64
	perRunCap    float64
	spent        float64
	runSpent     float64
	path         string
	runs         []RunEntry
	runsComplete int
}

// New initializes a new Oracle and loads the existing budget.json if present.
func New(lifetimeCap, perRunCap float64, persistPath string) *Oracle {
	o := &Oracle{
		lifetime:  lifetimeCap,
		perRunCap: perRunCap,
		path:      persistPath,
	}

	data, err := os.ReadFile(persistPath)
	if err == nil {
		var s Status
		if err := json.Unmarshal(data, &s); err == nil {
			o.spent = s.TotalSpent
			o.runs = s.Runs
			o.runsComplete = s.RunsComplete
			// We do not reload runSpent as it's per-run.
		}
	}
	return o
}

// CanSpend reports whether the estimated cost of the output tokens still fits
// within both the lifetime cap and the per-run cap.
func (o *Oracle) CanSpend(estimatedOutputTokens int) bool {
	o.mu.Lock()
	defer o.mu.Unlock()

	cost := float64(estimatedOutputTokens) * outputCostPerToken
	return o.spent+cost <= o.lifetime && o.runSpent+cost <= o.perRunCap
}

// Record adds the actual cost of a completed API call to both the lifetime and
// per-run totals, then persists. Cached input tokens are billed at the cheaper
// cached rate; the remainder is billed at the standard input rate.
func (o *Oracle) Record(inputTokens, outputTokens, cachedInputTokens int) {
	o.mu.Lock()
	defer o.mu.Unlock()

	freshInput := inputTokens - cachedInputTokens
	if freshInput < 0 {
		freshInput = 0
	}
	inputCost := float64(freshInput)*inputCostPerToken + float64(cachedInputTokens)*cachedCostPerToken
	outputCost := float64(outputTokens) * outputCostPerToken

	total := inputCost + outputCost
	o.spent += total
	o.runSpent += total
	o.persist()
}

// ResetRun zeroes the runSpent counter at the start of a new run.
func (o *Oracle) ResetRun() {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.runSpent = 0
}

func (o *Oracle) RecordRunCompletion() {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.runs = append(o.runs, RunEntry{
		Timestamp: time.Now().Format(time.RFC3339),
		Spent:     o.runSpent,
	})
	o.runsComplete++
	o.persist()
}


// Status returns a snapshot of the current budget. Mode is always "fast"
// (DeepSeek direct API) since that is the only provider Pragma supports.
func (o *Oracle) Status() Status {
	o.mu.Lock()
	defer o.mu.Unlock()
	return Status{
		Mode:         "fast",
		LifetimeCap:  o.lifetime,
		PerRunCap:    o.perRunCap,
		TotalSpent:   o.spent,
		RunSpent:     o.runSpent,
		RunsComplete: o.runsComplete,
		Runs:         o.runs,
	}
}

func (o *Oracle) persist() error {
	s := Status{
		LifetimeCap:  o.lifetime,
		PerRunCap:    o.perRunCap,
		TotalSpent:   o.spent,
		RunsComplete: o.runsComplete,
		Runs:         o.runs,
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}

	tmpFile := o.path + ".tmp"
	if err := os.WriteFile(tmpFile, data, 0644); err != nil {
		return err
	}

	if err := os.Rename(tmpFile, o.path); err != nil {
		return fmt.Errorf("failed to commit budget file: %w", err)
	}

	return nil
}
