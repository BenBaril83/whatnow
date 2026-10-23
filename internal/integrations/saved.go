package integrations

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
)

// saved is an integration's settings as stored. Values and row values hold text fields only;
// secrets are in the secrets store.
type saved struct {
	Enabled bool              `json:"enabled"`
	Values  map[string]string `json:"values"`
	Lists   map[string][]Row  `json:"lists"`
	Skipped []Skip            `json:"skipped"`
	// Via is how the integration is reached; empty is ViaDirect.
	Via    string  `json:"via,omitempty"`
	Pushed *Pushed `json:"pushed,omitempty"`
}

// Skip is a series, or any pulled item, the user chose never to see. Title is only for showing.
type Skip struct {
	Key   string `json:"key"`
	Title string `json:"title"`
}

func load(ctx context.Context, db *sql.DB, id string) (saved, error) {
	var s saved
	var body string
	err := db.QueryRowContext(ctx, `SELECT body FROM integrations WHERE id = ?`, id).Scan(&body)
	if errors.Is(err, sql.ErrNoRows) {
		return s, nil
	}
	if err != nil {
		return s, err
	}
	return s, json.Unmarshal([]byte(body), &s)
}

func store(ctx context.Context, db *sql.DB, id string, s saved) error {
	body, err := json.Marshal(s)
	if err != nil {
		return err
	}
	_, err = db.ExecContext(ctx,
		`INSERT INTO integrations (id, body) VALUES (?, ?) ON CONFLICT (id) DO UPDATE SET body = excluded.body`,
		id, string(body))
	return err
}

func newRowID() string {
	b := make([]byte, 6)
	rand.Read(b)
	return hex.EncodeToString(b)
}

// account names a secret in the store: one of the integration's own fields, or a row's.
func account(id, rowID, key string) string {
	if rowID == "" {
		return "integration/" + id + "/" + key
	}
	return "integration/" + id + "/" + rowID + "/" + key
}

// DeleteSecrets removes secrets from the store. One that isn't there is fine.
func (r *Runner) DeleteSecrets(ctx context.Context, accounts []string) error {
	var errs []error
	for _, acct := range accounts {
		if err := r.vault.Delete(ctx, acct); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}
