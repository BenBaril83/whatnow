// Package jsonschema builds the JSON schemas that constrain structured LLM replies. Every object
// field is required and objects allow no extra properties, which keeps a schema inside what both
// the Claude API and OpenAI's strict mode accept.
//
// Field order matters: constrained decoding writes fields in schema order, so list them in the
// order the model should decide them.
package jsonschema

import (
	"bytes"
	"encoding/json"
)

// Field is one property of an object. Build it with Prop.
type Field struct {
	name   string
	schema any
}

func Prop(name string, schema any) Field { return Field{name: name, schema: schema} }

// fields marshals as a JSON object that keeps its order, which a Go map would sort.
type fields []Field

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

func Object(fs ...Field) map[string]any {
	required := make([]string, len(fs))
	for i, f := range fs {
		required[i] = f.name
	}
	return map[string]any{"type": "object", "properties": fields(fs), "required": required, "additionalProperties": false}
}

func Array(items map[string]any) map[string]any {
	return map[string]any{"type": "array", "items": items}
}

func String(description string) map[string]any {
	return map[string]any{"type": "string", "description": description}
}

func Integer(description string) map[string]any {
	return map[string]any{"type": "integer", "description": description}
}

func Enum(description string, values ...string) map[string]any {
	return map[string]any{"type": "string", "description": description, "enum": values}
}
