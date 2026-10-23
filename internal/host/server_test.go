package host

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jameynakama/whatnow/internal/app"
	"github.com/jameynakama/whatnow/internal/httpapi"
	"github.com/jameynakama/whatnow/internal/integrations"
	"github.com/jameynakama/whatnow/internal/settings"
)

const publicURL = "https://whatnow.test"

// frontNow is the users' clock: 20:00 UTC is already tomorrow in Pacific/Kiritimati (UTC+14).
var frontNow = time.Date(2026, 10, 22, 20, 0, 0, 0, time.UTC)

type frontTest struct {
	t        *testing.T
	handler  http.Handler
	accounts *Accounts
	registry *Registry
	// sessionClock is the accounts' clock, which the test moves to expire sessions.
	sessionClock *time.Time

	mu    sync.Mutex
	opens map[string]int
}

func newFrontTest(t *testing.T) *frontTest {
	t.Helper()
	f := &frontTest{t: t, sessionClock: start(), opens: map[string]int{}}
	f.accounts = newAccounts(t, f.sessionClock)
	f.registry = NewRegistry(t.TempDir(), f.open, idle, time.Now)
	t.Cleanup(f.registry.Close)
	u, err := url.Parse(publicURL)
	if err != nil {
		t.Fatal(err)
	}
	static := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { io.WriteString(w, "static") })
	f.handler = New(Config{PublicURL: u, MaxUsers: 10, Provider: &fakeProvider{}, Static: static}, f.accounts, f.registry)
	return f
}

// open builds a user's App and handler as the hosted server does.
func (f *frontTest) open(userID, dbPath string) (*app.App, http.Handler, error) {
	f.mu.Lock()
	f.opens[userID]++
	f.mu.Unlock()
	a, err := app.Open(app.Options{DBPath: dbPath, NewClient: settings.NewClient, Now: func() time.Time { return frontNow }, Hosted: &app.Hosted{}})
	if err != nil {
		return nil, nil, err
	}
	return a, httpapi.New(httpapi.Deps{
		Service:      a.Service,
		Hub:          a.Hub,
		DB:           a.DB,
		Loc:          a.Loc,
		Now:          a.Now,
		OnCapture:    a.Cleanup.Enqueue,
		Clean:        a.Cleanup.Clean,
		NewClient:    a.NewClient,
		Keys:         a.Clients.Keys,
		Planner:      a.Planner,
		Agent:        a.Agent,
		Integrations: a.Integrations,
		Static:       http.NotFoundHandler(),
		Hosted:       true,
	}), nil
}

func (f *frontTest) openCount(userID string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.opens[userID]
}

// signIn makes an account for sub and returns it with a session cookie.
func (f *frontTest) signIn(sub string) (User, *http.Cookie) {
	f.t.Helper()
	ctx := context.Background()
	u, _, err := f.accounts.SignIn(ctx, Identity{Subject: sub, Email: sub + "@x.io", Name: "Name " + sub}, 10)
	if err != nil {
		f.t.Fatal(err)
	}
	token, err := f.accounts.CreateSession(ctx, u.ID)
	if err != nil {
		f.t.Fatal(err)
	}
	return u, &http.Cookie{Name: sessionCookie, Value: token}
}

// request builds a request the way the web client sends one: to the public host, with the
// public origin and a JSON body on every write.
func request(t *testing.T, method, path string, c *http.Cookie, body any) *http.Request {
	t.Helper()
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		rd = bytes.NewReader(b)
	}
	req := httptest.NewRequest(method, path, rd)
	req.Host = "whatnow.test"
	if method != http.MethodGet && method != http.MethodHead {
		req.Header.Set("Origin", publicURL)
		req.Header.Set("Content-Type", "application/json")
	}
	if c != nil {
		req.AddCookie(c)
	}
	return req
}

func (f *frontTest) serve(req *http.Request) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	f.handler.ServeHTTP(w, req)
	return w
}

func (f *frontTest) do(method, path string, c *http.Cookie, body any) *httptest.ResponseRecorder {
	f.t.Helper()
	return f.serve(request(f.t, method, path, c, body))
}

func (f *frontTest) want(w *httptest.ResponseRecorder, status int) {
	f.t.Helper()
	if w.Code != status {
		f.t.Fatalf("status %d, want %d; body %s", w.Code, status, w.Body)
	}
}

