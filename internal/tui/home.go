package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/sarv-projects/pragma/internal/budget"
	"github.com/sarv-projects/pragma/internal/config"
)

type HomeModel struct {
	cursor  int
	choices []string
	cfg     *config.Config
	oracle  *budget.Oracle
	width   int
	height  int
}

func NewHomeModel(cfg *config.Config, oracle *budget.Oracle) HomeModel {
	return HomeModel{
		choices: []string{"New Project", "Resume Run", "Settings", "Quit"},
		cfg:     cfg,
		oracle:  oracle,
	}
}

func (m *HomeModel) SetSize(w, h int) { m.width, m.height = w, h }

func (m HomeModel) Init() tea.Cmd { return nil }

func (m HomeModel) Update(msg tea.Msg) (HomeModel, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "up", "k":
			if m.cursor > 0 {
				m.cursor--
			}
		case "down", "j":
			if m.cursor < len(m.choices)-1 {
				m.cursor++
			}
		case "enter":
			switch m.cursor {
			case 0:
				return m, func() tea.Msg { return screenChangeMsg(ScreenInterview) }
			case 1:
				return m, func() tea.Msg { return screenChangeMsg(ScreenResume) }
			case 2:
				return m, func() tea.Msg { return screenChangeMsg(ScreenSettings) }
			case 3:
				return m, tea.Quit
			}
		}
	}
	return m, nil
}

func (m HomeModel) View() string {
	var b strings.Builder

	banner := lipgloss.NewStyle().Bold(true).Foreground(colBrandHi).Render("▰▰  PRAGMA")
	b.WriteString(banner + "\n")
	b.WriteString(StyleMuted.Render("Describe a project in plain English. Get a complete, buildable codebase.") + "\n\n")

	for i, choice := range m.choices {
		if m.cursor == i {
			b.WriteString(StyleSelected.Render("  ❯ "+choice) + "\n")
		} else {
			b.WriteString(StyleMuted.Render("    "+choice) + "\n")
		}
	}

	// Mode + budget summary panel. Pragma is DeepSeek-only.
	st := m.oracle.Status()
	mode := fmt.Sprintf("DeepSeek · $%.2f of $%.2f used", st.TotalSpent, st.LifetimeCap)
	info := StyleAccent.Render("Mode: ") + mode + "\n" +
		StyleAccent.Render("Profile: ") + m.cfg.Profile + "\n" +
		StyleAccent.Render("Output: ") + m.cfg.Output.Directory

	b.WriteString("\n" + StylePanel.Render(info) + "\n\n")
	b.WriteString(keyHint([2]string{"↑/↓", "move"}, [2]string{"enter", "select"}, [2]string{"q", "quit"}, [2]string{"?", "help"}))

	content := b.String()
	if m.width > 0 {
		return lipgloss.NewStyle().Padding(1, 2).Render(content)
	}
	return content
}
