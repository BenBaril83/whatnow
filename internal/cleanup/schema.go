package cleanup

import (
	"bytes"
	"encoding/json"
)

// schema is the reply shape. Every field is required and empty strings stand for "none", which
// keeps it inside what both the Claude API and OpenAI's strict mode accept.
//
// Field order matters: constrained decoding writes fields in schema order, so a task's ref and
// title come before its details, and blockers come after the tasks they refer to.
var schema = object(
	field{"original", object(append(fields{
		{"keep", map[string]any{"type": "boolean", "description": "False only when the note holds nothing but events."}},
	}, taskFields()...)...)},
	field{"new_tasks", array(object(append(fields{
		{"ref", str(`A short name other entries use to refer to this task, like "n1".`)},
	}, taskFields()...)...))},
	field{"events", array(object(
		field{"title", str("What the event is.")},
		field{"starts_at", str("Local start time as YYYY-MM-DDTHH:MM.")},
		field{"ends_at", str("Local end time as YYYY-MM-DDTHH:MM, or empty.")},
	))},
	field{"blockers", array(object(
		field{"task", str(`The task that waits: "original", a new task's ref, or "#<id>".`)},
		field{"blocked_by", str(`The task that must finish first, referenced the same way.`)},
	))},
)

func taskFields() fields {
	return fields{
		{"title", str("Short and concrete.")},
		{"notes", str("Useful detail that doesn't fit the title, or empty.")},
		{"size", enum("S, M, or L, or empty if unclear.", "", "S", "M", "L")},
		{"energy", enum("Mental effort: low, med, or high, or empty if unclear.", "", "low", "med", "high")},
		{"feeling", str("How the user feels about it, in their words, or empty.")},
		{"due_on", str("YYYY-MM-DD, or empty.")},
		{"not_before", str("YYYY-MM-DD, or empty.")},
		{"refs", array(object(
			field{"label", str("A ticket key or short name.")},
			field{"url", str("The link, or empty.")},
		))},
	}
}

type field struct {
	name   string
	schema any
}

// fields marshals as a JSON object that keeps its order, which a Go map would sort.
type fields []field

func (fs fields) MarshalJSON() ([]byte, error) {
	var buf bytes.Buffer
	buf.WriteByte('{')
	for i, f := range fs {
		if i > 0 {
			buf.WriteByte(',')
		}
		name, err := json.Marshal(f.name)
		if err != nil {
			return nil, err
		}
		value, err := json.Marshal(f.schema)
		if err != nil {
			return nil, err
		}
		buf.Write(name)
		buf.WriteByte(':')
		buf.Write(value)
	}
	buf.WriteByte('}')
	return buf.Bytes(), nil
}

func object(fs ...field) map[string]any {
	required := make([]string, len(fs))
	for i, f := range fs {
		required[i] = f.name
	}
	return map[string]any{"type": "object", "properties": fields(fs), "required": required, "additionalProperties": false}
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
