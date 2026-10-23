package host

import (
	"encoding/json"
	"errors"
	"log"
	"mime"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

// Config is what the hosted front door needs beyond the accounts and the registry.
type Config struct {
	// PublicURL is where users reach the server. Requests for any other host are refused, and
	// writes must come from its origin.
	PublicURL *url.URL
	MaxUsers  int
	Provider  Provider
	// Static serves the web client.
	Static http.Handler
	// now is the rate limiters' clock, which tests freeze. Nil is time.Now.
	now func() time.Time
}

const maxBody = 1 << 20

// limited matches the requests a user's limiter meters: those that can call the model, and
// integration pulls, which make the server fetch addresses the user chose.
var limited = func() *http.ServeMux {
	mux := http.NewServeMux()
	for _, pattern := range []string{
		"POST /api/v1/tasks",
		"POST /api/v1/today/start",
		"POST /api/v1/today/replan",
		"POST /api/v1/today/wrap",
		"POST /api/v1/whatnow",
		"POST /api/v1/chat",
		"POST /api/v1/settings/test",
		"PUT /api/v1/today/pace",
		"POST /api/v1/integrations/{id}/pull",
	} {
		mux.HandleFunc(pattern, func(http.ResponseWriter, *http.Request) {})
	}
	return mux
}()

// frontDoor is what every hosted request passes through: it checks where the request came
// from, finds the user from the session, and hands the request to that user's App.
type frontDoor struct {
	accounts *Accounts
	registry *Registry
	host     string
	origin   string
	now      func() time.Time

	mu sync.Mutex
	// limiters outlive an App's eviction, so closing and reopening it doesn't reset the limit.
	limiters map[string]*rate.Limiter
}

// New serves hosted whatnow: sign-in, the session, and each user's API through their own App.
func New(cfg Config, accounts *Accounts, registry *Registry) http.Handler {
	f := &frontDoor{
		accounts: accounts,
		registry: registry,
		host:     cfg.PublicURL.Host,
		origin:   cfg.PublicURL.Scheme + "://" + cfg.PublicURL.Host,
		now:      cfg.now,
		limiters: map[string]*rate.Limiter{},
	}
	if f.now == nil {
		f.now = time.Now
	}
	mux := http.NewServeMux()
	(&signIn{accounts, registry, cfg.Provider, cfg.MaxUsers}).register(mux)
	mux.HandleFunc("POST /auth/logout", f.logout)
	mux.HandleFunc("GET /api/v1/session", f.session)
	mux.HandleFunc("DELETE /api/v1/data", f.withUser(f.deleteAccount))
	mux.HandleFunc("POST "+pushPath, f.push)
	mux.HandleFunc("GET /api/v1/integrations/jira/key", f.withUser(f.pushKey))
	mux.HandleFunc("POST /api/v1/integrations/jira/key", f.withUser(f.createPushKey))
	mux.HandleFunc("DELETE /api/v1/integrations/jira/key", f.withUser(f.deletePushKey))
	mux.HandleFunc("/api/v1/", f.withUser(f.dispatch))
	mux.Handle("/", cfg.Static)
	return f.guard(mux)
}

// guard refuses requests another site could make through the user's browser. A Host that isn't
// the public one means DNS rebinding. A write from another origin, or one that isn't JSON (a
// cross-site form post, which skips the CORS preflight), is cross-site request forgery. A push
// carries its key in a header no browser sends on its own, so it has no Origin to check.
func (f *frontDoor) guard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.EqualFold(r.Host, f.host) {
			writeJSON(w, http.StatusForbidden, errorBody{"wrong host"})
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			mt, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
			crossSite := r.Header.Get("Origin") != f.origin && r.URL.Path != pushPath
			if crossSite || err != nil || mt != "application/json" {
				writeJSON(w, http.StatusForbidden, errorBody{"writes must be application/json from this site"})
				return
			}
		}
		r.Body = http.MaxBytesReader(w, r.Body, maxBody)
		next.ServeHTTP(w, r)
	})
}

// withUser runs next with the session's user, or answers 401. A cookie whose session is gone,
// such as one for a deleted account, is cleared.
func (f *frontDoor) withUser(next func(http.ResponseWriter, *http.Request, User)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		c, err := r.Cookie(sessionCookie)
		if err != nil {
			writeJSON(w, http.StatusUnauthorized, errorBody{"sign in first"})
			return
		}
		u, err := f.accounts.UserForSession(r.Context(), c.Value)
		if errors.Is(err, ErrNoSession) {
			clearSession(w)
			writeJSON(w, http.StatusUnauthorized, errorBody{"sign in first"})
			return
		}
		if err != nil {
			log.Printf("find session: %v", err)
			writeJSON(w, http.StatusInternalServerError, errorBody{"could not check your session"})
			return
		}
		next(w, r, u)
	}
}

