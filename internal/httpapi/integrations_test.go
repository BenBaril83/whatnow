package httpapi_test

import (
	"errors"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/zalando/go-keyring"

	"github.com/jameynakama/whatnow/internal/domain"
	"github.com/jameynakama/whatnow/internal/httpapi"
	"github.com/jameynakama/whatnow/internal/integrations"
)

// testFeed has today's standup, a daily blockout, and a one-off, at 09:00 local (UTC-5).
const testFeed = "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nX-WR-CALNAME:me@example.com\r\n" +
	"BEGIN:VEVENT\r\nUID:standup@google.com\r\nDTSTAMP:20261001T000000Z\r\nSUMMARY:Standup\r\nDTSTART:20261005T140000Z\r\nDTEND:20261005T141500Z\r\nEND:VEVENT\r\n" +
	"BEGIN:VEVENT\r\nUID:block/out@google.com\r\nDTSTAMP:20261001T000000Z\r\nSUMMARY:Blockout\r\nRRULE:FREQ=DAILY\r\nDTSTART:20261001T200000Z\r\nDTEND:20261001T210000Z\r\nEND:VEVENT\r\n" +
	"END:VCALENDAR\r\n"

// setUpCalendar saves one calendar named Work, turns Google Calendar on, and returns the view.
func setUpCalendar(t *testing.T, api *testAPI) integrations.View {
	t.Helper()
	update := integrations.Update{Lists: map[string][]integrations.RowUpdate{"calendars": {
		{Values: map[string]string{"label": "Work", "url": api.feedURL}},
	}}}
	rec := api.do(http.MethodPut, "/api/v1/integrations/gcal", update)
	wantStatus(t, rec, http.StatusOK)
	if strings.Contains(rec.Body.String(), "private-token") {
		t.Fatal("the saved view contains the calendar's URL")
	}
	rec = api.do(http.MethodPut, "/api/v1/integrations/gcal/enabled", map[string]bool{"enabled": true})
	wantStatus(t, rec, http.StatusOK)
	return decodeAs[integrations.View](t, rec)
}

func TestIntegrationsListEachWithItsFields(t *testing.T) {
	keyring.MockInit()
	api := newAPI(t)
	views := decodeAs[[]integrations.View](t, api.do(http.MethodGet, "/api/v1/integrations", nil))
	if len(views) != 2 || views[0].ID != "gcal" || views[0].Name != "Google Calendar" || views[0].Enabled ||
		views[0].Pulls != integrations.PullsEvents || views[1].ID != "jira" || views[1].Pulls != integrations.PullsTickets {
		t.Fatalf("views = %+v", views)
	}
	cal := views[0].Fields[0]
	if cal.Key != "calendars" || cal.Kind != integrations.List || len(cal.Fields) != 2 || cal.Fields[1].Kind != integrations.Secret {
		t.Errorf("fields = %+v", views[0].Fields)
	}
}

func TestPullingGoogleCalendar(t *testing.T) {
	keyring.MockInit()
	api := newAPI(t)
	api.feed = testFeed
	wantStatus(t, api.do(http.MethodPost, "/api/v1/integrations/gcal/pull", nil), http.StatusConflict)
	v := setUpCalendar(t, api)
	if !v.Enabled || !v.Ready || !v.Lists["calendars"][0].Secrets["url"] {
		t.Fatalf("view = %+v", v)
	}

	rec := api.do(http.MethodPost, "/api/v1/integrations/gcal/pull", nil)
	wantStatus(t, rec, http.StatusOK)
	res := decodeAs[integrations.PullResult](t, rec)
	if len(res.Sources) != 1 || res.Sources[0].Label != "Work" || res.BatchID == 0 {
		t.Fatalf("result = %+v", res)
	}
	var got []string
	for _, e := range res.Sources[0].Events {
		got = append(got, e.Title+" "+e.Series)
	}
	if diff := cmp.Diff([]string{"Standup ", "Blockout block/out@google.com"}, got); diff != "" {
		t.Errorf("events (-want +got):\n%s", diff)
	}

	api.feed = "not a calendar"
	res = decodeAs[integrations.PullResult](t, api.do(http.MethodPost, "/api/v1/integrations/gcal/pull", nil))
	if res.Sources[0].Error != "couldn't read the calendar" {
		t.Errorf("failed pull = %+v", res)
	}
}

