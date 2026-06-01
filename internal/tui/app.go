package tui

import (
	"context"
	"fmt"
	"os"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/sarv-projects/pragma/internal/budget"
	"github.com/sarv-projects/pragma/internal/config"
	"github.com/sarv-projects/pragma/internal/keyvault"
	"github.com/sarv-projects/pragma/internal/pipeline"
)

type Screen int

const (
	ScreenHome Screen = iota
	ScreenOnboarding
	ScreenInterview
	ScreenProfileSelect
	ScreenWorking
	ScreenSpecReview
	ScreenDAGApproval
	ScreenGenerating
	ScreenComplete
	ScreenSettings
	ScreenResume
)

// screenChangeMsg switches between screens.
type screenChangeMsg Screen

// startRunMsg carries the manifest + chosen profile into the pipeline.
type startRunMsg struct {
	manifest string
	profile  string
}

type AppModel struct {
	CurrentScreen Screen
	prevScreen    Screen
	Oracle        *budget.Oracle
	Service       *pipeline.Service
	cfg           *config.Config

	HomeModel        HomeModel
	OnboardingModel  OnboardingModel
	InterviewModel   InterviewModel
	ProfileModel     ProfileSelectModel
	WorkingModel     WorkingModel
	SpecReviewModel  SpecReviewModel
	DAGApprovalModel DAGApprovalModel
	GeneratingModel  GeneratingModel
	CompleteModel    CompleteModel
	SettingsModel    SettingsModel
	ResumeModel      ResumeModel

	phase     pipeline.Phase
	showHelp  bool
	fatalMsg  string
	manifest  string
	runCancel context.CancelFunc

	Width  int
	Height int
}

func NewAppModel(oracle *budget.Oracle, service *pipeline.Service, cfg *config.Config) AppModel {
	interview := NewInterviewModel()
	interview.SetClient(service.Client())

	start := ScreenHome
	// First-run detection (F2): if no API key is configured anywhere, onboard.
	if !keyvault.HasAnyKey(os.LookupEnv) {
		start = ScreenOnboarding
	}

	return AppModel{
		CurrentScreen:    start,
		prevScreen:       ScreenHome,
		Oracle:           oracle,
		Service:          service,
		cfg:              cfg,
		HomeModel:        NewHomeModel(cfg, oracle),
		OnboardingModel:  NewOnboardingModel(),
		InterviewModel:   interview,
		ProfileModel:     NewProfileSelectModel(cfg.Profile),
		WorkingModel:     NewWorkingModel(),
		SpecReviewModel:  NewSpecReviewModel(),
		DAGApprovalModel: NewDAGApprovalModel(),
		GeneratingModel:  NewGeneratingModel(),
		CompleteModel:    NewCompleteModel(),
		SettingsModel:    NewSettingsModel(cfg),
		ResumeModel:      NewResumeModel(cfg),
	}
}

func (m AppModel) Init() tea.Cmd {
	return tea.Batch(m.WorkingModel.Init(), tea.EnterAltScreen)
}

