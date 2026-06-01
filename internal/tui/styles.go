package tui

import (
	"os"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
)

// Pragma palette — a balanced indigo/teal theme that reads well on both dark
// and light terminals (not too dark, not too light). Hex values are adapted to
// the terminal's color profile automatically by lipgloss.
var (
	colBrand   = lipgloss.Color("#8b7cf6") // indigo/violet — primary brand
	colBrandHi = lipgloss.Color("#a78bfa") // lighter brand for highlights
	colAccent  = lipgloss.Color("#2dd4bf") // teal accent
	colSuccess = lipgloss.Color("#34d399") // green
	colWarning = lipgloss.Color("#fbbf24") // amber
	colError   = lipgloss.Color("#f87171") // soft red
	colText    = lipgloss.AdaptiveColor{Light: "#1f2937", Dark: "#e5e7eb"}
	colMuted   = lipgloss.AdaptiveColor{Light: "#6b7280", Dark: "#9ca3af"}
	colSubtle  = lipgloss.Color("#5b5b7a") // borders / dividers
	colBarBg   = lipgloss.Color("#2a2a40")
	colBarFg   = lipgloss.Color("#d6d6e7")
)

var (
	StyleTitle = lipgloss.NewStyle().Bold(true).Foreground(colBrandHi)

	StyleSubtitle = lipgloss.NewStyle().Foreground(colMuted)

	StyleSuccess = lipgloss.NewStyle().Foreground(colSuccess).Bold(true)
	StyleWarning = lipgloss.NewStyle().Foreground(colWarning)
	StyleError   = lipgloss.NewStyle().Foreground(colError).Bold(true)
	StyleMuted   = lipgloss.NewStyle().Foreground(colMuted)
	StyleAccent  = lipgloss.NewStyle().Foreground(colAccent)
	StyleBrand   = lipgloss.NewStyle().Foreground(colBrand)

	StyleBorder = lipgloss.NewStyle().BorderForeground(colSubtle)

	StyleStatusBar = lipgloss.NewStyle().
			Background(colBarBg).
			Foreground(colBarFg).
			Padding(0, 1)

	StyleSelected = lipgloss.NewStyle().Foreground(colBrandHi).Bold(true)

	// A rounded panel used to frame primary content.
	StylePanel = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(colSubtle).
			Padding(1, 2)

	// Pill/badge for status chips in the status bar.
	StyleChipBrand = lipgloss.NewStyle().Foreground(lipgloss.Color("#0b0b14")).
			Background(colBrand).Padding(0, 1).Bold(true)
	StyleChipAccent = lipgloss.NewStyle().Foreground(lipgloss.Color("#0b0b14")).
			Background(colAccent).Padding(0, 1)
)

func init() {
	// PRAGMA_NO_COLOR (spec §18.2) disables all colour output.
	if v := strings.TrimSpace(os.Getenv("PRAGMA_NO_COLOR")); v != "" {
		lipgloss.SetColorProfile(termenv.Ascii)
	}
}

// logo renders the Pragma wordmark.
func logo() string {
	return lipgloss.NewStyle().Bold(true).Foreground(colBrandHi).Render("▰ PRAGMA") +
		StyleMuted.Render("  spec-first code generation")
}

// divider returns a horizontal rule of the given width.
func divider(width int) string {
	if width < 4 {
		width = 4
	}
	return lipgloss.NewStyle().Foreground(colSubtle).Render(strings.Repeat("─", width))
}

// keyHint renders a "key action" hint group, e.g. [enter] approve.
func keyHint(pairs ...[2]string) string {
	var b strings.Builder
	for i, p := range pairs {
		if i > 0 {
			b.WriteString(StyleMuted.Render("  ·  "))
		}
		b.WriteString(StyleAccent.Render(p[0]))
		b.WriteString(" ")
		b.WriteString(StyleMuted.Render(p[1]))
	}
	return b.String()
}
