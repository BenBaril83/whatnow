package cleanup

import "sort"

// schema is the reply shape. Every field is required and empty strings stand for "none", which
// keeps it inside what both the Claude API and OpenAI's strict mode accept.
var schema = object(map[string]any{
	"original": object(withFields(map[string]any{
		"keep": map[string]any{"type": "boolean", "description": "False only when the note holds nothing but events."},
	})),
	"new_tasks": array(object(withFields(map[string]any{
		"ref": str(`A short name other entries use to refer to this task, like "n1".`),
	}))),
	"events": array(object(map[string]any{
		"title":     str("What the event is."),
		"starts_at": str("Local start time as YYYY-MM-DDTHH:MM."),
		"ends_at":   str("Local end time as YYYY-MM-DDTHH:MM, or empty."),
	})),
	"blockers": array(object(map[string]any{
		"task":       str(`The task that waits: "original", a new task's ref, or "#<id>".`),
		"blocked_by": str(`The task that must finish first, referenced the same way.`),
	})),
})

func withFields(extra map[string]any) map[string]any {
	props := map[string]any{
		"title":      str("Short and concrete."),
		"notes":      str("Useful detail that doesn't fit the title, or empty."),
		"size":       enum("S, M, or L, or empty if unclear.", "", "S", "M", "L"),
		"energy":     enum("Mental effort: low, med, or high, or empty if unclear.", "", "low", "med", "high"),
		"feeling":    str("How the user feels about it, in their words, or empty."),
		"due_on":     str("YYYY-MM-DD, or empty."),
		"not_before": str("YYYY-MM-DD, or empty."),
		"refs": array(object(map[string]any{
			"label": str("A ticket key or short name."),
			"url":   str("The link, or empty."),
		})),
	}
	for k, v := range extra {
		props[k] = v
	}
	return props
}

func object(props map[string]any) map[string]any {
	required := make([]string, 0, len(props))
	for k := range props {
		required = append(required, k)
	}
	sort.Strings(required)
	return map[string]any{"type": "object", "properties": props, "required": required, "additionalProperties": false}
}

func array(items map[string]any) map[string]any {
	return map[string]any{"type": "array", "items": items}
}

func str(description string) map[string]any {
	return map[string]any{"type": "string", "description": description}
}

func enum(description string, values ...string) map[string]any {
	return map[string]any{"type": "string", "description": description, "enum": values}
}