// dispatch hands the request to the user's App, once the user's limiter allows a metered one.
func (f *frontDoor) dispatch(w http.ResponseWriter, r *http.Request, u User) {
	// Pushed tickets arrive only through a push key; see push.
	if strings.HasSuffix(r.URL.Path, "/received") {
		writeJSON(w, http.StatusNotFound, errorBody{"not found"})
		return
	}
	if _, pattern := limited.Handler(r); pattern != "" && !f.limiter(u.ID).AllowN(f.now(), 1) {
		writeJSON(w, http.StatusTooManyRequests, errorBody{"too many requests; wait a minute and try again"})
		return
	}
	if r.Method != http.MethodPut || r.URL.Path != "/api/v1/settings" {
		f.registry.Serve(w, r, u.ID)
		return
	}
	// An App's time zone is fixed when it opens, so a saved move to another zone reopens it.
	sw := &statusWriter{ResponseWriter: w}
	f.registry.Serve(sw, r, u.ID)
	if sw.status >= 200 && sw.status < 300 {
		f.registry.EvictIfMoved(r.Context(), u.ID)
	}
}

func (f *frontDoor) limiter(userID string) *rate.Limiter {
	f.mu.Lock()
	defer f.mu.Unlock()
	l := f.limiters[userID]
	if l == nil {
		l = rate.NewLimiter(rate.Every(time.Minute/120), 30)
		f.limiters[userID] = l
	}
	return l
}

type sessionView struct {
	Hosted bool      `json:"hosted"`
	User   *userView `json:"user"`
}

type userView struct {
	Name  string `json:"name"`
	Email string `json:"email"`
}

// session tells the web client this is hosted, and who is signed in, if anyone.
func (f *frontDoor) session(w http.ResponseWriter, r *http.Request) {
	view := sessionView{Hosted: true}
	if c, err := r.Cookie(sessionCookie); err == nil {
		u, err := f.accounts.UserForSession(r.Context(), c.Value)
		if err != nil && !errors.Is(err, ErrNoSession) {
			log.Printf("find session: %v", err)
			writeJSON(w, http.StatusInternalServerError, errorBody{"could not check your session"})
			return
		}
		if err == nil {
			view.User = &userView{Name: u.Name, Email: u.Email}
		}
	}
	writeJSON(w, http.StatusOK, view)
}

func (f *frontDoor) logout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(sessionCookie); err == nil {
		if err := f.accounts.DeleteSession(r.Context(), c.Value); err != nil {
			log.Printf("delete session: %v", err)
			writeJSON(w, http.StatusInternalServerError, errorBody{"could not sign out"})
			return
		}
	}
	clearSession(w)
	w.WriteHeader(http.StatusNoContent)
}

// deleteAccount removes the account first, so no new request can get past the session check,
// then the user's App and database.
func (f *frontDoor) deleteAccount(w http.ResponseWriter, r *http.Request, u User) {
	if err := f.accounts.DeleteUser(r.Context(), u.ID); err != nil {
		log.Printf("delete account %s: %v", u.ID, err)
		writeJSON(w, http.StatusInternalServerError, errorBody{"could not delete your account"})
		return
	}
	f.mu.Lock()
	delete(f.limiters, u.ID)
	f.mu.Unlock()
	clearSession(w)
	if err := f.registry.Delete(u.ID); err != nil {
		log.Printf("remove data for %s: %v", u.ID, err)
		writeJSON(w, http.StatusInternalServerError, errorBody{"your account is gone, but its data could not all be removed"})
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func clearSession(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name: sessionCookie, Path: "/", MaxAge: -1,
		HttpOnly: true, Secure: true, SameSite: http.SameSiteLaxMode,
	})
}

// statusWriter records the status the handler answered with. Unwrap keeps flushing working
// through it.
type statusWriter struct {
	http.ResponseWriter
	status int
}

func (s *statusWriter) WriteHeader(code int) {
	if s.status == 0 {
		s.status = code
	}
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusWriter) Write(b []byte) (int, error) {
	if s.status == 0 {
		s.status = http.StatusOK
	}
	return s.ResponseWriter.Write(b)
}

func (s *statusWriter) Unwrap() http.ResponseWriter { return s.ResponseWriter }

type errorBody struct {
	Error string `json:"error"`
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		log.Printf("write response: %v", err)
	}
}
