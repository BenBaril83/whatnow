package tui

import (
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/charmbracelet/lipgloss"

	"github.com/jameynakama/whatnow/internal/domain"
)

// stripLayout places the day's events on a strip of whole columns. It follows strip() in
// web/src/lib/time.ts: the strip runs from the earliest of when the day started, the first event,
// and now, to the end of day, rounded out to whole hours, and stretches to keep now and every
// event on it. Ticks mark every third clock hour, or every hour on a strip of 6 hours or less.
type stripLayout struct {
	width    int
	ticks    []stripTick
	items    []stripItem
	now      int
	endOfDay int
	eodLabel string
}

type stripTick struct {
	col   int
	label string
}

type stripItem struct {
	col, width int
	title      string
}

func layoutStrip(events []domain.Event, now, endOfDay, startedAt time.Time, width int) stripLayout {
	loc := now.Location()
	start, end := now, endOfDay.In(loc)
	if s := startedAt.In(loc); s.Before(start) {
		start = s
	}
	if now.After(end) {
		end = now
	}
	for _, e := range events {
		if s := e.StartsAt.In(loc); s.Before(start) {
			start = s
		}
		if e := eventEnd(e).In(loc); e.After(end) {
			end = e
		}
	}
	start, end = floorHour(start), floorHour(end).Add(time.Hour)
	col := func(t time.Time) int {
		c := int(float64(width) * float64(t.Sub(start)) / float64(end.Sub(start)))
		return min(max(c, 0), width-1)
	}
	step := 3
	if end.Sub(start) <= 6*time.Hour {
		step = 1
	}
	l := stripLayout{width: width, now: col(now), endOfDay: col(endOfDay), eodLabel: endOfDay.In(loc).Format("3:04 PM")}
	for h := start.Add(time.Hour); h.Before(end); h = h.Add(time.Hour) {
		if h.Hour()%step == 0 {
			l.ticks = append(l.ticks, stripTick{col: col(h), label: hourLabel(h.Hour())})
		}
	}
	for _, e := range events {
		c := col(e.StartsAt)
		l.items = append(l.items, stripItem{col: c, width: max(col(eventEnd(e))-c, 1), title: e.Title})
	}
	return l
}

func floorHour(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), t.Hour(), 0, 0, 0, t.Location())
}

// eventEnd is when an event ends. One with no end is drawn as 30 minutes.
func eventEnd(e domain.Event) time.Time {
	if e.EndsAt != nil {
		return *e.EndsAt
	}
	return e.StartsAt.Add(30 * time.Minute)
}

func hourLabel(h int) string {
	suffix := "a"
	if h >= 12 {
		suffix = "p"
	}
	if h%12 == 0 {
		return fmt.Sprintf("12%s", suffix)
	}
	return fmt.Sprintf("%d%s", h%12, suffix)
}

type cellKind int

const (
	plainCell cellKind = iota
	tickCell
	eodCell
	eventCell
	nowCell
)

type cell struct {
	r    rune
	kind cellKind
}

var cellStyles = map[cellKind]lipgloss.Style{
	plainCell: lipgloss.NewStyle(),
	tickCell:  tickStyle,
	eodCell:   softStyle,
	eventCell: eventStyle,
	nowCell:   nowStyle,
}

// render draws the strip as two rows: tick and end-of-day labels, then events. The end-of-day rule
// and the now line cross both.
func (l stripLayout) render() string {
	var rows [2][]cell
	for i := range rows {
		rows[i] = make([]cell, l.width)
		for j := range rows[i] {
			rows[i][j] = cell{r: ' '}
		}
	}
	put := func(row, col int, s string, k cellKind) {
		for _, r := range s {
			if col >= 0 && col < l.width {
				rows[row][col] = cell{r: r, kind: k}
			}
			col++
		}
	}
	for _, t := range l.ticks {
		put(0, t.col, t.label, tickCell)
	}
	put(0, l.endOfDay, "┆", eodCell)
	put(1, l.endOfDay, "┆", eodCell)
	// The label sits right of its rule, sliding left to stay inside the strip.
	put(0, min(l.endOfDay+1, l.width-utf8.RuneCountInString(l.eodLabel)), l.eodLabel, eodCell)
	for _, it := range l.items {
		text := " " + it.title + " "
		if n := utf8.RuneCountInString(text); n < it.width {
			text += strings.Repeat(" ", it.width-n)
		}
		put(1, it.col, text, eventCell)
	}
	put(0, l.now, "▌", nowCell)
	put(1, l.now, "▌", nowCell)
	return paint(rows[0]) + "\n" + paint(rows[1])
}

// paint renders a row, styling each run of same-kind cells once.
func paint(cells []cell) string {
	var b strings.Builder
	for i := 0; i < len(cells); {
		j := i
		var run strings.Builder
		for j < len(cells) && cells[j].kind == cells[i].kind {
			run.WriteRune(cells[j].r)
			j++
		}
		b.WriteString(cellStyles[cells[i].kind].Render(run.String()))
		i = j
	}
	return b.String()
}
