package host

import (
	"context"
	"crypto/subtle"
	"errors"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"time"

	"github.com/jameynakama/whatnow/internal/settings"
	"github.com/jameynakama/whatnow/internal/store"
)

const (
	oauthCookie   = "whatnow_oauth"
	sessionCookie = "whatnow_session"
	// oauthPath scopes the oauth cookie to the sign-in routes, so no other request carries it.
	oauthPath = "/auth/google"
	oauthLife = 10 * time.Minute
)

// signIn serves Google sign-in: it creates accounts and their databases, and starts sessions.
type signIn struct {
	accounts *Accounts
	registry *Registry
	provider Provider
	maxUsers int
}

func (s *signIn) register(mux *http.ServeMux) {
	mux.HandleFunc("GET "+oauthPath, s.begin)
	mux.HandleFunc("GET "+oauthPath+"/callback", s.callback)
}

// begin sends the browser to the provider. The cookie keeps what the callback must check, plus
// the browser's time zone (?tz=) for a new account.
func (s *signIn) begin(w http.ResponseWriter, r *http.Request) {
	state, err := randomHex(32)
	if err != nil {
		http.Error(w, "could not start sign-in", http.StatusInternalServerError)
		return
	}
	nonce, err := randomHex(32)
	if err != nil {
		http.Error(w, "could not start sign-in", http.StatusInternalServerError)
		return
	}
	v := url.Values{"state": {state}, "nonce": {nonce}, "tz": {r.URL.Query().Get("tz")}}
	http.SetCookie(w, &http.Cookie{
		Name: oauthCookie, Value: v.Encode(), Path: oauthPath, MaxAge: int(oauthLife.Seconds()),
		HttpOnly: true, Secure: true, SameSite: http.SameSiteLaxMode,
	})
	http.Redirect(w, r, s.provider.AuthURL(state, nonce), http.StatusFound)
}

func (s *signIn) callback(w http.ResponseWriter, r *http.Request) {
	// The cookie is single use, whatever happens next.
	http.SetCookie(w, &http.Cookie{
		Name: oauthCookie, Path: oauthPath, MaxAge: -1,
		HttpOnly: true, Secure: true, SameSite: http.SameSiteLaxMode,
	})
	c, err := r.Cookie(oauthCookie)
	if err != nil {
		http.Error(w, "sign-in expired; try again", http.StatusBadRequest)
		return
	}
	saved, err := url.ParseQuery(c.Value)
	state := saved.Get("state")
	if err != nil || state == "" || subtle.ConstantTimeCompare([]byte(state), []byte(r.URL.Query().Get("state"))) != 1 {
		http.Error(w, "sign-in expired; try again", http.StatusBadRequest)
		return
	}

	ctx := r.Context()
	id, err := s.provider.Exchange(ctx, r.URL.Query().Get("code"), saved.Get("nonce"))
	if err != nil {
		log.Printf("sign-in exchange: %v", err)
		http.Error(w, "sign-in failed; try again", http.StatusBadRequest)
		return
	}
	u, created, err := s.accounts.SignIn(ctx, id, s.maxUsers)
	if errors.Is(err, ErrFull) {
		http.Redirect(w, r, "/?signups=closed", http.StatusSeeOther)
		return
	}
	if err != nil {
		log.Printf("sign in %s: %v", id.Subject, err)
		http.Error(w, "sign-in failed", http.StatusInternalServerError)
		return
	}
	if created {
		if err := s.createDatabase(ctx, u.ID, saved.Get("tz")); err != nil {
			log.Printf("create database for %s: %v", u.ID, err)
			// Without its database the account is unusable, so drop it and let the next sign-in start over.
			if err := s.accounts.DeleteUser(ctx, u.ID); err != nil {
				log.Printf("remove account %s: %v", u.ID, err)
			}
			http.Error(w, "could not create your planner", http.StatusInternalServerError)
			return
		}
	}

	token, err := s.accounts.CreateSession(ctx, u.ID)
	if err != nil {
		log.Printf("create session for %s: %v", u.ID, err)
		http.Error(w, "sign-in failed", http.StatusInternalServerError)
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name: sessionCookie, Value: token, Path: "/", MaxAge: int(sessionLife.Seconds()),
		HttpOnly: true, Secure: true, SameSite: http.SameSiteLaxMode,
	})
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

// createDatabase gives a new user a database whose settings carry their time zone, so their App
// opens in it.
func (s *signIn) createDatabase(ctx context.Context, userID, tz string) error {
	path := s.registry.DBPath(userID)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	db, err := store.Open(path)
	if err != nil {
		return err
	}
	defer db.Close()
	st := settings.Defaults()
	st.TimeZone = zoneName(tz)
	return settings.Save(ctx, db, st)
}

// zoneName is tz if it names an IANA zone, or UTC. "Local" loads but means the server's zone,
// which is never the user's.
func zoneName(tz string) string {
	if tz == "" || tz == "Local" {
		return "UTC"
	}
	if _, err := time.LoadLocation(tz); err != nil {
		return "UTC"
	}
	return tz
}
