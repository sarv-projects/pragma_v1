package tui

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/sarv-projects/pragma/internal/config"
	"github.com/sarv-projects/pragma/internal/keyvault"
)

const (
	setProfile = iota
	setLifetime
	setPerRun
	setOutput
	setDSKey
	setSave
	setRowCount
)

type SettingsModel struct {
	cfg      *config.Config
	cursor   int
	editing  bool
	input    textinput.Model
	profiles []string
	profIdx  int
	status   string
	width    int
	height   int
}

func NewSettingsModel(cfg *config.Config) SettingsModel {
	ti := textinput.New()
	ti.CharLimit = 200
	ti.Width = 40
	profiles := config.ProfileNames()
	profIdx := 0
	for i, p := range profiles {
		if p == cfg.Profile {
			profIdx = i
			break
		}
	}
	return SettingsModel{cfg: cfg, input: ti, profiles: profiles, profIdx: profIdx}
}

func (m *SettingsModel) SetSize(w, h int) { m.width, m.height = w, h }

func (m SettingsModel) Init() tea.Cmd { return nil }

func (m SettingsModel) Update(msg tea.Msg) (SettingsModel, tea.Cmd) {
	if m.editing {
		return m.updateEditing(msg)
	}
	switch msg := msg.(type) {
	case settingsSavedMsg:
		if msg.err != nil {
			m.status = StyleError.Render("Save failed: " + msg.err.Error())
			return m, nil
		}
		return m, func() tea.Msg { return screenChangeMsg(ScreenHome) }
	case tea.KeyMsg:
		switch msg.String() {
		case "up", "k":
			if m.cursor > 0 {
				m.cursor--
			}
		case "down", "j":
			if m.cursor < setRowCount-1 {
				m.cursor++
			}
		case "left", "right", "h", "l", " ":
			m.cycle()
		case "enter":
			return m.activate()
		}
	}
	return m, nil
}

func (m SettingsModel) updateEditing(msg tea.Msg) (SettingsModel, tea.Cmd) {
	switch km := msg.(type) {
	case tea.KeyMsg:
		switch km.Type {
		case tea.KeyEnter:
			m.commitEdit()
			m.editing = false
			return m, nil
		case tea.KeyEsc:
			m.editing = false
			return m, nil
		}
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}

func (m *SettingsModel) cycle() {
	switch m.cursor {
	case setProfile:
		if len(m.profiles) > 0 {
			m.profIdx = (m.profIdx + 1) % len(m.profiles)
			m.cfg.Profile = m.profiles[m.profIdx]
		}
	}
}

func (m SettingsModel) activate() (SettingsModel, tea.Cmd) {
	switch m.cursor {
	case setProfile:
		m.cycle()
		return m, nil
	case setLifetime:
		m.beginEdit(fmt.Sprintf("%.2f", m.cfg.Budget.LifetimeCap), "")
	case setPerRun:
		m.beginEdit(fmt.Sprintf("%.2f", m.cfg.Budget.PerRunCap), "")
	case setOutput:
		m.beginEdit(m.cfg.Output.Directory, "")
	case setDSKey:
		m.beginEdit("", "sk-...")
	case setSave:
		return m, m.save()
	}
	return m, nil
}

func (m *SettingsModel) beginEdit(value, placeholder string) {
	m.input.SetValue(value)
	m.input.Placeholder = placeholder
	m.input.Focus()
	m.editing = true
}

func (m *SettingsModel) commitEdit() {
	val := strings.TrimSpace(m.input.Value())
	switch m.cursor {
	case setLifetime:
		if f, err := strconv.ParseFloat(val, 64); err == nil {
			m.cfg.Budget.LifetimeCap = f
		}
	case setPerRun:
		if f, err := strconv.ParseFloat(val, 64); err == nil {
			m.cfg.Budget.PerRunCap = f
		}
	case setOutput:
		if val != "" {
			m.cfg.Output.Directory = val
		}
	case setDSKey:
		if val != "" {
			if err := keyvault.SaveKeys(map[string]string{keyvault.KeyDeepSeek: val}); err != nil {
				m.status = StyleError.Render("Failed to save DeepSeek key: " + err.Error())
			} else {
				m.status = StyleSuccess.Render("DeepSeek key saved.")
			}
		}
	}
}

func (m SettingsModel) save() tea.Cmd {
	return func() tea.Msg {
		if err := m.cfg.Save(config.DefaultPath()); err != nil {
			return settingsSavedMsg{err: err}
		}
		return settingsSavedMsg{}
	}
}

type settingsSavedMsg struct{ err error }

func maskKey(name string) string {
	kr := keyvault.NewKeyring(keyvault.DefaultService)
	v, err := kr.Get(name)
	if err != nil || v == "" {
		return StyleMuted.Render("(not set)")
	}
	if len(v) <= 4 {
		return "••••"
	}
	return "••••" + v[len(v)-4:]
}

func (m SettingsModel) View() string {
	var b strings.Builder
	b.WriteString(StyleTitle.Render("Settings") + "\n\n")

	mode := "Fast (DeepSeek)"
	rows := []struct{ label, value string }{
		{"Mode", mode},
		{"Profile", m.cfg.Profile + StyleMuted.Render("   ← space/→ to cycle")},
		{"Lifetime budget", fmt.Sprintf("$%.2f", m.cfg.Budget.LifetimeCap)},
		{"Per-run budget", fmt.Sprintf("$%.2f", m.cfg.Budget.PerRunCap)},
		{"Output dir", m.cfg.Output.Directory},
		{"DeepSeek key", maskKey(keyvault.KeyDeepSeek)},
		{"Save & back", ""},
	}

	for i, r := range rows {
		cursor := "  "
		label := StyleMuted.Render(fmt.Sprintf("%-16s", r.label))
		if i == m.cursor {
			cursor = StyleSelected.Render("❯ ")
			label = StyleSelected.Render(fmt.Sprintf("%-16s", r.label))
		}
		val := r.value
		if m.editing && i == m.cursor {
			val = m.input.View()
		}
		b.WriteString(cursor + label + val + "\n")
	}

	if m.status != "" {
		b.WriteString("\n" + m.status + "\n")
	}
	b.WriteString("\n" + keyHint([2]string{"↑/↓", "move"}, [2]string{"enter", "edit/toggle"}, [2]string{"esc", "back"}))
	return StylePanel.Render(b.String())
}
