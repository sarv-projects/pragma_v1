package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/sarv-projects/pragma/internal/pipeline"
)

type ApproveDAGMsg struct{}

type DAGApprovalModel struct {
	slices     [][]string
	sliceCount int
	estSeconds int
	totalFiles int
	viewport   viewport.Model
	width      int
	height     int
	ready      bool
}

func NewDAGApprovalModel() DAGApprovalModel { return DAGApprovalModel{} }

func (m *DAGApprovalModel) SetSize(w, h int) {
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
	m.render()
}

func (m DAGApprovalModel) Init() tea.Cmd { return nil }

func (m DAGApprovalModel) Update(msg tea.Msg) (DAGApprovalModel, tea.Cmd) {
	switch msg := msg.(type) {
	case pipeline.DAGReadyEvent:
		m.slices = msg.Slices
		m.sliceCount = msg.SliceCount
		m.estSeconds = msg.EstSeconds
		m.totalFiles = 0
		for _, s := range msg.Slices {
			m.totalFiles += len(s)
		}
		m.render()
	case tea.KeyMsg:
		switch msg.String() {
		case "enter", "a":
			return m, func() tea.Msg { return ApproveDAGMsg{} }
		case "r":
			return m, func() tea.Msg { return cancelRunMsg{} }
		}
		var cmd tea.Cmd
		m.viewport, cmd = m.viewport.Update(msg)
		return m, cmd
	}
	return m, nil
}

func (m *DAGApprovalModel) render() {
	if !m.ready {
		return
	}
	var b strings.Builder
	for i, slice := range m.slices {
		par := ""
		if len(slice) > 1 {
			par = StyleAccent.Render(fmt.Sprintf("  (%d in parallel)", len(slice)))
		}
		b.WriteString(StyleBrand.Render(fmt.Sprintf("Slice %d", i+1)) + par + "\n")
		for _, f := range slice {
			b.WriteString(StyleMuted.Render("   • "+f) + "\n")
		}
		b.WriteString("\n")
	}
	if len(m.slices) == 0 {
		b.WriteString(StyleMuted.Render("(waiting for execution plan…)"))
	}
	m.viewport.SetContent(b.String())
}

func (m DAGApprovalModel) View() string {
	header := StyleTitle.Render("Execution Plan — Human Gate 2")
	summary := fmt.Sprintf("%s slices   %s files   est. ~%ds",
		StyleBrand.Render(fmt.Sprintf("%d", m.sliceCount)),
		StyleBrand.Render(fmt.Sprintf("%d", m.totalFiles)),
		m.estSeconds,
	)
	plan := ""
	if m.ready {
		plan = m.viewport.View()
	}
	hint := keyHint([2]string{"a/enter", "approve"}, [2]string{"r", "reject"}, [2]string{"↑/↓", "scroll"})
	w := min(m.width-2, 90)
	return header + "\n" + summary + "\n" + divider(w) + "\n" + plan + "\n" + divider(w) + "\n" + hint
}
