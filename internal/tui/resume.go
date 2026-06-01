package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/sarv-projects/pragma/internal/config"
	"github.com/sarv-projects/pragma/internal/pipeline"
)

// resumeRunMsg asks the app to resume the selected run.
type resumeRunMsg struct{ runID string }

type ResumeModel struct {
	runs   []pipeline.RunSummary
	cursor int
	cfg    *config.Config
	width  int
	height int
}

func NewResumeModel(cfg *config.Config) ResumeModel {
	runs, _ := pipeline.ListRuns(cfg.Output.Directory)
	return ResumeModel{runs: runs, cfg: cfg}
}

func (m *ResumeModel) SetSize(w, h int) { m.width, m.height = w, h }

func (m ResumeModel) Init() tea.Cmd { return nil }

func (m ResumeModel) Update(msg tea.Msg) (ResumeModel, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "up", "k":
			if m.cursor > 0 {
				m.cursor--
			}
		case "down", "j":
			if m.cursor < len(m.runs)-1 {
				m.cursor++
			}
		case "enter":
			if len(m.runs) > 0 {
				runID := m.runs[m.cursor].RunID
				return m, func() tea.Msg { return resumeRunMsg{runID: runID} }
			}
		}
	}
	return m, nil
}

func (m ResumeModel) View() string {
	var b strings.Builder
	b.WriteString(StyleTitle.Render("Resume a Run") + "\n\n")

	if len(m.runs) == 0 {
		b.WriteString(StyleMuted.Render("No previous runs found in "+m.cfg.Output.Directory+".") + "\n\n")
		b.WriteString(keyHint([2]string{"esc", "back"}))
		return StylePanel.Render(b.String())
	}

	for i, r := range m.runs {
		name := r.ProjectName
		if name == "" {
			name = r.RunID
		}
		line := fmt.Sprintf("%-28s %s", name, StyleMuted.Render(r.Phase.String()))
		if i == m.cursor {
			b.WriteString(StyleSelected.Render("  ❯ "+line) + "\n")
		} else {
			b.WriteString(StyleMuted.Render("    "+line) + "\n")
		}
	}

	b.WriteString("\n" + keyHint([2]string{"↑/↓", "move"}, [2]string{"enter", "resume"}, [2]string{"esc", "back"}))
	return StylePanel.Render(b.String())
}
