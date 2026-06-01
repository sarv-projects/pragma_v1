package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/progress"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/sarv-projects/pragma/internal/pipeline"
)

type fileStatus struct {
	path     string
	healed   bool
	failed   bool
	duration time.Duration
}

type GeneratingModel struct {
	total     int
	completed int
	healed    int
	failed    int
	files     []fileStatus
	progress  progress.Model
	viewport  viewport.Model
	startedAt time.Time
	coverage  int
	width     int
	height    int
	ready     bool
}

func NewGeneratingModel() GeneratingModel {
	p := progress.New(progress.WithGradient("#8b7cf6", "#2dd4bf"))
	return GeneratingModel{progress: p, coverage: -1}
}

func (m *GeneratingModel) SetSize(w, h int) {
	m.width, m.height = w, h
	m.progress.Width = min(w-6, 60)
	vh := h - 10
	if vh < 3 {
		vh = 3
	}
	vw := w - 4
	if vw < 20 {
		vw = 20
	}
	if !m.ready {
		m.viewport = viewport.New(vw, vh)
		m.ready = true
	} else {
		m.viewport.Width, m.viewport.Height = vw, vh
	}
	m.renderList()
}

func (m GeneratingModel) Init() tea.Cmd { return nil }

func (m GeneratingModel) Update(msg tea.Msg) (GeneratingModel, tea.Cmd) {
	switch msg := msg.(type) {
	case pipeline.SpecReadyEvent:
		m.total = msg.FileCount
		if m.startedAt.IsZero() {
			m.startedAt = time.Now()
		}
		m.renderList()
	case pipeline.FileCompletedEvent:
		if m.startedAt.IsZero() {
			m.startedAt = time.Now()
		}
		fs := fileStatus{path: msg.Path, healed: msg.Healed, failed: msg.Failed, duration: msg.Duration}
		m.files = append(m.files, fs)
		m.completed++
		if msg.Healed {
			m.healed++
		}
		if msg.Failed {
			m.failed++
		}
		m.renderList()
		if m.total > 0 {
			return m, m.progress.SetPercent(float64(m.completed) / float64(m.total))
		}
	case pipeline.CoverageReportEvent:
		if msg.Total > 0 {
			m.coverage = msg.Passed * 100 / msg.Total
		}
	case progress.FrameMsg:
		pm, cmd := m.progress.Update(msg)
		m.progress = pm.(progress.Model)
		return m, cmd
	}
	return m, nil
}

func (m *GeneratingModel) renderList() {
	if !m.ready {
		return
	}
	var b strings.Builder
	// Show most recent files last; viewport scrolls to bottom.
	for _, f := range m.files {
		icon := StyleSuccess.Render("✓")
		if f.failed {
			icon = StyleError.Render("✗")
		} else if f.healed {
			icon = StyleWarning.Render("✚")
		}
		dur := StyleMuted.Render(fmt.Sprintf("(%.1fs)", f.duration.Seconds()))
		b.WriteString(fmt.Sprintf("%s %s %s\n", icon, f.path, dur))
	}
	m.viewport.SetContent(b.String())
	m.viewport.GotoBottom()
}

func (m GeneratingModel) eta() string {
	if m.completed == 0 || m.total == 0 || m.startedAt.IsZero() {
		return "—"
	}
	elapsed := time.Since(m.startedAt).Seconds()
	perFile := elapsed / float64(m.completed)
	remaining := float64(m.total-m.completed) * perFile
	if remaining < 0 {
		remaining = 0
	}
	return fmt.Sprintf("~%ds", int(remaining))
}

func (m GeneratingModel) speed() string {
	if m.startedAt.IsZero() || m.completed == 0 {
		return "—"
	}
	elapsed := time.Since(m.startedAt).Seconds()
	if elapsed < 0.5 {
		return "…"
	}
	return fmt.Sprintf("%.1f files/s", float64(m.completed)/elapsed)
}

func (m GeneratingModel) View() string {
	header := StyleTitle.Render("Generating")

	bar := m.progress.View()
	counts := fmt.Sprintf("%s %d/%d   %s %d healed   %s %d failed   %s %s   ETA %s",
		StyleSuccess.Render("done"), m.completed, m.total,
		StyleWarning.Render("✚"), m.healed,
		StyleError.Render("✗"), m.failed,
		StyleAccent.Render("speed"), m.speed(),
		m.eta(),
	)

	list := ""
	if m.ready {
		list = m.viewport.View()
	}
	w := min(m.width-2, 90)
	hint := keyHint([2]string{"↑/↓", "scroll"}, [2]string{"ctrl+c", "pause/quit"})
	return header + "\n" + bar + "\n" + counts + "\n" + divider(w) + "\n" + list + "\n" + hint
}
