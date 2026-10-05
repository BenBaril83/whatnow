package jsonschema_test

import (
	"encoding/json"
	"testing"

	"github.com/jameynakama/whatnow/internal/jsonschema"
)

func TestObjectKeepsFieldOrderAndRequiresEveryField(t *testing.T) {
	s := jsonschema.Object(
		jsonschema.Prop("zeta", jsonschema.String("first")),
		jsonschema.Prop("alpha", jsonschema.Integer("second")),
	)
	b, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"additionalProperties":false,"properties":{"zeta":{"description":"first","type":"string"},` +
		`"alpha":{"description":"second","type":"integer"}},"required":["zeta","alpha"],"type":"object"}`
	if string(b) != want {
		t.Errorf("got  %s\nwant %s", b, want)
	}
}
