package tui

import (
	"encoding/json"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/sarv-projects/pragma/internal/budget"
	"github.com/sarv-projects/pragma/internal/config"
	"github.com/sarv-projects/pragma/internal/pipeline"
)

func sampleSpec() json.RawMessage {
	spec := map[string]any{
		"files": []map[string]any{
			{"path": "app/config.py", "role": "config", "exports": []string{"Settings"}},
			{"path": "app/models/user.py", "role": "model", "depends_on": []string{"app/config.py"}, "exports": []string{"User"}},
			{"path": "app/routes/users.py", "role": "route", "depends_on": []string{"app/models/user.py"}, "public_api": []map[string]any{{"name": "get_user"}}},
		},
		"tests":        []map[string]any{{"path": "tests/test_users.py"}},
		"dependencies": []string{"fastapi>=0.115"},
	}
	b, _ := json.Marshal(spec)
	return b
}

// renderNoPanic asserts View() returns non-empty and doesn't panic.
func renderNoPanic(t *testing.T, name, view string) {
	t.Helper()
	if strings.TrimSpace(view) == "" {
		t.Errorf("%s rendered empty", name)
	}
}

func TestAllScreensRender(t *testing.T) {
	cfg, _ := config.Load("/nonexistent")
	oracle := budget.New(2.0, 0.25, t.TempDir()+"/b.json")

	// Home
	home := NewHomeModel(cfg, oracle)
	home.SetSize(100, 30)
	renderNoPanic(t, "home", home.View())

	// Onboarding (walk through steps)
	ob := NewOnboardingModel()
	ob.SetSize(100, 30)
	renderNoPanic(t, "onboarding", ob.View())
	ob, _ = ob.Update(tea.KeyMsg{Type: tea.KeyEnter}) // -> mode
	ob, _ = ob.Update(tea.KeyMsg{Type: tea.KeyEnter}) // -> key
	renderNoPanic(t, "onboarding-key", ob.View())

	// Interview
	iv := NewInterviewModel()
	iv.SetSize(100, 30)
	renderNoPanic(t, "interview", iv.View())

	// Profile select
	ps := NewProfileSelectModel("fastapi-async")
	ps.SetSize(100, 30)
	renderNoPanic(t, "profile", ps.View())

	// Working
	wk := NewWorkingModel()
	wk.SetSize(100, 30)
	wk.SetPhase(pipeline.PhaseCompilingSpec)
	renderNoPanic(t, "working", wk.View())

	// Spec review (with event)
	sr := NewSpecReviewModel()
	sr.SetSize(100, 30)
	sr, _ = sr.Update(pipeline.SpecReadyEvent{Spec: sampleSpec(), FileCount: 3, TestCount: 1})
	v := sr.View()
	renderNoPanic(t, "spec-review", v)
	if !strings.Contains(v, "users.py") {
		t.Errorf("spec review should show file tree with users.py")
	}

	// DAG approval (with slices)
	dag := NewDAGApprovalModel()
	dag.SetSize(100, 30)
	dag, _ = dag.Update(pipeline.DAGReadyEvent{
		SliceCount: 2, EstSeconds: 20,
		Slices: [][]string{{"app/config.py"}, {"app/models/user.py", "app/routes/users.py"}},
	})
	v = dag.View()
	renderNoPanic(t, "dag", v)
	if !strings.Contains(v, "config.py") {
		t.Errorf("dag should list files per slice")
	}

	// Generating (with progress)
	gen := NewGeneratingModel()
	gen.SetSize(100, 30)
	gen, _ = gen.Update(pipeline.SpecReadyEvent{FileCount: 3})
	gen, _ = gen.Update(pipeline.FileCompletedEvent{Path: "app/config.py"})
	v = gen.View()
	renderNoPanic(t, "generating", v)
	if !strings.Contains(v, "config.py") {
		t.Errorf("generating should show completed file list")
	}

	// Complete
	cp := NewCompleteModel()
	cp.SetSize(100, 30)
	cp, _ = cp.Update(pipeline.RunCompleteEvent{
		ProjectName: "demo", OutputPath: "/tmp/out", FileCount: 3, Coverage: 100,
	})
	v = cp.View()
	renderNoPanic(t, "complete", v)
	if !strings.Contains(v, "/tmp/out") {
		t.Errorf("complete should show output path")
	}

	// Settings
	st := NewSettingsModel(cfg)
	st.SetSize(100, 30)
	renderNoPanic(t, "settings", st.View())

	// Resume
	rs := NewResumeModel(cfg)
	rs.SetSize(100, 30)
	renderNoPanic(t, "resume", rs.View())
}

func TestQQuitsOnlyFromHome(t *testing.T) {
	cfg, _ := config.Load("/nonexistent")
	oracle := budget.New(2.0, 0.25, t.TempDir()+"/b.json")
	svc := pipeline.NewService(nil, oracle, cfg, make(chan pipeline.Event, 10))
	app := NewAppModel(oracle, svc, cfg)
	app.CurrentScreen = ScreenHome

	// q on Home quits.
	_, cmd := app.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")})
	if cmd == nil {
		t.Errorf("q on Home should quit")
	}

	// q on Settings must NOT quit (it's a normal keystroke).
	app.CurrentScreen = ScreenSettings
	_, cmd = app.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")})
	if cmd != nil {
		// cmd may be non-nil from settings delegation, but it must not be tea.Quit.
		msg := cmd()
		if _, ok := msg.(tea.QuitMsg); ok {
			t.Errorf("q on Settings must not quit the app")
		}
	}
}

func TestHelpOverlayToggle(t *testing.T) {
	cfg, _ := config.Load("/nonexistent")
	oracle := budget.New(2.0, 0.25, t.TempDir()+"/b.json")
	svc := pipeline.NewService(nil, oracle, cfg, make(chan pipeline.Event, 10))
	app := NewAppModel(oracle, svc, cfg)
	m, _ := app.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("?")})
	am := m.(AppModel)
	if !am.showHelp {
		t.Errorf("? should open help overlay")
	}
	if !strings.Contains(am.View(), "Keyboard Shortcuts") {
		t.Errorf("help overlay should render shortcuts")
	}
}
