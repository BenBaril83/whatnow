package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/lipgloss"

	"github.com/jameynakama/whatnow/internal/domain"
)

func at(clock string) time.Time {
	t, err := time.ParseInLocation("2006-01-02 15:04", "2026-10-06 "+clock, time.UTC)
	if err != nil {
		panic(err)
	}
	return t
}

func event(title, from, to string) domain.Event {
	e := domain.Event{Title: title, StartsAt: at(from)}
	if to != "" {
		end := at(to)
		e.EndsAt = &end
	}
	return e
}

func labels(l stripLayout) []string {
	var out []string
	for _, t := range l.ticks {
		out = append(out, t.label)
	}
	return out
}

func TestStripRunsFromTheStartOfDayToTheEndOfDayInWholeHours(t *testing.T) {
	l := layoutStrip(nil, at("13:00"), at("17:30"), at("08:40"), 100)
	if l.now != 50 { // 13:00 is halfway through 8:00 to 18:00
		t.Errorf("now = %d, want 50", l.now)
	}
	if l.endOfDay != 95 {
		t.Errorf("endOfDay = %d, want 95", l.endOfDay)
	}
}

func TestStripTicksEveryThirdHourOnALongStrip(t *testing.T) {
	got := strings.Join(labels(layoutStrip(nil, at("09:00"), at("17:30"), at("08:40"), 100)), " ")
	if got != "9a 12p 3p" {
		t.Errorf("ticks = %q", got)
	}
}

func TestStripTicksEveryHourOnAShortStrip(t *testing.T) {
	got := strings.Join(labels(layoutStrip(nil, at("13:10"), at("17:30"), at("13:05"), 100)), " ")
	if got != "2p 3p 4p 5p" {
		t.Errorf("ticks = %q", got)
	}
}

func TestStripStretchesPastTheEndOfDayForLateEvents(t *testing.T) {
	l := layoutStrip([]domain.Event{event("Late call", "18:00", "19:00")}, at("09:00"), at("17:00"), at("09:00"), 110)
	item := l.items[0]
	if item.col+item.width > 110 {
		t.Errorf("event runs off the strip: col %d width %d", item.col, item.width)
	}
	if l.endOfDay >= item.col {
		t.Errorf("end of day %d should sit left of the late event at %d", l.endOfDay, item.col)
	}
}

func TestStripStartsAtAnEarlierEvent(t *testing.T) {
	l := layoutStrip([]domain.Event{event("Standup", "07:30", "08:00")}, at("13:00"), at("17:30"), at("08:40"), 110)
	if l.items[0].col != 5 { // 30 of 660 minutes, from 7:00
		t.Errorf("col = %d, want 5", l.items[0].col)
	}
}

func TestStripDrawsAnEventWithNoEndAsHalfAnHour(t *testing.T) {
	l := layoutStrip([]domain.Event{event("Focus", "13:00", "")}, at("09:00"), at("17:00"), at("09:00"), 90)
	if l.items[0].width != 5 { // 30 of 540 minutes
		t.Errorf("width = %d, want 5", l.items[0].width)
	}
}

func TestStripRendersEventsOnTheSecondRow(t *testing.T) {
	rows := strings.Split(layoutStrip([]domain.Event{event("Standup", "09:00", "09:30")}, at("13:00"), at("17:30"), at("08:40"), 100).render(), "\n")
	if got := string([]rune(rows[1])[10:19]); got != " Standup " {
		t.Errorf("event row = %q", got)
	}
}

func TestStripKeepsEveryRowAtTheStripWidth(t *testing.T) {
	for _, row := range strings.Split(layoutStrip([]domain.Event{event("A very long meeting title", "16:00", "17:00")}, at("13:00"), at("17:30"), at("08:40"), 60).render(), "\n") {
		if w := lipgloss.Width(row); w != 60 {
			t.Errorf("row width %d, want 60: %q", w, row)
		}
	}
}

func TestStripKeepsTheEndOfDayLabelInsideTheRightEdge(t *testing.T) {
	rows := strings.Split(layoutStrip(nil, at("10:00"), at("23:59"), at("10:00"), 40).render(), "\n")
	if !strings.HasSuffix(rows[0], "11:59 PM") {
		t.Errorf("label row = %q", rows[0])
	}
}

func TestHourLabelCallsNoon12p(t *testing.T) {
	if got := hourLabel(12); got != "12p" {
		t.Errorf("hourLabel(12) = %q", got)
	}
	if got := hourLabel(0); got != "12a" {
		t.Errorf("hourLabel(0) = %q", got)
	}
}