func decodeBody[T any](t *testing.T, w *httptest.ResponseRecorder) T {
	t.Helper()
	var v T
	if err := json.Unmarshal(w.Body.Bytes(), &v); err != nil {
		t.Fatalf("decode %q: %v", w.Body, err)
	}
	return v
}

// clearsSession reports whether the response expires the session cookie.
func clearsSession(w *httptest.ResponseRecorder) bool {
	c := cookie(w, sessionCookie)
	return c != nil && c.MaxAge < 0 && c.Value == ""
}

func TestUsersSeeOnlyTheirOwn(t *testing.T) {
	f := newFrontTest(t)
	_, a := f.signIn("a")
	_, b := f.signIn("b")

	srv := httptest.NewServer(f.handler)
	t.Cleanup(srv.Close)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	streamReq, err := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+"/api/v1/stream", nil)
	if err != nil {
		t.Fatal(err)
	}
	streamReq.Host = "whatnow.test"
	streamReq.AddCookie(a)
	resp, err := srv.Client().Do(streamReq)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	lines := bufio.NewScanner(resp.Body)
	if !lines.Scan() || lines.Text() != ": connected" {
		t.Fatalf("stream opened with %q, want the connected comment", lines.Text())
	}

	// A has two tasks and B one, so each user's today can be told apart by its count.
	for _, w := range []struct {
		c     *http.Cookie
		title string
	}{{b, "B task"}, {a, "A task"}, {a, "A task 2"}} {
		f.want(f.do(http.MethodPost, "/api/v1/tasks", w.c, map[string]string{"title": w.title}), http.StatusCreated)
	}
	f.want(f.do(http.MethodPost, "/api/v1/events", a, map[string]any{"title": "A event", "starts_at": frontNow}), http.StatusCreated)
	f.want(f.do(http.MethodPost, "/api/v1/events", b, map[string]any{"title": "B event", "starts_at": frontNow}), http.StatusCreated)

	// B wrote first, so if B's writes reached A's stream, the first event would be B's.
	var data string
	for lines.Scan() {
		if after, ok := strings.CutPrefix(lines.Text(), "data: "); ok {
			data = after
			break
		}
	}
	if !strings.Contains(data, "A task") || strings.Contains(data, "B task") {
		t.Fatalf("A's stream first got %q, want A's own task", data)
	}

	for name, c := range map[string]*http.Cookie{"A": a, "B": b} {
		tasks := f.do(http.MethodGet, "/api/v1/tasks", c, nil)
		f.want(tasks, http.StatusOK)
		events := f.do(http.MethodGet, "/api/v1/events?date=2026-10-22", c, nil)
		f.want(events, http.StatusOK)
		for _, body := range []string{tasks.Body.String(), events.Body.String()} {
			if !strings.Contains(body, name+" ") || strings.Contains(body, otherName(name)+" ") {
				t.Errorf("%s got %s, want only their own", name, body)
			}
		}
		today := decodeBody[struct{ Unplanned []int64 }](t, f.do(http.MethodGet, "/api/v1/today", c, nil))
		if want := map[string]int{"A": 2, "B": 1}[name]; len(today.Unplanned) != want {
			t.Errorf("%s's today has %d tasks, want %d", name, len(today.Unplanned), want)
		}
	}
}

func otherName(name string) string {
	if name == "A" {
		return "B"
	}
	return "A"
}

func TestNeedsSession(t *testing.T) {
	f := newFrontTest(t)
	_, expiring := f.signIn("a")
	*f.sessionClock = f.sessionClock.Add(sessionLife + time.Minute)

	for name, c := range map[string]*http.Cookie{
		"no cookie": nil,
		"unknown":   {Name: sessionCookie, Value: "not-a-session"},
		"expired":   expiring,
	} {
		t.Run(name, func(t *testing.T) {
			f.want(f.do(http.MethodGet, "/api/v1/tasks", c, nil), http.StatusUnauthorized)
		})
	}
}

func TestDeletedUserSession(t *testing.T) {
	f := newFrontTest(t)
	u, c := f.signIn("a")
	if err := f.accounts.DeleteUser(context.Background(), u.ID); err != nil {
		t.Fatal(err)
	}

	w := f.do(http.MethodGet, "/api/v1/tasks", c, nil)
	f.want(w, http.StatusUnauthorized)
	if !clearsSession(w) {
		t.Errorf("cookies %v: want the session cleared", w.Result().Cookies())
	}
	if _, err := os.Stat(f.registry.DBPath(u.ID)); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("database stat: %v, want none created", err)
	}
	var n int
	if err := f.accounts.db.QueryRow(`SELECT count(*) FROM users`).Scan(&n); err != nil || n != 0 {
		t.Errorf("%d accounts (err %v), want none", n, err)
	}
}

