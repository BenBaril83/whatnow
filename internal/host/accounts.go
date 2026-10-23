// Package host holds what hosted mode needs beyond a single-user app: accounts, sign-in, and the HTTP front door.
package host

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"embed"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"time"

	"github.com/jameynakama/whatnow/internal/store"
)

//go:embed migrations/*.sql
var migrations embed.FS

// ErrFull means the user cap is reached, so no new account can be created.
var ErrFull = errors.New("host: user limit reached")

// ErrNoSession means the session token is unknown or has expired.
var ErrNoSession = errors.New("host: no such session")

const sessionLife = 90 * 24 * time.Hour

// timeLayout is fixed width, so stored timestamps sort correctly as strings.
const timeLayout = "2006-01-02T15:04:05.000000000Z07:00"

// Identity is who Google says someone is.
type Identity struct{ Subject, Email, Name string }

// User is a hosted account. ID is random and opaque; it names the user's database file.
type User struct {
	ID, GoogleSub, Email, Name string
	CreatedAt                  time.Time
}

// Accounts is the database of users and their sessions.
type Accounts struct {
	db  *sql.DB
	now func() time.Time
}

// OpenAccounts opens the accounts database at path. now is the clock for session expiry.
func OpenAccounts(path string, now func() time.Time) (*Accounts, error) {
	db, err := store.OpenMigrated(path, migrations)
	if err != nil {
		return nil, err
	}
	return &Accounts{db: db, now: now}, nil
}

func (a *Accounts) Close() error { return a.db.Close() }

// SignIn finds the account for the Google subject, refreshing its email and name, or creates it
// and reports true. Creating one beyond maxUsers fails with ErrFull.
func (a *Accounts) SignIn(ctx context.Context, id Identity, maxUsers int) (User, bool, error) {
	tx, err := a.db.BeginTx(ctx, nil)
	if err != nil {
		return User{}, false, err
	}
	defer tx.Rollback()

	u := User{GoogleSub: id.Subject, Email: id.Email, Name: id.Name}
	err = tx.QueryRowContext(ctx, `SELECT id FROM users WHERE google_sub = ?`, id.Subject).Scan(&u.ID)
	created := errors.Is(err, sql.ErrNoRows)
	switch {
	case err != nil && !created:
		return User{}, false, err
	case created:
		var n int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM users`).Scan(&n); err != nil {
			return User{}, false, err
		}
		if n >= maxUsers {
			return User{}, false, ErrFull
		}
		if u.ID, err = randomHex(16); err != nil {
			return User{}, false, err
		}
		u.CreatedAt = a.now().UTC()
		_, err = tx.ExecContext(ctx, `INSERT INTO users (id, google_sub, email, name, created_at) VALUES (?, ?, ?, ?, ?)`,
			u.ID, u.GoogleSub, u.Email, u.Name, u.CreatedAt.Format(timeLayout))
	default:
		_, err = tx.ExecContext(ctx, `UPDATE users SET email = ?, name = ? WHERE id = ?`, u.Email, u.Name, u.ID)
	}
	if err != nil {
		return User{}, false, err
	}
	if !created {
		// Re-read so CreatedAt is the stored value.
		if u, err = userByID(ctx, tx, u.ID); err != nil {
			return User{}, false, err
		}
	}
	return u, created, tx.Commit()
}

type queryer interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

func userByID(ctx context.Context, q queryer, id string) (User, error) {
	var u User
	var created string
	err := q.QueryRowContext(ctx, `SELECT id, google_sub, email, name, created_at FROM users WHERE id = ?`, id).
		Scan(&u.ID, &u.GoogleSub, &u.Email, &u.Name, &created)
	if err != nil {
		return User{}, err
	}
	u.CreatedAt, err = time.Parse(timeLayout, created)
	return u, err
}

// CreateSession returns a new session token. Only its hash is stored.
func (a *Accounts) CreateSession(ctx context.Context, userID string) (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	token := base64.RawURLEncoding.EncodeToString(raw)
	_, err := a.db.ExecContext(ctx, `INSERT INTO sessions (token_hash, user_id, expires_at) VALUES (?, ?, ?)`,
		hashToken(token), userID, a.now().UTC().Add(sessionLife).Format(timeLayout))
	if err != nil {
		return "", err
	}
	return token, nil
}

// UserForSession returns the session's user, or ErrNoSession if the token is unknown or expired.
func (a *Accounts) UserForSession(ctx context.Context, token string) (User, error) {
	var userID, expires string
	err := a.db.QueryRowContext(ctx, `SELECT user_id, expires_at FROM sessions WHERE token_hash = ?`, hashToken(token)).
		Scan(&userID, &expires)
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, ErrNoSession
	}
	if err != nil {
		return User{}, err
	}
	// Fixed-width UTC timestamps compare correctly as strings.
	if expires <= a.now().UTC().Format(timeLayout) {
		return User{}, ErrNoSession
	}
	return userByID(ctx, a.db, userID)
}

func (a *Accounts) DeleteSession(ctx context.Context, token string) error {
	_, err := a.db.ExecContext(ctx, `DELETE FROM sessions WHERE token_hash = ?`, hashToken(token))
	return err
}

// DeleteUser removes the account and, by cascade, its sessions.
func (a *Accounts) DeleteUser(ctx context.Context, id string) error {
	_, err := a.db.ExecContext(ctx, `DELETE FROM users WHERE id = ?`, id)
	return err
}

func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func randomHex(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
