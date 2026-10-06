package httpapi_test

import (
	"net/http"
	"testing"
)

func TestStaticServesEveryGETOutsideTheAPI(t *testing.T) {
	api := newAPI(t)

	rec := api.do(http.MethodGet, "/settings", nil)
	wantStatus(t, rec, http.StatusOK)
	if rec.Body.String() != "static" {
		t.Errorf("GET /settings = %q, want the web client", rec.Body)
	}
	// A wrong method on an API route is still the API's answer, not the client.
	wantStatus(t, api.do(http.MethodPost, "/api/v1/today", map[string]string{}), http.StatusMethodNotAllowed)
}
