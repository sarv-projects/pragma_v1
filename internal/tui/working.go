package tui

import (
	"fmt"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/sarv-projects/pragma/internal/pipeline"
)

type WorkingModel struct {
	spinner   spinner.Model
	phase     pipeline.Phase
	startedAt time.Time
	width     int
	height    int
}

func NewWorkingModel() WorkingModel {
	s := spinner.New()
	s.Spinner = spinner.Dot
	s.Style = lipgloss.NewStyle().Foreground(colBrandHi)
	return WorkingModel{spinner: s, phase: pipeline.PhaseResearching}
}

func (m *WorkingModel) SetSize(w, h int) { m.width, m.height = w, h }

func (m *WorkingModel) SetPhase(p pipeline.Phase) {
	if p != m.phase || m.startedAt.IsZero() {
		m.startedAt = time.Now() // reset the elapsed timer on each new phase
	}
	m.phase = p
}

func (m WorkingModel) Init() tea.Cmd { return m.spinner.Tick }
func (m WorkingModel) tick() tea.Cmd { return m.spinner.Tick }

func (m WorkingModel) Update(msg tea.Msg) (WorkingModel, tea.Cmd) {
	var cmd tea.Cmd
	m.spinner, cmd = m.spinner.Update(msg)
	return m, cmd
}

func (m WorkingModel) phaseLabel() (string, string) {
	switch m.phase {
	case pipeline.PhaseResearching:
		return "Researching", "Querying DeepWiki + DuckDuckGo for library patterns (free, ~3-8s)…"
	case pipeline.PhaseCompilingSpec:
		return "Compiling the Build Contract", "Reasoning over the architecture, then refining it. This can take 1-3 minutes — grab a coffee."
	default:
		return "Working", "Please wait…"
	}
}

func (m WorkingModel) elapsed() string {
	if m.startedAt.IsZero() {
		return ""
	}
	secs := int(time.Since(m.startedAt).Seconds())
	if secs < 60 {
		return fmt.Sprintf("%ds", secs)
	}
	return fmt.Sprintf("%dm%02ds", secs/60, secs%60)
}

func (m WorkingModel) View() string {
	title, detail := m.phaseLabel()
	header := m.spinner.View() + " " + StyleTitle.Render(title)
	if e := m.elapsed(); e != "" {
		header += "   " + StyleMuted.Render("elapsed "+e)
	}
	body := header + "\n\n" + StyleMuted.Render(detail)
	return StylePanel.Render(body)
}
