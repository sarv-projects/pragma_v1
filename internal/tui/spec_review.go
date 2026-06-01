package tui

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/sarv-projects/pragma/internal/pipeline"
)

type ApproveSpecMsg struct{}

// cancelRunMsg asks the app to cancel the in-flight run and return home.
type cancelRunMsg struct{}

type specFileRow struct {
	path    string
	role    string
	exports int
}

type SpecReviewModel struct {
	rows      []specFileRow
	fileCount int
	testCount int
	depCount  int
	viewport  viewport.Model
	width     int
	height    int
	ready     bool
}

func NewSpecReviewModel() SpecReviewModel { return SpecReviewModel{} }

func (m *SpecReviewModel) SetSize(w, h int) {
	m.width, m.height = w, h
	vh := h - 9
	if vh < 4 {
		vh = 4
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
	m.renderTree()
}

func (m SpecReviewModel) Init() tea.Cmd { return nil }

func (m SpecReviewModel) Update(msg tea.Msg) (SpecReviewModel, tea.Cmd) {
	switch msg := msg.(type) {
	case pipeline.SpecReadyEvent:
		m.parseSpec(msg.Spec)
		m.fileCount = msg.FileCount
		m.testCount = msg.TestCount
		m.renderTree()
	case tea.KeyMsg:
		switch msg.String() {
		case "enter", "a":
			return m, func() tea.Msg { return ApproveSpecMsg{} }
		case "r":
			return m, func() tea.Msg { return cancelRunMsg{} }
		}
		var cmd tea.Cmd
		m.viewport, cmd = m.viewport.Update(msg)
		return m, cmd
	}
	return m, nil
}

func (m *SpecReviewModel) parseSpec(raw json.RawMessage) {
	var spec struct {
		Files []struct {
			Path      string            `json:"path"`
			Role      string            `json:"role"`
			Exports   []json.RawMessage `json:"exports"`
			PublicAPI []json.RawMessage `json:"public_api"`
		} `json:"files"`
		Dependencies []json.RawMessage `json:"dependencies"`
	}
	if err := json.Unmarshal(raw, &spec); err != nil {
		return
	}
	m.depCount = len(spec.Dependencies)
	m.rows = nil
	for _, f := range spec.Files {
		n := len(f.Exports)
		if n == 0 {
			n = len(f.PublicAPI)
		}
		m.rows = append(m.rows, specFileRow{path: f.Path, role: f.Role, exports: n})
	}
	sort.Slice(m.rows, func(i, j int) bool { return m.rows[i].path < m.rows[j].path })
}

func (m *SpecReviewModel) renderTree() {
	if !m.ready {
		return
	}
	var b strings.Builder
	var lastDir string
	for _, r := range m.rows {
		dir := "."
		base := r.path
		if idx := strings.LastIndex(r.path, "/"); idx >= 0 {
			dir = r.path[:idx]
			base = r.path[idx+1:]
		}
		if dir != lastDir {
			b.WriteString(StyleAccent.Render("📁 "+dir) + "\n")
			lastDir = dir
		}
		meta := ""
		if r.role != "" {
			meta = StyleMuted.Render("  [" + r.role + "]")
		}
		exp := ""
		if r.exports > 0 {
			exp = StyleMuted.Render(fmt.Sprintf("  %d exports", r.exports))
		}
		b.WriteString("   " + lipgloss.NewStyle().Foreground(colText).Render(base) + meta + exp + "\n")
	}
	if len(m.rows) == 0 {
		b.WriteString(StyleMuted.Render("(waiting for spec…)"))
	}
	m.viewport.SetContent(b.String())
}

func (m SpecReviewModel) View() string {
	header := StyleTitle.Render("Spec Review — Human Gate 1")
	stats := fmt.Sprintf("%s files   %s tests   %s dependencies",
		StyleBrand.Render(fmt.Sprintf("%d", m.fileCount)),
		StyleBrand.Render(fmt.Sprintf("%d", m.testCount)),
		StyleBrand.Render(fmt.Sprintf("%d", m.depCount)),
	)
	tree := ""
	if m.ready {
		tree = m.viewport.View()
	}
	hint := keyHint([2]string{"a/enter", "approve"}, [2]string{"r", "reject"}, [2]string{"↑/↓", "scroll"})
	w := min(m.width-2, 90)
	return header + "\n" + stats + "\n" + divider(w) + "\n" + tree + "\n" + divider(w) + "\n" + hint
}