func TestSkippingASeriesAndBringingItBack(t *testing.T) {
	keyring.MockInit()
	api := newAPI(t)
	api.feed = testFeed
	setUpCalendar(t, api)
	res := decodeAs[integrations.PullResult](t, api.do(http.MethodPost, "/api/v1/integrations/gcal/pull", nil))
	blockout := res.Sources[0].Events[1]

	rec := api.do(http.MethodPost, "/api/v1/integrations/gcal/skipped",
		map[string]any{"key": blockout.Series, "title": blockout.Title, "event_id": blockout.ID})
	wantStatus(t, rec, http.StatusOK)
	if decodeAs[httpapi.Mutation](t, rec).BatchID == 0 {
		t.Error("no batch to undo")
	}
	res = decodeAs[integrations.PullResult](t, api.do(http.MethodPost, "/api/v1/integrations/gcal/pull", nil))
	if n := len(res.Sources[0].Events); n != 1 {
		t.Errorf("after skip: %d events", n)
	}

	// The key has a slash in it, so it's escaped in the path.
	rec = api.do(http.MethodDelete, "/api/v1/integrations/gcal/skipped/"+url.PathEscape(blockout.Series), nil)
	wantStatus(t, rec, http.StatusOK)
	if v := decodeAs[integrations.View](t, rec); len(v.Skipped) != 0 {
		t.Errorf("still skipped: %+v", v.Skipped)
	}
}

func TestSavingIntegrationsRejectsBadInput(t *testing.T) {
	keyring.MockInit()
	api := newAPI(t)
	wantStatus(t, api.do(http.MethodPut, "/api/v1/integrations/gcal",
		map[string]any{"lists": map[string]any{"calendars": []any{map[string]any{"values": map[string]string{"label": "Work"}}}}}),
		http.StatusBadRequest)
	wantStatus(t, api.do(http.MethodPut, "/api/v1/integrations/nope", map[string]any{}), http.StatusNotFound)
	wantStatus(t, api.do(http.MethodPost, "/api/v1/integrations/gcal/skipped", map[string]any{"key": "x", "event_id": 99}), http.StatusNotFound)
}

func TestDeleteEverythingForgetsCalendarURLs(t *testing.T) {
	keyring.MockInit()
	api := newAPI(t)
	v := setUpCalendar(t, api)
	acct := "integration/gcal/" + v.Lists["calendars"][0].ID + "/url"
	if _, err := keyring.Get("whatnow", acct); err != nil {
		t.Fatalf("the URL wasn't saved: %v", err)
	}

	wantStatus(t, api.do(http.MethodDelete, "/api/v1/data", nil), http.StatusNoContent)

	if _, err := keyring.Get("whatnow", acct); !errors.Is(err, keyring.ErrNotFound) {
		t.Errorf("the URL survived: err = %v", err)
	}
	views := decodeAs[[]integrations.View](t, api.do(http.MethodGet, "/api/v1/integrations", nil))
	if views[0].Enabled || len(views[0].Lists["calendars"]) != 0 {
		t.Errorf("after wipe = %+v", views[0])
	}
}

const testSearch = `{"total": 2, "issues": [
	{"key": "FLEX1-1", "fields": {"summary": "Mine", "description": "d", "assignee": {"name": "me"}}},
	{"key": "FLEX1-2", "fields": {"summary": "Up for grabs", "description": null, "assignee": null}}
]}`

// setUpJira saves Jira pointed at the fake search and turns it on.
func setUpJira(t *testing.T, api *testAPI) {
	t.Helper()
	rec := api.do(http.MethodPut, "/api/v1/integrations/jira", integrations.Update{Values: map[string]string{
		"url": api.jiraURL, "token": "pat-token", "jql": "project = FLEX1",
	}})
	wantStatus(t, rec, http.StatusOK)
	if strings.Contains(rec.Body.String(), "pat-token") {
		t.Fatal("the saved view contains the token")
	}
	wantStatus(t, api.do(http.MethodPut, "/api/v1/integrations/jira/enabled", map[string]bool{"enabled": true}), http.StatusOK)
}

