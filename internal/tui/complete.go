package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/sarv-projects/pragma/internal/pipeline"
)

type CompleteModel struct {
	info           pipeline.RunCompleteEvent
	coverage       int
	coverageSet    bool
	coverageIssues []string
	width          int
	height         int
	got            bool
}

func NewCompleteModel() CompleteModel { return CompleteModel{coverage: -1} }

func (m *CompleteModel) SetSize(w, h int) { m.width, m.height = w, h }

func (m CompleteModel) Init() tea.Cmd { return nil }

func (m CompleteModel) Update(msg tea.Msg) (CompleteModel, tea.Cmd) {
	switch msg := msg.(type) {
	case pipeline.RunCompleteEvent:
		m.info = msg
		m.got = true
		if msg.Coverage > 0 {
			m.coverage = msg.Coverage
			m.coverageSet = true
		}
	case pipeline.CoverageReportEvent:
		if msg.Total > 0 {
			m.coverage = msg.Passed * 100 / msg.Total
			m.coverageSet = true
		}
		m.coverageIssues = msg.Issues
	case tea.KeyMsg:
		switch msg.String() {
		case "enter", "h":
			return m, func() tea.Msg { return screenChangeMsg(ScreenHome) }
		case "n":
			return m, func() tea.Msg { return screenChangeMsg(ScreenInterview) }
		}
	}
	return m, nil
}

func (m CompleteModel) View() string {
	var b strings.Builder
	b.WriteString(StyleSuccess.Render("✓ Run Complete") + "\n\n")

	if !m.got {
		b.WriteString(StyleMuted.Render("Your project has been generated."))
		return StylePanel.Render(b.String())
	}

	i := m.info
	row := func(k, v string) string {
		return StyleAccent.Render(fmt.Sprintf("%-12s", k)) + v + "\n"
	}
	b.WriteString(row("Project", i.ProjectName))
	b.WriteString(row("Output", i.OutputPath))
	b.WriteString(row("Files", fmt.Sprintf("%d generated · %d healed · %d failed", i.FileCount, i.Healed, i.Failed)))
	if m.coverageSet {
		cov := fmt.Sprintf("%d%%", m.coverage)
		if m.coverage >= 100 {
			cov = StyleSuccess.Render(cov)
		} else {
			cov = StyleWarning.Render(cov)
		}
		b.WriteString(row("Coverage", cov))
	}
	b.WriteString(row("Cost", fmt.Sprintf("$%.4f this run · $%.2f budget left", i.TotalCost, i.BudgetLeft)))

	if len(m.coverageIssues) > 0 {
		b.WriteString("\n" + StyleWarning.Render("Coverage notes:") + "\n")
		shown := m.coverageIssues
		if len(shown) > 5 {
			shown = shown[:5]
		}
		for _, iss := range shown {
			b.WriteString(StyleMuted.Render("  ⚠ "+iss) + "\n")
		}
	}

	b.WriteString("\n" + StyleBrand.Render("Next steps:") + "\n")
	b.WriteString(StyleMuted.Render("  1. cd "+i.OutputPath) + "\n")
	b.WriteString(StyleMuted.Render("  2. Read README.md for setup & run commands") + "\n")
	b.WriteString(StyleMuted.Render("  3. Build & run, then iterate") + "\n\n")
	b.WriteString(keyHint([2]string{"n", "new project"}, [2]string{"enter/h", "home"}, [2]string{"ctrl+c", "quit"}))

	return StylePanel.Render(b.String())
}
