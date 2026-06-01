package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/sarv-projects/pragma/internal/keyvault"
)

// Onboarding steps:
//
//	0 = welcome
//	1 = DeepSeek API key (required)
//	2 = Groq API key (required)
//	3 = complete
type OnboardingModel struct {
	step      int
	keyInput  textinput.Model
	groqInput textinput.Model
	err       string
	width     int
	height    int
}

func NewOnboardingModel() OnboardingModel {
	ki := textinput.New()
	ki.CharLimit = 200
	ki.Width = 50
	gi := textinput.New()
	gi.CharLimit = 200
	gi.Width = 50
	return OnboardingModel{
		step:      0,
		keyInput:  ki,
		groqInput: gi,
	}
}

func (m *OnboardingModel) SetSize(w, h int) { m.width, m.height = w, h }

func (m OnboardingModel) Init() tea.Cmd { return textinput.Blink }

func (m OnboardingModel) Update(msg tea.Msg) (OnboardingModel, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.Type {
		case tea.KeyEnter:
			return m.advance()
		case tea.KeyEsc:
			if m.step > 0 {
				m.step--
				m.err = ""
				if m.step == 1 {
					m.keyInput.Focus()
				} else if m.step == 2 {
					m.groqInput.Focus()
				}
				return m, textinput.Blink
			}
			return m, nil
		}
	}

	var cmd tea.Cmd
	switch m.step {
	case 1:
		m.keyInput, cmd = m.keyInput.Update(msg)
	case 2:
		m.groqInput, cmd = m.groqInput.Update(msg)
	}
	return m, cmd
}

func (m OnboardingModel) advance() (OnboardingModel, tea.Cmd) {
	switch m.step {
	case 0:
		m.step = 1
		return m, nil
	case 1:
		key := strings.TrimSpace(m.keyInput.Value())
		if key == "" {
			m.err = "A DeepSeek API key is required to continue. Paste your key, or press ctrl+c to exit."
			return m, nil
		}
		if err := keyvault.SaveKeys(map[string]string{keyvault.KeyDeepSeek: key}); err != nil {
			m.err = "Could not save to OS keyring: " + err.Error() +
				"\nYou can instead set the key as an environment variable."
			return m, nil
		}
		m.err = ""
		m.step = 2
		m.groqInput.Focus()
		return m, nil
	case 2:
		key := strings.TrimSpace(m.groqInput.Value())
		if key == "" {
			m.err = "A Groq API key is required to continue. Groq provides free image analysis with Llama 4 Scout."
			return m, nil
		}
		if err := keyvault.SaveKeys(map[string]string{keyvault.KeyGroq: key}); err != nil {
			m.err = "Could not save to OS keyring: " + err.Error() +
				"\nYou can instead set the key as an environment variable."
			return m, nil
		}
		m.err = ""
		m.step = 3
		return m, nil
	case 3:
		return m, func() tea.Msg { return screenChangeMsg(ScreenHome) }
	}
	return m, nil
}

func (m OnboardingModel) View() string {
	title := StyleTitle.Render("Welcome to Pragma") + "\n\n"
	var body string

	switch m.step {
	case 0:
		body = "Pragma turns a plain-English description into a complete, buildable codebase.\n\n" +
			"Let's get you set up — it takes about a minute.\n\n" +
			"Both DeepSeek and Groq API keys are required:\n" +
			"  • DeepSeek powers code generation (~$0.03/project)\n" +
			"  • Groq powers faster chat + image analysis (free)\n\n" +
			keyHint([2]string{"enter", "continue"}, [2]string{"ctrl+c", "exit"})

	case 1:
		provider, url := "DeepSeek", "https://platform.deepseek.com/api_keys"
		body = StyleSubtitle.Render(fmt.Sprintf("Step 1 of 2 — Paste your %s API key", provider)) + "\n\n" +
			"Get a key at:\n" +
			StyleAccent.Render("  "+url) + "\n\n" +
			m.keyInput.View() + "\n\n" +
			keyHint([2]string{"enter", "save"}, [2]string{"esc", "back"})
		if m.err != "" {
			body += "\n\n" + StyleError.Render(m.err)
		}

	case 2:
		provider, url := "Groq", "https://console.groq.com/keys"
		body = StyleSubtitle.Render(fmt.Sprintf("Step 2 of 2 — Paste your free %s API key", provider)) + "\n\n" +
			"Get a key at (no credit card needed):\n" +
			StyleAccent.Render("  "+url) + "\n\n" +
			m.groqInput.View() + "\n\n" +
			keyHint([2]string{"enter", "save"}, [2]string{"esc", "back"})
		if m.err != "" {
			body += "\n\n" + StyleError.Render(m.err)
		}

	default: // step 3 complete
		body = StyleSuccess.Render("✓ Setup complete!") + "\n\n" +
			"Both keys are stored in the OS keyring.\n\n" +
			keyHint([2]string{"enter", "go to home"})
	}

	return StylePanel.Render(title + body)
}