func (m AppModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.Width = msg.Width
		m.Height = msg.Height
		m.propagateSize()
		return m, nil

	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c":
			// Universal quit (and would save a checkpoint mid-run since the
			// pipeline persists state continuously).
			return m, tea.Quit
		case "?":
			m.showHelp = !m.showHelp
			return m, nil
		case "esc":
			if m.showHelp {
				m.showHelp = false
				return m, nil
			}
			// Back to previous screen (16.4). Active gates/generation are not
			// interrupted by esc.
			if m.CurrentScreen != ScreenHome &&
				m.CurrentScreen != ScreenGenerating {
				m.CurrentScreen = ScreenHome
				return m, nil
			}
		case "q":
			// q quits ONLY from Home (F1). Elsewhere it's a normal keystroke.
			if m.CurrentScreen == ScreenHome {
				return m, tea.Quit
			}
		}

	case InterviewDoneMsg:
		m.manifest = msg.Manifest
		m.ProfileModel = NewProfileSelectModel(m.cfg.Profile)
		m.ProfileModel.SetSize(m.Width, m.Height)
		m.CurrentScreen = ScreenProfileSelect
		return m, nil

	case profileChosenMsg:
		// Persist the chosen profile as the new default for next time.
		m.cfg.Profile = msg.profile
		_ = m.cfg.Save(config.DefaultPath())
		return m, func() tea.Msg {
			return startRunMsg{manifest: m.manifest, profile: msg.profile}
		}

	case startRunMsg:
		m.CurrentScreen = ScreenWorking
		m.WorkingModel.SetPhase(pipeline.PhaseResearching)
		runCtx, cancel := context.WithCancel(context.Background())
		m.runCancel = cancel
		manifest, profile := msg.manifest, msg.profile
		go m.Service.StartRun(runCtx, manifest, profile)
		return m, m.WorkingModel.Init()

	case cancelRunMsg:
		if m.runCancel != nil {
			m.runCancel()
			m.runCancel = nil
		}
		m.CurrentScreen = ScreenHome
		return m, nil

	case screenChangeMsg:
		m.prevScreen = m.CurrentScreen
		m.CurrentScreen = Screen(msg)
		switch m.CurrentScreen {
		case ScreenResume:
			m.ResumeModel = NewResumeModel(m.cfg)
			m.ResumeModel.SetSize(m.Width, m.Height)
		case ScreenInterview:
			// Fresh interview each time; fire the first question.
			m.InterviewModel = NewInterviewModel()
			m.InterviewModel.SetClient(m.Service.Client())
			m.InterviewModel.SetSize(m.Width, m.Height)
			return m, m.InterviewModel.Init()
		}
		return m, nil

	case resumeRunMsg:
		state, err := pipeline.LoadCheckpoint(msg.runID, m.cfg.Output.Directory)
		if err != nil || state == nil || len(state.Spec) == 0 {
			// Nothing resumable (no compiled spec yet) — return home.
			m.fatalMsg = "That run can't be resumed (no compiled spec saved). Start a new project."
			m.CurrentScreen = ScreenHome
			return m, nil
		}
		m.fatalMsg = ""
		m.CurrentScreen = ScreenGenerating
		runCtx, cancel := context.WithCancel(context.Background())
		m.runCancel = cancel
		st := *state
		go m.Service.Resume(runCtx, st)
		return m, nil

	case pipeline.PhaseChangedEvent:
		m.phase = msg.To
		switch msg.To {
		case pipeline.PhaseResearching, pipeline.PhaseCompilingSpec:
			m.WorkingModel.SetPhase(msg.To)
			m.CurrentScreen = ScreenWorking
		case pipeline.PhaseSpecReview:
			m.CurrentScreen = ScreenSpecReview
		case pipeline.PhaseDAGReview:
			m.CurrentScreen = ScreenDAGApproval
		case pipeline.PhaseGenerating:
			m.CurrentScreen = ScreenGenerating
		case pipeline.PhaseComplete:
			m.CurrentScreen = ScreenComplete
		}
		// Let the working spinner keep ticking.
		return m, m.WorkingModel.tick()

	case pipeline.SpecReadyEvent:
		m.SpecReviewModel, _ = m.SpecReviewModel.Update(msg)
		var gcmd tea.Cmd
		m.GeneratingModel, gcmd = m.GeneratingModel.Update(msg) // seed total file count
		return m, gcmd

	case pipeline.DAGReadyEvent:
		m.DAGApprovalModel, _ = m.DAGApprovalModel.Update(msg)
		return m, nil

	case pipeline.FileCompletedEvent:
		var gcmd tea.Cmd
		m.GeneratingModel, gcmd = m.GeneratingModel.Update(msg)
		return m, gcmd

	case pipeline.CoverageReportEvent:
		m.CompleteModel, _ = m.CompleteModel.Update(msg)
		m.GeneratingModel, _ = m.GeneratingModel.Update(msg)
		return m, nil

	case pipeline.RunCompleteEvent:
		m.CompleteModel, _ = m.CompleteModel.Update(msg)
		m.CurrentScreen = ScreenComplete
		return m, nil

	case pipeline.ErrorEvent:
		if msg.Fatal {
			m.fatalMsg = msg.Err.Error()
		}
		return m, nil

	case ApproveSpecMsg:
		m.Service.ApproveSpec()
		return m, nil
	case ApproveDAGMsg:
		m.Service.ApproveDAG()
		return m, nil
	}

	// Delegate to the active screen.
	var cmd tea.Cmd
	switch m.CurrentScreen {
	case ScreenHome:
		m.HomeModel, cmd = m.HomeModel.Update(msg)
	case ScreenOnboarding:
		m.OnboardingModel, cmd = m.OnboardingModel.Update(msg)
	case ScreenInterview:
		m.InterviewModel, cmd = m.InterviewModel.Update(msg)
	case ScreenProfileSelect:
		m.ProfileModel, cmd = m.ProfileModel.Update(msg)
	case ScreenWorking:
		m.WorkingModel, cmd = m.WorkingModel.Update(msg)
	case ScreenSpecReview:
		m.SpecReviewModel, cmd = m.SpecReviewModel.Update(msg)
	case ScreenDAGApproval:
		m.DAGApprovalModel, cmd = m.DAGApprovalModel.Update(msg)
	case ScreenGenerating:
		m.GeneratingModel, cmd = m.GeneratingModel.Update(msg)
	case ScreenComplete:
		m.CompleteModel, cmd = m.CompleteModel.Update(msg)
	case ScreenSettings:
		m.SettingsModel, cmd = m.SettingsModel.Update(msg)
	case ScreenResume:
		m.ResumeModel, cmd = m.ResumeModel.Update(msg)
	}
	return m, cmd
}

