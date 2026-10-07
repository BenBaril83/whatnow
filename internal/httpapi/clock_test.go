package httpapi_test

import (
	"net/http"
	"testing"
	"time"

	"github.com/jameynakama/whatnow/internal/httpapi"
)

func TestClockIsTheServersTime(t *testing.T) {
	api := newAPI(t)
	rec := api.do(http.MethodGet, "/api/v1/clock", nil)
	wantStatus(t, rec, http.StatusOK)
	got := decodeAs[httpapi.Clock](t, rec)
	if want := time.Date(2026, 10, 5, 14, 0, 0, 0, time.UTC); !got.Now.Equal(want) {
		t.Errorf("got %v, want %v", got.Now, want)
	}
}
