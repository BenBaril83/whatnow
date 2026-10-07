package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

const goodnightKeyHint = "a add · u undo · c chat · ? keys · q quit"

// wrapped says today is wrapped up. The list gives way to the goodnight card until midnight.
func (m Model) wrapped() bool {
	return m.snap.today.Day != nil && m.snap.today.Day.WrappedAt != nil
}

// winLine counts the day's wins, as the web's Day's-done card does.
func winLine(done, meetings int) string {
	if done == 0 && meetings == 0 {
		return "Nothing checked off yet"
	}
	var parts []string
	if done > 0 {
		parts = append(parts, count(done, "thing done", "things done"))
	}
	if meetings > 0 {
		parts = append(parts, count(meetings, "meeting survived", "meetings survived"))
	}
	return "✓ " + strings.Join(parts, ", ")
}

func count(n int, one, many string) string {
	if n == 1 {
		return fmt.Sprintf("1 %s", one)
	}
	return fmt.Sprintf("%d %s", n, many)
}

// goodnightLines is the card that replaces the list once the day is wrapped, wrapped to w columns.
func (m Model) goodnightLines(w int) []string {
	day := m.snap.today.Day
	wrap := func(s string, style lipgloss.Style) []string {
		return strings.Split(style.Width(max(w, 1)).Render(s), "\n")
	}
	lines := []string{
		cursorStyle.Render("whatnow") + softStyle.Render(" · executive function for the rest of us"),
		"",
		"🌙 Goodnight. Day's done.",
		lipgloss.NewStyle().Bold(true).Render(winLine(m.snap.doneToday, m.snap.meetings)),
	}
	if day.Reflection != "" {
		lines = append(append(lines, ""), wrap(day.Reflection, lipgloss.NewStyle())...)
	}
	if day.TomorrowNote != "" {
		lines = append(append(lines, ""), wrap("For tomorrow: "+day.TomorrowNote, lipgloss.NewStyle())...)
	}
	return append(append(lines, ""), wrap("You're off the hook. Anything you add now waits for the morning.", softStyle)...)
}
