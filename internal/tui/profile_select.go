package tui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/sarv-projects/pragma/internal/config"
)

// profileChosenMsg is emitted when the user picks a stack profile.
type profileChosenMsg struct{ profile string }

type ProfileSelectModel struct {
	names  []string
	metas  []config.ProfileMeta
	cursor int
	width  int
	height int
}

func NewProfileSelectModel(defaultProfile string) ProfileSelectModel {
	names := config.ProfileNames()
	metas := make([]config.ProfileMeta, len(names))
	for i, n := range names {
		if p, err := config.LoadProfile(n); err == nil {
			metas[i] = p.Meta
		}
	}
	cursor := 0
	for i, n := range names {
		if n == defaultProfile {
			cursor = i
			break
		}
	}
	return ProfileSelectModel{names: names, metas: metas, cursor: cursor}
}

func (m *ProfileSelectModel) SetSize(w, h int) { m.width, m.height = w, h }

func (m ProfileSelectModel) Init() tea.Cmd { return nil }

func (m ProfileSelectModel) Update(msg tea.Msg) (ProfileSelectModel, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "up", "k":
			if m.cursor > 0 {
				m.cursor--
			}
		case "down", "j":
			if m.cursor < len(m.names)-1 {
				m.cursor++
			}
		case "enter":
			if len(m.names) == 0 {
				return m, nil
			}
			chosen := m.names[m.cursor]
			return m, func() tea.Msg { return profileChosenMsg{profile: chosen} }
		}
	}
	return m, nil
}

func (m ProfileSelectModel) View() string {
	var b strings.Builder
	b.WriteString(StyleTitle.Render("Choose your stack") + "\n")
	b.WriteString(StyleMuted.Render("Pragma generates against a pinned, curated stack profile.") + "\n\n")

	for i, n := range m.names {
		meta := m.metas[i]
		label := meta.Name
		if label == "" {
			label = n
		}
		line := label + "  " + StyleMuted.Render("("+meta.Language+")")
		if i == m.cursor {
			b.WriteString(StyleSelected.Render("  ❯ "+line) + "\n")
			if meta.Description != "" {
				b.WriteString(StyleMuted.Render("      "+meta.Description) + "\n")
			}
		} else {
			b.WriteString(StyleMuted.Render("    "+line) + "\n")
		}
	}

	b.WriteString("\n" + keyHint([2]string{"↑/↓", "choose"}, [2]string{"enter", "start build"}, [2]string{"esc", "home"}))
	return StylePanel.Render(b.String())
}
