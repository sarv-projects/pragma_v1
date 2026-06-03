package pipeline

import (
	"encoding/json"
	"time"

	"github.com/sarv-projects/pragma/internal/budget"
)

type Event interface {
	eventTag()
}

type PhaseChangedEvent struct {
	From Phase
	To   Phase
}

type FileCompletedEvent struct {
	Path        string
	Duration    time.Duration
	Healed      bool
	Failed      bool
	Description string
}

type BudgetUpdatedEvent struct {
	Status budget.Status
}

type LogEvent struct {
	Level   string
	Message string
}

type InterviewMessageEvent struct {
	Role    string
	Content string
}

type SpecReadyEvent struct {
	Spec      json.RawMessage
	FileCount int
	TestCount int
}

type DAGReadyEvent struct {
	DAG        json.RawMessage
	SliceCount int
	EstSeconds int
	EstCost    float64
	Slices     [][]string // file paths per parallel slice
}

type CoverageReportEvent struct {
	Passed int
	Total  int
	Issues []string
}

type ErrorEvent struct {
	Err   error
	Fatal bool
}

type RunCompleteEvent struct {
	ProjectName string
	OutputPath  string
	FileCount   int
	Healed      int
	Failed      int
	TotalCost   float64
	BudgetLeft  float64
	Coverage    int
	Manifest    json.RawMessage
	Spec        json.RawMessage
}

func (PhaseChangedEvent) eventTag()     {}
func (FileCompletedEvent) eventTag()    {}
func (BudgetUpdatedEvent) eventTag()    {}
func (LogEvent) eventTag()              {}
func (InterviewMessageEvent) eventTag() {}
func (SpecReadyEvent) eventTag()        {}
func (DAGReadyEvent) eventTag()         {}
func (CoverageReportEvent) eventTag()   {}
func (ErrorEvent) eventTag()            {}
func (RunCompleteEvent) eventTag()      {}

type SecurityAuditEvent struct {
	Warnings []string
}

func (SecurityAuditEvent) eventTag() {}

type SpecAmendmentProposedEvent struct {
	FilePath string
	Reason   string
}

func (SpecAmendmentProposedEvent) eventTag() {}

type TestRunEvent struct {
	Command string
	Passed  bool
	Output  string
}

func (TestRunEvent) eventTag() {}

type RuntimeValidationErrorEvent struct {
	Message string
	Logs    string
}

func (RuntimeValidationErrorEvent) eventTag() {}

type RuntimeValidationPassedEvent struct{}

func (RuntimeValidationPassedEvent) eventTag() {}

// SpecProgressEvent is emitted during spec compilation to show partial progress.
type SpecProgressEvent struct {
	Pass    int    // 1, 2, or 3
	Status  string // "started", "completed", "error"
	Message string // Human-readable status
}

func (SpecProgressEvent) eventTag() {}

// QueuedMessageEvent notifies the frontend that a message was queued during generation.
type QueuedMessageEvent struct {
	Content string
}

func (QueuedMessageEvent) eventTag() {}
