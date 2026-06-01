package pipeline

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

type RunSummary struct {
	RunID       string     `json:"run_id"`
	ProjectName string     `json:"project_name"`
	Phase       Phase      `json:"phase"`
	PausedAt    *time.Time `json:"paused_at"`
}

func SaveCheckpoint(state RunState, dir string) error {
	runDir := filepath.Join(dir, state.RunID)
	if err := os.MkdirAll(runDir, 0755); err != nil {
		return err
	}

	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}

	chkPath := filepath.Join(runDir, "checkpoint.json")
	tmpPath := chkPath + ".tmp"

	if err := os.WriteFile(tmpPath, data, 0644); err != nil {
		return err
	}
	return os.Rename(tmpPath, chkPath)
}

func LoadCheckpoint(runID string, dir string) (*RunState, error) {
	chkPath := filepath.Join(dir, runID, "checkpoint.json")
	data, err := os.ReadFile(chkPath)
	if err != nil {
		return nil, err
	}

	var state RunState
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, err
	}
	return &state, nil
}

func ListRuns(dir string) ([]RunSummary, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var runs []RunSummary
	for _, e := range entries {
		if e.IsDir() {
			if state, err := LoadCheckpoint(e.Name(), dir); err == nil {
				runs = append(runs, RunSummary{
					RunID:       state.RunID,
					ProjectName: state.ProjectName,
					Phase:       state.Phase,
					PausedAt:    state.PausedAt,
				})
			}
		}
	}
	return runs, nil
}
