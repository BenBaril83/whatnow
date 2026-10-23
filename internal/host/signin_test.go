package host

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"

	"github.com/jameynakama/whatnow/internal/settings"
	"github.com/jameynakama/whatnow/internal/store"
)

// fakeProvider signs everyone in as id and records the nonce it was asked to check.
type fakeProvider struct {
	id    Identity
	nonce string
}

func (p *fakeProvider) AuthURL(state, nonce string) string {
	return "https://idp.test/auth?" + url.Values{"state": {state}, "nonce": {nonce}}.Encode()
}

func (p *fakeProvider) Exchange(_ context.Context, code, nonce string) (Identity, error) {
	if code != "the-code" {
		return Identity{}, errors.New("bad code")
	}
	p.nonce = nonce
	return p.id, nil
}

type signInTest struct {
	t        *testing.T
	mux      *http.ServeMux
	accounts *Accounts
	registry *Registry
	provider *fakeProvider
	dir      string
}

func newSignInTest(t *testing.T, maxUsers int) *signInTest {
	t.Helper()
	dir := t.TempDir()
	a := newAccounts(t, start())
	r := NewRegistry(dir, nil, idle, (&clock{t: *start()}).now)
	p := &fakeProvider{id: Identity{Subject: "g1", Email: "a@x.io", Name: "Ann"}}
	mux := http.NewServeMux()
	(&signIn{accounts: a, registry: r, provider: p, maxUsers: maxUsers}).register(mux)
	return &signInTest{t: t, mux: mux, accounts: a, registry: r, provider: p, dir: dir}
}

// begin starts sign-in with the query and returns the oauth cookie and the URL it redirects to.
func (s *signInTest) begin(query string) (*http.Cookie, *url.URL) {
	s.t.Helper()
	w := httptest.NewRecorder()
	s.mux.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/auth/google"+query, nil))
	if w.Code != http.StatusFound {
		s.t.Fatalf("begin: status %d, want %d", w.Code, http.StatusFound)
	}
	to, err := url.Parse(w.Header().Get("Location"))
	if err != nil {
		s.t.Fatal(err)
	}
	c := cookie(w, "whatnow_oauth")
	if c == nil {
		s.t.Fatal("begin set no whatnow_oauth cookie")
	}
	return c, to
}

func (s *signInTest) callback(c *http.Cookie, state string) *httptest.ResponseRecorder {
	s.t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/auth/google/callback?"+url.Values{"state": {state}, "code": {"the-code"}}.Encode(), nil)
	req.AddCookie(c)
	w := httptest.NewRecorder()
	s.mux.ServeHTTP(w, req)
	return w
}

// signInWith runs the whole flow with the query on the first request.
func (s *signInTest) signInWith(query string) *httptest.ResponseRecorder {
	s.t.Helper()
	c, to := s.begin(query)
	return s.callback(c, to.Query().Get("state"))
}

// timeZone is the time zone saved in the database of the user who holds the session.
func (s *signInTest) timeZone(session *http.Cookie) string {
	s.t.Helper()
	u, err := s.accounts.UserForSession(context.Background(), session.Value)
	if err != nil {
		s.t.Fatal(err)
	}
	db, err := store.Open(s.registry.DBPath(u.ID))
	if err != nil {
		s.t.Fatal(err)
	}
	defer db.Close()
	got, err := settings.Load(context.Background(), db)
	if err != nil {
		s.t.Fatal(err)
	}
	return got.TimeZone
}

func cookie(w *httptest.ResponseRecorder, name string) *http.Cookie {
	for _, c := range w.Result().Cookies() {
		if c.Name == name {
			return c
		}
	}
	return nil
}

