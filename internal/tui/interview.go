package tui

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/sarv-projects/pragma/internal/daemon"
)

// InterviewDoneMsg signals the interview is complete with a manifest.
type InterviewDoneMsg struct {
	Manifest string
}

type interviewResponseMsg struct {
	content  string
	done     bool
	manifest json.RawMessage
	err      error
}

type chatMessage struct {
	role    string
	content string
}

type InterviewModel struct {
	textInput textinput.Model
	viewport  viewport.Model
	messages  []chatMessage
	done      bool
	waiting   bool
	errMsg    string
	client    *daemon.Client
	width     int
	height    int
	ready     bool
}

func NewInterviewModel() InterviewModel {
	ti := textinput.New()
	ti.Placeholder = "Describe your project, or answer the question above..."
	ti.Focus()
	ti.CharLimit = 4000
	ti.Width = 70

	return InterviewModel{
		textInput: ti,
		messages:  []chatMessage{},
	}
}

func (m *InterviewModel) SetClient(c *daemon.Client) { m.client = c }

func (m *InterviewModel) SetSize(w, h int) {
	m.width, m.height = w, h
	vpHeight := h - 8
	if vpHeight < 3 {
		vpHeight = 3
	}
	vpWidth := w - 4
	if vpWidth < 20 {
		vpWidth = 20
	}
	if !m.ready {
		m.viewport = viewport.New(vpWidth, vpHeight)
		m.ready = true
	} else {
		m.viewport.Width = vpWidth
		m.viewport.Height = vpHeight
	}
	m.textInput.Width = vpWidth - 6
	m.refreshViewport()
}

func (m InterviewModel) Init() tea.Cmd {
	return tea.Batch(textinput.Blink, m.callDeepSeek())
}

func (m InterviewModel) callDeepSeek() tea.Cmd {
	client := m.client
	rpcMessages := make([]map[string]string, 0, len(m.messages))
	for _, msg := range m.messages {
		rpcMessages = append(rpcMessages, map[string]string{"role": msg.role, "content": msg.content})
	}

	return func() tea.Msg {
		if client == nil {
			return interviewResponseMsg{
				content: "Welcome to Pragma! Describe the project you want to build — purpose, key features, data models, and any integrations.",
				done:    false,
			}
		}
		ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
		defer cancel()
		args := map[string]any{"messages": rpcMessages}
		res, err := client.Call(ctx, "interview_chat", args)
		if err != nil {
			return interviewResponseMsg{err: err}
		}
		var resp struct {
			Content  string          `json:"content"`
			Done     bool            `json:"done"`
			Manifest json.RawMessage `json:"manifest"`
		}
		if err := json.Unmarshal(res, &resp); err != nil {
			return interviewResponseMsg{err: err}
		}
		return interviewResponseMsg{content: resp.Content, done: resp.Done, manifest: resp.Manifest}
	}
}

func (m InterviewModel) Update(msg tea.Msg) (InterviewModel, tea.Cmd) {
	var cmds []tea.Cmd
	switch msg := msg.(type) {
	case interviewResponseMsg:
		m.waiting = false
		if msg.err != nil {
			// #22: distinct error state, not a fake assistant bubble.
			m.errMsg = msg.err.Error()
			m.refreshViewport()
			return m, nil
		}
		m.errMsg = ""

		displayText := msg.content
		if idx := strings.Index(displayText, "[SCOPING_COMPLETE]"); idx >= 0 {
			displayText = strings.TrimSpace(displayText[:idx])
		}
		if displayText != "" {
			m.messages = append(m.messages, chatMessage{role: "assistant", content: displayText})
		}
		m.refreshViewport()

		if msg.done {
			m.done = true
			manifest := string(msg.manifest)
			if manifest == "" || manifest == "null" {
				// #23: don't ship a useless {"description":"project"} manifest —
				// reconstruct from the transcript instead.
				manifest = m.transcriptManifest()
			}
			return m, func() tea.Msg { return InterviewDoneMsg{Manifest: manifest} }
		}
		return m, nil

	case tea.KeyMsg:
		if m.waiting || m.done {
			// still allow scrolling while waiting
			var cmd tea.Cmd
			m.viewport, cmd = m.viewport.Update(msg)
			return m, cmd
		}
		switch msg.String() {
		case "enter":
			userText := strings.TrimSpace(m.textInput.Value())
			if userText == "" {
				return m, nil
			}
			m.messages = append(m.messages, chatMessage{role: "user", content: userText})
			m.textInput.SetValue("")
			m.waiting = true
			m.refreshViewport()
			return m, m.callDeepSeek()
		}
	}

	if !m.waiting && !m.done {
		var cmd tea.Cmd
		m.textInput, cmd = m.textInput.Update(msg)
		cmds = append(cmds, cmd)
	}
	var vpCmd tea.Cmd
	m.viewport, vpCmd = m.viewport.Update(msg)
	cmds = append(cmds, vpCmd)
	return m, tea.Batch(cmds...)
}

// transcriptManifest builds a minimal manifest from the user's answers so the
// pipeline always has the project description to work from.
func (m InterviewModel) transcriptManifest() string {
	var userParts []string
	for _, msg := range m.messages {
		if msg.role == "user" {
			userParts = append(userParts, msg.content)
		}
	}
	desc := strings.Join(userParts, " ")
	if desc == "" {
		desc = "a small web project"
	}
	b, _ := json.Marshal(map[string]any{"description": desc, "transcript": userParts})
	return string(b)
}

var (
	styleUser      = lipgloss.NewStyle().Foreground(colBrandHi).Bold(true)
	styleAssistant = lipgloss.NewStyle().Foreground(colAccent).Bold(true)
)

func (m *InterviewModel) refreshViewport() {
	if !m.ready {
		return
	}
	var sb strings.Builder
	for _, msg := range m.messages {
		if msg.role == "user" {
			sb.WriteString(styleUser.Render("You") + "\n")
			sb.WriteString(wrap(msg.content, m.viewport.Width) + "\n\n")
		} else {
			sb.WriteString(styleAssistant.Render("Pragma") + "\n")
			sb.WriteString(wrap(msg.content, m.viewport.Width) + "\n\n")
		}
	}
	if m.errMsg != "" {
		sb.WriteString(StyleError.Render("⚠ "+m.errMsg) + "\n")
	}
	m.viewport.SetContent(sb.String())
	m.viewport.GotoBottom()
}

func wrap(s string, width int) string {
	if width < 10 {
		width = 10
	}
	return lipgloss.NewStyle().Width(width).Render(s)
}

func (m InterviewModel) View() string {
	header := StyleTitle.Render("Phase 0 — Interview")
	var convo string
	if m.ready {
		convo = m.viewport.View()
	} else {
		convo = StyleMuted.Render("Loading...")
	}

	var footer string
	if m.waiting {
		footer = StyleMuted.Render("Pragma is thinking…")
	} else if !m.done {
		footer = m.textInput.View()
	} else {
		footer = StyleSuccess.Render("Scoping complete — moving on…")
	}

	hint := keyHint([2]string{"enter", "send"}, [2]string{"↑/↓", "scroll"}, [2]string{"esc", "home"})
	return header + "\n" + divider(min(m.width-2, 80)) + "\n" + convo + "\n" + divider(min(m.width-2, 80)) + "\n" + footer + "\n" + hint
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