func TestWrongOriginRefused(t *testing.T) {
	f := newFrontTest(t)
	_, c := f.signIn("a")

	evil := request(t, http.MethodPost, "/api/v1/tasks", c, map[string]string{"title": "x"})
	evil.Header.Set("Origin", "https://evil.example")
	noOrigin := request(t, http.MethodPost, "/api/v1/tasks", c, map[string]string{"title": "x"})
	noOrigin.Header.Del("Origin")
	form := request(t, http.MethodPost, "/api/v1/tasks", c, map[string]string{"title": "x"})
	form.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	logout := request(t, http.MethodPost, "/auth/logout", c, nil)
	logout.Header.Set("Origin", "https://evil.example")
	host := request(t, http.MethodGet, "/api/v1/tasks", c, nil)
	host.Host = "evil.example"
	static := request(t, http.MethodGet, "/", nil, nil)
	static.Host = "evil.example"

	for name, req := range map[string]*http.Request{
		"evil origin": evil, "no origin": noOrigin, "form post": form, "logout": logout,
		"wrong host": host, "wrong host static": static,
	} {
		t.Run(name, func(t *testing.T) {
			f.want(f.serve(req), http.StatusForbidden)
		})
	}
	tasks := f.do(http.MethodGet, "/api/v1/tasks", c, nil)
	f.want(tasks, http.StatusOK)
	if strings.Contains(tasks.Body.String(), `"x"`) {
		t.Errorf("a refused write landed: %s", tasks.Body)
	}
}

func TestBodyCapped(t *testing.T) {
	f := newFrontTest(t)
	_, c := f.signIn("a")
	w := f.do(http.MethodPost, "/api/v1/tasks", c, map[string]string{"title": "x", "notes": strings.Repeat("n", 1<<20)})
	if w.Code == http.StatusCreated {
		t.Fatal("a body over 1 MiB was accepted")
	}
}

func TestSessionEndpoint(t *testing.T) {
	f := newFrontTest(t)
	_, c := f.signIn("a")

	type session struct {
		Hosted bool
		User   *struct{ Name, Email string }
	}
	in := f.do(http.MethodGet, "/api/v1/session", c, nil)
	f.want(in, http.StatusOK)
	got := decodeBody[session](t, in)
	if !got.Hosted || got.User == nil || got.User.Name != "Name a" || got.User.Email != "a@x.io" {
		t.Fatalf("signed in: %s", in.Body)
	}

	out := f.do(http.MethodGet, "/api/v1/session", nil, nil)
	f.want(out, http.StatusOK)
	if !strings.Contains(out.Body.String(), `"user":null`) || !decodeBody[session](t, out).Hosted {
		t.Fatalf("signed out: %s", out.Body)
	}
}

func TestLogout(t *testing.T) {
	f := newFrontTest(t)
	_, c := f.signIn("a")
	w := f.do(http.MethodPost, "/auth/logout", c, nil)
	f.want(w, http.StatusNoContent)
	if !clearsSession(w) {
		t.Errorf("cookies %v: want the session cleared", w.Result().Cookies())
	}
	f.want(f.do(http.MethodGet, "/api/v1/tasks", c, nil), http.StatusUnauthorized)
}

