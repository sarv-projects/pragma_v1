package pipeline

import (
	"encoding/json"
	"time"
)

type Phase int

const (
	PhaseIdeation Phase = iota
	PhaseResearching
	PhaseCompilingSpec
	PhaseSpecReview
	PhaseDAGReview
	PhaseGenerating
	PhaseComplete
	PhasePaused
	PhaseFailed
)

func (p Phase) String() string {
	switch p {
	case PhaseIdeation:
		return "Ideation"
	case PhaseResearching:
		return "Researching"
	case PhaseCompilingSpec:
		return "CompilingSpec"
	case PhaseSpecReview:
		return "SpecReview"
	case PhaseDAGReview:
		return "DAGReview"
	case PhaseGenerating:
		return "Generating"
	case PhaseComplete:
		return "Complete"
	case PhasePaused:
		return "Paused"
	case PhaseFailed:
		return "Failed"
	default:
		return "Unknown"
	}
}

type RunState struct {
	RunID          string
	Phase          Phase
	ProjectName    string
	ProfileName    string
	Manifest       json.RawMessage
	Research       json.RawMessage
	Spec           json.RawMessage
	DAG            json.RawMessage
	SliceIndex     int
	FilesCompleted []string
	FilesRemaining []string
	FilesFailed    []string
	CostSoFar      float64
	PausedAt       *time.Time
}