func TestSignInFlow(t *testing.T) {
	s := newSignInTest(t, 10)
	oauth, to := s.begin("?tz=America/Chicago")
	if to.Host != "idp.test" {
		t.Fatalf("redirected to %s, want the provider", to)
	}
	if !oauth.HttpOnly || !oauth.Secure || oauth.SameSite != http.SameSiteLaxMode || oauth.MaxAge != 600 {
		t.Fatalf("oauth cookie %+v: want HttpOnly, Secure, SameSite=Lax, Max-Age 600", oauth)
	}

	w := s.callback(oauth, to.Query().Get("state"))
	if w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/" {
		t.Fatalf("callback: %d to %q, want %d to /", w.Code, w.Header().Get("Location"), http.StatusSeeOther)
	}
	if s.provider.nonce != to.Query().Get("nonce") {
		t.Fatalf("exchange checked nonce %q, want the one sent to the provider %q", s.provider.nonce, to.Query().Get("nonce"))
	}
	if c := cookie(w, "whatnow_oauth"); c == nil || c.MaxAge >= 0 {
		t.Fatalf("callback left the oauth cookie: %+v", c)
	}
	session := cookie(w, "whatnow_session")
	if session == nil || !session.HttpOnly || !session.Secure || session.SameSite != http.SameSiteLaxMode {
		t.Fatalf("session cookie %+v: want HttpOnly, Secure, SameSite=Lax", session)
	}
	if tz := s.timeZone(session); tz != "America/Chicago" {
		t.Fatalf("time zone %q, want America/Chicago", tz)
	}
}

func TestSignInBadState(t *testing.T) {
	s := newSignInTest(t, 10)
	oauth, to := s.begin("?tz=UTC")
	for _, state := range []string{"", "forged", to.Query().Get("state") + "x"} {
		w := s.callback(oauth, state)
		if w.Code != http.StatusBadRequest {
			t.Errorf("state %q: status %d, want %d", state, w.Code, http.StatusBadRequest)
		}
		if cookie(w, "whatnow_session") != nil {
			t.Errorf("state %q: set a session", state)
		}
	}
}

func TestSignInWithoutOAuthCookie(t *testing.T) {
	s := newSignInTest(t, 10)
	w := httptest.NewRecorder()
	s.mux.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/auth/google/callback?state=s&code=the-code", nil))
	if w.Code != http.StatusBadRequest || cookie(w, "whatnow_session") != nil {
		t.Fatalf("status %d, session %v; want %d and no session", w.Code, cookie(w, "whatnow_session"), http.StatusBadRequest)
	}
}

func TestSignInUnknownTimeZone(t *testing.T) {
	for _, query := range []string{"?tz=Nowhere/Special", "", "?tz=Local", "?tz=../../etc/passwd"} {
		s := newSignInTest(t, 10)
		session := cookie(s.signInWith(query), "whatnow_session")
		if session == nil {
			t.Fatalf("query %q: no session", query)
		}
		if tz := s.timeZone(session); tz != "UTC" {
			t.Errorf("query %q: time zone %q, want UTC", query, tz)
		}
	}
}

func TestSignInExistingUserKeepsTimeZone(t *testing.T) {
	s := newSignInTest(t, 10)
	s.signInWith("?tz=America/Chicago")
	session := cookie(s.signInWith("?tz=Asia/Tokyo"), "whatnow_session")
	if session == nil {
		t.Fatal("second sign-in set no session")
	}
	if tz := s.timeZone(session); tz != "America/Chicago" {
		t.Fatalf("time zone %q, want America/Chicago kept from the first sign-in", tz)
	}
}

func TestSignInFlowFull(t *testing.T) {
	s := newSignInTest(t, 1)
	if _, _, err := s.accounts.SignIn(context.Background(), Identity{Subject: "g0"}, 1); err != nil {
		t.Fatal(err)
	}
	w := s.signInWith("?tz=UTC")
	if w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/?signups=closed" {
		t.Fatalf("%d to %q, want %d to /?signups=closed", w.Code, w.Header().Get("Location"), http.StatusSeeOther)
	}
	if cookie(w, "whatnow_session") != nil {
		t.Fatal("set a session")
	}
	if entries, err := os.ReadDir(filepath.Join(s.dir, "users")); !errors.Is(err, os.ErrNotExist) && len(entries) != 0 {
		t.Fatalf("users directory has %d entries (err %v), want none", len(entries), err)
	}
}

func TestSignInExchangeFails(t *testing.T) {
	s := newSignInTest(t, 10)
	oauth, to := s.begin("")
	req := httptest.NewRequest(http.MethodGet, "/auth/google/callback?"+url.Values{"state": {to.Query().Get("state")}, "error": {"access_denied"}}.Encode(), nil)
	req.AddCookie(oauth)
	w := httptest.NewRecorder()
	s.mux.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest || cookie(w, "whatnow_session") != nil {
		t.Fatalf("status %d, session %v; want %d and no session", w.Code, cookie(w, "whatnow_session"), http.StatusBadRequest)
	}
}