func (m *AppModel) propagateSize() {
	m.HomeModel.SetSize(m.Width, m.Height)
	m.OnboardingModel.SetSize(m.Width, m.Height)
	m.InterviewModel.SetSize(m.Width, m.Height)
	m.ProfileModel.SetSize(m.Width, m.Height)
	m.WorkingModel.SetSize(m.Width, m.Height)
	m.SpecReviewModel.SetSize(m.Width, m.Height)
	m.DAGApprovalModel.SetSize(m.Width, m.Height)
	m.GeneratingModel.SetSize(m.Width, m.Height)
	m.CompleteModel.SetSize(m.Width, m.Height)
	m.SettingsModel.SetSize(m.Width, m.Height)
	m.ResumeModel.SetSize(m.Width, m.Height)
}

func (m AppModel) View() string {
	if m.showHelp {
		return m.helpView()
	}

	var view string
	switch m.CurrentScreen {
	case ScreenHome:
		view = m.HomeModel.View()
	case ScreenOnboarding:
		view = m.OnboardingModel.View()
	case ScreenInterview:
		view = m.InterviewModel.View()
	case ScreenProfileSelect:
		view = m.ProfileModel.View()
	case ScreenWorking:
		view = m.WorkingModel.View()
	case ScreenSpecReview:
		view = m.SpecReviewModel.View()
	case ScreenDAGApproval:
		view = m.DAGApprovalModel.View()
	case ScreenGenerating:
		view = m.GeneratingModel.View()
	case ScreenComplete:
		view = m.CompleteModel.View()
	case ScreenSettings:
		view = m.SettingsModel.View()
	case ScreenResume:
		view = m.ResumeModel.View()
	}

	parts := []string{view}
	if m.fatalMsg != "" {
		parts = append(parts, StyleError.Render("✖ "+m.fatalMsg))
	}
	// Home and Onboarding don't show the status bar (16.3).
	if m.CurrentScreen != ScreenHome && m.CurrentScreen != ScreenOnboarding {
		parts = append(parts, m.statusBar())
	}
	return strings.Join(parts, "\n")
}

func (m AppModel) statusBar() string {
	st := m.Oracle.Status()
	phase := m.phase.String()

	cost := fmt.Sprintf("Cost: $%.3f · Budget: $%.2f left", st.RunSpent, st.LifetimeCap-st.TotalSpent)

	left := StyleChipBrand.Render(" PRAGMA ")
	mid := StyleStatusBar.Render(fmt.Sprintf(" Phase: %s ", phase))
	right := StyleStatusBar.Render(fmt.Sprintf(" %s · ? help · ^C quit ", cost))

	bar := lipgloss.JoinHorizontal(lipgloss.Top, left, mid, right)
	if m.Width > 0 {
		return lipgloss.NewStyle().Width(m.Width).Render(bar)
	}
	return bar
}

func (m AppModel) helpView() string {
	rows := [][2]string{
		{"↑/↓ or j/k", "move selection"},
		{"enter", "select / approve"},
		{"esc", "back to previous screen"},
		{"q", "quit (from Home)"},
		{"ctrl+c", "quit / pause run anywhere"},
		{"a / r", "approve / regenerate (review gates)"},
		{"?", "toggle this help"},
	}
	var b strings.Builder
	b.WriteString(StyleTitle.Render("Keyboard Shortcuts") + "\n\n")
	for _, r := range rows {
		b.WriteString("  " + StyleAccent.Render(lipgloss.NewStyle().Width(16).Render(r[0])) + " " + StyleMuted.Render(r[1]) + "\n")
	}
	b.WriteString("\n" + StyleMuted.Render("Press ? or esc to close."))
	return StylePanel.Render(b.String())
}
