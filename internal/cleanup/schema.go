package cleanup

import js "github.com/jameynakama/whatnow/internal/jsonschema"

// schema is the reply shape. Empty strings stand for "none". A task's ref and title come before
// its details, and blockers come after the tasks they refer to.
var schema = js.Object(
	js.Prop("original", js.Object(append([]js.Field{
		js.Prop("keep", map[string]any{"type": "boolean", "description": "False only when the note holds nothing but events."}),
	}, taskFields()...)...)),
	js.Prop("new_tasks", js.Array(js.Object(append([]js.Field{
		js.Prop("ref", js.String(`A short name other entries use to refer to this task, like "n1".`)),
	}, taskFields()...)...))),
	js.Prop("events", js.Array(js.Object(
		js.Prop("title", js.String("What the event is.")),
		js.Prop("starts_at", js.String("Local start time as YYYY-MM-DDTHH:MM.")),
		js.Prop("ends_at", js.String("Local end time as YYYY-MM-DDTHH:MM, or empty.")),
	))),
	js.Prop("blockers", js.Array(js.Object(
		js.Prop("task", js.String(`The task that waits: "original", a new task's ref, or "#<id>".`)),
		js.Prop("blocked_by", js.String(`The task that must finish first, referenced the same way.`)),
	))),
)

func taskFields() []js.Field {
	return []js.Field{
		js.Prop("title", js.String("Short and concrete.")),
		js.Prop("notes", js.String("Useful detail that doesn't fit the title, or empty.")),
		js.Prop("size", js.Enum("S, M, or L, or empty if unclear.", "", "S", "M", "L")),
		js.Prop("energy", js.Enum("Mental effort: low, med, or high, or empty if unclear.", "", "low", "med", "high")),
		js.Prop("feeling", js.String("How the user feels about it, in their words, or empty.")),
		js.Prop("due_on", js.String("YYYY-MM-DD, or empty.")),
		js.Prop("not_before", js.String("YYYY-MM-DD, or empty.")),
		js.Prop("refs", js.Array(js.Object(
			js.Prop("label", js.String("A ticket key or short name.")),
			js.Prop("url", js.String("The link, or empty.")),
		))),
	}
}