func TestDeleteAccount(t *testing.T) {
	f := newFrontTest(t)
	u, c := f.signIn("a")
	_, other := f.signIn("b")
	f.want(f.do(http.MethodPost, "/api/v1/tasks", c, map[string]string{"title": "mine"}), http.StatusCreated)
	f.want(f.do(http.MethodPost, "/api/v1/tasks", other, map[string]string{"title": "theirs"}), http.StatusCreated)

	w := f.do(http.MethodDelete, "/api/v1/data", c, nil)
	f.want(w, http.StatusNoContent)
	if !clearsSession(w) {
		t.Errorf("cookies %v: want the session cleared", w.Result().Cookies())
	}
	for _, suffix := range []string{"", "-wal", "-shm"} {
		if _, err := os.Stat(f.registry.DBPath(u.ID) + suffix); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("database%s stat: %v, want it gone", suffix, err)
		}
	}
	var n int
	if err := f.accounts.db.QueryRow(`SELECT count(*) FROM users WHERE id = ?`, u.ID).Scan(&n); err != nil || n != 0 {
		t.Errorf("%d accounts (err %v), want it gone", n, err)
	}
	f.want(f.do(http.MethodGet, "/api/v1/tasks", c, nil), http.StatusUnauthorized)
	theirs := f.do(http.MethodGet, "/api/v1/tasks", other, nil)
	if theirs.Code != http.StatusOK || !strings.Contains(theirs.Body.String(), "theirs") {
		t.Errorf("the other user's tasks: %d %s", theirs.Code, theirs.Body)
	}
}

func TestTimeZoneChangeReopens(t *testing.T) {
	f := newFrontTest(t)
	u, c := f.signIn("a")
	if got := decodeBody[struct{ Date string }](t, f.do(http.MethodGet, "/api/v1/today", c, nil)); got.Date != "2026-10-22" {
		t.Fatalf("today in UTC is %s", got.Date)
	}

	st := settings.Defaults()
	st.TimeZone = "Pacific/Kiritimati"
	f.want(f.do(http.MethodPut, "/api/v1/settings", c, st), http.StatusOK)
	if got := decodeBody[struct{ Date string }](t, f.do(http.MethodGet, "/api/v1/today", c, nil)); got.Date != "2026-10-23" {
		t.Errorf("today after moving to UTC+14 is %s, want 2026-10-23", got.Date)
	}
	if n := f.openCount(u.ID); n != 2 {
		t.Errorf("opened %d times, want 2", n)
	}

	st.TimeZone = "Mars/Olympus"
	f.want(f.do(http.MethodPut, "/api/v1/settings", c, st), http.StatusBadRequest)
	f.do(http.MethodGet, "/api/v1/today", c, nil)
	if n := f.openCount(u.ID); n != 2 {
		t.Errorf("opened %d times after a refused save, want still 2", n)
	}
}

func TestRateLimited(t *testing.T) {
	f := newFrontTest(t)
	_, c := f.signIn("a")
	_, other := f.signIn("b")

	for i := range 30 {
		if w := f.do(http.MethodPost, "/api/v1/whatnow", c, map[string]any{}); w.Code == http.StatusTooManyRequests {
			t.Fatalf("request %d was limited, want a burst of 30", i+1)
		}
	}
	f.want(f.do(http.MethodPost, "/api/v1/whatnow", c, map[string]any{}), http.StatusTooManyRequests)
	f.want(f.do(http.MethodPut, "/api/v1/today/pace", c, map[string]any{"pace": 3}), http.StatusTooManyRequests)
	for range 40 {
		f.want(f.do(http.MethodGet, "/api/v1/tasks", c, nil), http.StatusOK)
	}
	if w := f.do(http.MethodPost, "/api/v1/whatnow", other, map[string]any{}); w.Code == http.StatusTooManyRequests {
		t.Error("another user was limited")
	}
}

func TestHostedCalendarCantReachLoopback(t *testing.T) {
	f := newFrontTest(t)
	_, c := f.signIn("a")
	var hits atomic.Int32
	cal := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		io.WriteString(w, "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nEND:VCALENDAR\r\n")
	}))
	t.Cleanup(cal.Close)

	update := integrations.Update{Lists: map[string][]integrations.RowUpdate{"calendars": {
		{Values: map[string]string{"label": "Work", "url": cal.URL + "/basic.ics"}},
	}}}
	f.want(f.do(http.MethodPut, "/api/v1/integrations/gcal", c, update), http.StatusOK)
	f.want(f.do(http.MethodPut, "/api/v1/integrations/gcal/enabled", c, map[string]bool{"enabled": true}), http.StatusOK)

	w := f.do(http.MethodPost, "/api/v1/integrations/gcal/pull", c, nil)
	f.want(w, http.StatusOK)
	res := decodeBody[integrations.PullResult](t, w)
	if len(res.Sources) != 1 || res.Sources[0].Error == "" {
		t.Errorf("pull = %+v, want the calendar to fail", res)
	}
	if n := hits.Load(); n != 0 {
		t.Errorf("the loopback calendar was fetched %d times", n)
	}
}
