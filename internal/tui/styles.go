package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/jameynakama/whatnow/internal/domain"
)

// The sticker look from web/src/lib/tokens.css. Fills keep dark ink on them, so they read on light
// and dark terminals alike; plain ink flips with the terminal's background.
var (
	ink     = lipgloss.AdaptiveColor{Light: "#1f1a3d", Dark: "#f1eadb"}
	inkSoft = lipgloss.AdaptiveColor{Light: "#6b6590", Dark: "#a59fc9"}
	onFill  = lipgloss.Color("#1f1a3d")
	primary = lipgloss.Color("#7b5cff")
	nowRed  = lipgloss.Color("#ff4d6d")

	tierFill = map[domain.Tier]lipgloss.Color{
		domain.TierMust:   "#ffd9d2",
		domain.TierShould: "#d8ecff",
		domain.TierCould:  "#ffffff",
	}
	eventFill = lipgloss.Color("#e3ffd8")
	chipFill  = lipgloss.Color("#ffd84d")

	paneStyle   = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(ink).Padding(0, 1)
	softStyle   = lipgloss.NewStyle().Foreground(inkSoft)
	cursorStyle = lipgloss.NewStyle().Foreground(primary).Bold(true)
	toastStyle  = lipgloss.NewStyle().Background(chipFill).Foreground(onFill).Bold(true).Padding(0, 1)
	errorStyle  = lipgloss.NewStyle().Background(nowRed).Foreground(lipgloss.Color("#ffffff")).Bold(true).Padding(0, 1)
	nowStyle    = lipgloss.NewStyle().Foreground(nowRed)
	eventStyle  = lipgloss.NewStyle().Background(eventFill).Foreground(onFill)
	tickStyle   = softStyle.Bold(true)
	shadeStyle  = lipgloss.NewStyle().Foreground(ink)
)

func chip(text string, fill lipgloss.Color) string {
	return lipgloss.NewStyle().Background(fill).Foreground(onFill).Bold(true).Padding(0, 1).Render(text)
}

// sticker frames lines in a rounded pane w columns wide and h rows tall, shadow included. The
// shadow is a flat ink edge offset one column right and one row down. Lines past the pane's
// height are dropped, and long lines are cut with an ellipsis rather than wrapped.
func sticker(lines []string, w, h int) string {
	inner, rows := max(w-5, 1), max(h-3, 1)
	fitted := make([]string, 0, rows)
	for _, l := range lines[:min(len(lines), rows)] {
		fitted = append(fitted, ansi.Truncate(l, inner, "…"))
	}
	box := strings.Split(paneStyle.Width(inner+2).Height(rows).Render(strings.Join(fitted, "\n")), "\n")
	for i := range box {
		if i == 0 {
			box[i] += " "
			continue
		}
		box[i] += shadeStyle.Render("█")
	}
	box = append(box, " "+shadeStyle.Render(strings.Repeat("▀", lipgloss.Width(box[0])-1)))
	return strings.Join(box, "\n")
}
