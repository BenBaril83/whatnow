package httpapi_test

import (
	"net/http"
	"testing"
)

func TestOnlyRawCapturesTriggerCleanup(t *testing.T) {
	api := newAPI(t)
	raw := decodeAs[taskMutation](t, api.do(http.MethodPost, "/api/v1/tasks", map[string]string{"raw_text": "do the thing"}))
	api.do(http.MethodPost, "/api/v1/tasks", map[string]string{"title": "typed in the form"})

	if len(api.captured) != 1 || api.captured[0] != raw.Data.ID {
		t.Errorf("captured = %v, want only task %d", api.captured, raw.Data.ID)
	}
}