func TestPickingJiraTickets(t *testing.T) {
	keyring.MockInit()
	api := newAPI(t)
	api.search = testSearch
	tk := integrations.Ticket{Key: "FLEX1-1", Title: "FLEX1-1 Mine", URL: api.jiraURL + "/browse/FLEX1-1"}
	wantStatus(t, api.do(http.MethodPost, "/api/v1/integrations/jira/add", map[string]any{"tickets": []integrations.Ticket{tk}}), http.StatusConflict)
	setUpJira(t, api)

	res := decodeAs[integrations.PullResult](t, api.do(http.MethodPost, "/api/v1/integrations/jira/pull", nil))
	if len(res.Sources) != 1 || len(res.Sources[0].Tickets) != 2 || res.BatchID != 0 {
		t.Fatalf("pull = %+v", res)
	}

	rec := api.do(http.MethodPost, "/api/v1/integrations/jira/add", map[string]any{"tickets": []integrations.Ticket{res.Sources[0].Tickets[0]}})
	wantStatus(t, rec, http.StatusOK)
	added := decodeAs[struct {
		BatchID int64         `json:"batch_id"`
		Data    []domain.Task `json:"data"`
	}](t, rec)
	if added.BatchID == 0 || len(added.Data) != 1 || added.Data[0].Title != "FLEX1-1 Mine" {
		t.Fatalf("added = %+v", added)
	}

	rec = api.do(http.MethodPost, "/api/v1/integrations/jira/skipped", map[string]string{"key": "FLEX1-2", "title": "FLEX1-2 Up for grabs"})
	wantStatus(t, rec, http.StatusOK)

	res = decodeAs[integrations.PullResult](t, api.do(http.MethodPost, "/api/v1/integrations/jira/pull", nil))
	if len(res.Sources[0].Tickets) != 0 {
		t.Errorf("pull after add and skip = %+v", res.Sources[0].Tickets)
	}
}

func TestSetVia(t *testing.T) {
	keyring.MockInit()
	api := newAPI(t)
	rec := api.do(http.MethodPut, "/api/v1/integrations/jira/via", map[string]string{"via": "computer"})
	wantStatus(t, rec, http.StatusOK)
	if v := decodeAs[integrations.View](t, rec); v.Via != "computer" {
		t.Errorf("view = %+v", v)
	}
	wantStatus(t, api.do(http.MethodPut, "/api/v1/integrations/jira/via", map[string]string{"via": "x"}), http.StatusBadRequest)
}

func TestReceivedHostedOnly(t *testing.T) {
	keyring.MockInit()
	push := map[string]any{"tickets": []integrations.Ticket{{Key: "FLEX1-7", Title: "FLEX1-7 Pushed"}}, "more": 0}

	local := newAPI(t)
	if rec := local.do(http.MethodPost, "/api/v1/integrations/jira/received", push); rec.Code == http.StatusNoContent {
		t.Error("a local server received a push")
	}

	api := newAPIHosted(t, true)
	wantStatus(t, api.do(http.MethodPut, "/api/v1/integrations/jira/via", map[string]string{"via": "computer"}), http.StatusOK)
	wantStatus(t, api.do(http.MethodPut, "/api/v1/integrations/jira/enabled", map[string]bool{"enabled": true}), http.StatusOK)
	wantStatus(t, api.do(http.MethodPost, "/api/v1/integrations/jira/received", push), http.StatusNoContent)

	res := decodeAs[integrations.PullResult](t, api.do(http.MethodPost, "/api/v1/integrations/jira/pull", nil))
	if len(res.Sources) != 1 || len(res.Sources[0].Tickets) != 1 || res.Sources[0].Tickets[0].Key != "FLEX1-7" {
		t.Errorf("pull = %+v", res)
	}
}

func TestHintOnlyWhenHosted(t *testing.T) {
	keyring.MockInit()
	for _, hosted := range []bool{false, true} {
		api := newAPIHosted(t, hosted)
		wantStatus(t, api.do(http.MethodPut, "/api/v1/integrations/jira", integrations.Update{Values: map[string]string{
			"url": "http://127.0.0.1:1", "token": "pat-token", "jql": "project = FLEX1",
		}}), http.StatusOK)
		wantStatus(t, api.do(http.MethodPut, "/api/v1/integrations/jira/enabled", map[string]bool{"enabled": true}), http.StatusOK)

		rec := api.do(http.MethodPost, "/api/v1/integrations/jira/pull", nil)
		wantStatus(t, rec, http.StatusOK)
		if got := strings.Contains(rec.Body.String(), `"hint":"computer"`); got != hosted {
			t.Errorf("hosted %v: pull = %s", hosted, rec.Body)
		}
	}
}
