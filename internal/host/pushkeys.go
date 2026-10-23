package host

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"errors"
	"log"
	"net/http"
	"strings"
	"time"
)

// pushPath is the one route a push key opens. It answers without the session cookie, so the
// user's own computer can send Jira tickets the server can't fetch itself.
const pushPath = "/api/v1/integrations/jira/push"

// ErrNoPushKey means the push key is unknown, replaced, or revoked.
var ErrNoPushKey = errors.New("host: no such push key")

// PushKey is what the settings card shows of a user's key: never the key itself.
type PushKey struct {
	CreatedAt  time.Time  `json:"created_at"`
	LastUsedAt *time.Time `json:"last_used_at"`
}

// CreatePushKey returns a new key for the user, replacing any they had. Only its hash is stored.
func (a *Accounts) CreatePushKey(ctx context.Context, userID string) (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	key := "wn_" + base64.RawURLEncoding.EncodeToString(raw)
	_, err := a.db.ExecContext(ctx,
		`INSERT INTO push_keys (user_id, key_hash, created_at) VALUES (?, ?, ?)
		 ON CONFLICT (user_id) DO UPDATE SET key_hash = excluded.key_hash, created_at = excluded.created_at, last_used_at = NULL`,
		userID, hashToken(key), a.now().UTC().Format(timeLayout))
	if err != nil {
		return "", err
	}
	return key, nil
}

// PushKey returns the user's key as the card shows it, or nil when they have none.
func (a *Accounts) PushKey(ctx context.Context, userID string) (*PushKey, error) {
	var created string
	var used sql.NullString
	err := a.db.QueryRowContext(ctx, `SELECT created_at, last_used_at FROM push_keys WHERE user_id = ?`, userID).Scan(&created, &used)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	k := &PushKey{}
	if k.CreatedAt, err = time.Parse(timeLayout, created); err != nil {
		return nil, err
	}
	if used.Valid {
		t, err := time.Parse(timeLayout, used.String)
		if err != nil {
			return nil, err
		}
		k.LastUsedAt = &t
	}
	return k, nil
}

func (a *Accounts) DeletePushKey(ctx context.Context, userID string) error {
	_, err := a.db.ExecContext(ctx, `DELETE FROM push_keys WHERE user_id = ?`, userID)
	return err
}

// UserForPushKey returns the key's user and notes the key was used, or ErrNoPushKey.
func (a *Accounts) UserForPushKey(ctx context.Context, key string) (User, error) {
	var userID string
	err := a.db.QueryRowContext(ctx,
		`UPDATE push_keys SET last_used_at = ? WHERE key_hash = ? RETURNING user_id`,
		a.now().UTC().Format(timeLayout), hashToken(key)).Scan(&userID)
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, ErrNoPushKey
	}
	if err != nil {
		return User{}, err
	}
	return userByID(ctx, a.db, userID)
}

func (f *frontDoor) pushKey(w http.ResponseWriter, r *http.Request, u User) {
	k, err := f.accounts.PushKey(r.Context(), u.ID)
	if err != nil {
		log.Printf("find push key for %s: %v", u.ID, err)
		writeJSON(w, http.StatusInternalServerError, errorBody{"could not look up your key"})
		return
	}
	writeJSON(w, http.StatusOK, struct {
		Key *PushKey `json:"key"`
	}{k})
}

func (f *frontDoor) createPushKey(w http.ResponseWriter, r *http.Request, u User) {
	key, err := f.accounts.CreatePushKey(r.Context(), u.ID)
	if err != nil {
		log.Printf("create push key for %s: %v", u.ID, err)
		writeJSON(w, http.StatusInternalServerError, errorBody{"could not create a key"})
		return
	}
	writeJSON(w, http.StatusOK, struct {
		Key string `json:"key"`
	}{key})
}

func (f *frontDoor) deletePushKey(w http.ResponseWriter, r *http.Request, u User) {
	if err := f.accounts.DeletePushKey(r.Context(), u.ID); err != nil {
		log.Printf("delete push key for %s: %v", u.ID, err)
		writeJSON(w, http.StatusInternalServerError, errorBody{"could not revoke the key"})
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// push takes tickets sent with a push key and hands them to the key's user's App, at a route the
// session cookie can't reach.
func (f *frontDoor) push(w http.ResponseWriter, r *http.Request) {
	key, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !ok || key == "" {
		writeJSON(w, http.StatusUnauthorized, errorBody{"send your key"})
		return
	}
	u, err := f.accounts.UserForPushKey(r.Context(), key)
	if errors.Is(err, ErrNoPushKey) {
		writeJSON(w, http.StatusUnauthorized, errorBody{"unknown key"})
		return
	}
	if err != nil {
		log.Printf("find push key: %v", err)
		writeJSON(w, http.StatusInternalServerError, errorBody{"could not check your key"})
		return
	}
	if !f.limiter(u.ID).AllowN(f.now(), 1) {
		writeJSON(w, http.StatusTooManyRequests, errorBody{"too many requests; wait a minute and try again"})
		return
	}
	fwd := r.Clone(r.Context())
	fwd.URL.Path = "/api/v1/integrations/jira/received"
	fwd.URL.RawPath = ""
	f.registry.Serve(w, fwd, u.ID)
}
