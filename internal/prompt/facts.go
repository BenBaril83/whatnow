package prompt

import (
	"fmt"
	"strings"

	"github.com/jameynakama/whatnow/internal/facts"
)

// Facts renders the facts of the day as live context for the model.
func Facts(f facts.Facts) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Now: %s (%s). The working day ends at %s.\n", f.Now.Format("Monday 2006-01-02 15:04"),
		f.Now.Format("MST"), f.DayEnd.Format("15:04"))

	b.WriteString("Free time left today:")
	if len(f.Gaps) == 0 {
		b.WriteString(" none")
	}
	for i, g := range f.Gaps {
		if i > 0 {
			b.WriteByte(',')
		}
		fmt.Fprintf(&b, " %s-%s (%s)", g.Start.Format("15:04"), g.End.Format("15:04"), facts.Duration(g.Duration()))
	}

	b.WriteString("\n\nToday's events:\n")
	if len(f.Events) == 0 {
		b.WriteString("(none)\n")
	}
	for _, e := range f.Events {
		start := e.StartsAt.In(f.Now.Location()).Format("15:04")
		if e.EndsAt != nil {
			fmt.Fprintf(&b, "- %s-%s %s\n", start, e.EndsAt.In(f.Now.Location()).Format("15:04"), e.Title)
		} else {
			fmt.Fprintf(&b, "- %s %s\n", start, e.Title)
		}
	}

	b.WriteString("\nEligible tasks, as #<id> title:\n")
	if len(f.Tasks) == 0 {
		b.WriteString("(none)\n")
	}
	for _, tf := range f.Tasks {
		t := tf.Task
		fmt.Fprintf(&b, "#%d %s", t.ID, t.Title)
		var details []string
		if t.Size != nil {
			details = append(details, "size "+string(*t.Size))
		}
		if t.Energy != nil {
			details = append(details, string(*t.Energy)+" energy")
		}
		if len(details) > 0 {
			fmt.Fprintf(&b, " [%s]", strings.Join(details, ", "))
		}
		if t.Feeling != "" {
			fmt.Fprintf(&b, " feeling: %q", t.Feeling)
		}
		b.WriteByte('\n')
		for _, a := range tf.Annotations {
			fmt.Fprintf(&b, "  - %s\n", a)
		}
	}
	return b.String()
}
