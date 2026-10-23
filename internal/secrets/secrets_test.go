package secrets

import (
	"bytes"
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"path/filepath"
	"testing"

	"github.com/zalando/go-keyring"

	"github.com/jameynakama/whatnow/internal/store"
)

func openDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func randomKey(t *testing.T) [32]byte {
	t.Helper()
	var k [32]byte
	if _, err := rand.Read(k[:]); err != nil {
		t.Fatal(err)
	}
	return k
}

func mustGet(t *testing.T, s Store, name string) string {
	t.Helper()
	got, err := s.Get(context.Background(), name)
	if err != nil {
		t.Fatalf("Get(%q): %v", name, err)
	}
	return got
}

func TestSealedRoundTrip(t *testing.T) {
	ctx := context.Background()
	s := NewSealed(openDB(t), randomKey(t))

	if err := s.Set(ctx, "anthropic", "sk-1"); err != nil {
		t.Fatal(err)
	}
	if got := mustGet(t, s, "anthropic"); got != "sk-1" {
		t.Errorf("got %q, want sk-1", got)
	}
	if err := s.Set(ctx, "anthropic", "sk-2"); err != nil {
		t.Fatal(err)
	}
	if got := mustGet(t, s, "anthropic"); got != "sk-2" {
		t.Errorf("got %q, want sk-2", got)
	}
	if err := s.Delete(ctx, "anthropic"); err != nil {
		t.Fatal(err)
	}
	if got := mustGet(t, s, "anthropic"); got != "" {
		t.Errorf("got %q after delete, want empty", got)
	}
	if err := s.Delete(ctx, "anthropic"); err != nil {
		t.Errorf("deleting a missing name: %v", err)
	}
}

func TestSealedIsEncrypted(t *testing.T) {
	db := openDB(t)
	s := NewSealed(db, randomKey(t))
	if err := s.Set(context.Background(), "anthropic", "sk-1"); err != nil {
		t.Fatal(err)
	}
	var sealed []byte
	if err := db.QueryRow(`SELECT sealed FROM secrets`).Scan(&sealed); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(sealed, []byte("sk-1")) {
		t.Error("stored row contains the plaintext")
	}
}

func TestSealedWrongKey(t *testing.T) {
	db := openDB(t)
	if err := NewSealed(db, randomKey(t)).Set(context.Background(), "anthropic", "sk-1"); err != nil {
		t.Fatal(err)
	}
	got, err := NewSealed(db, randomKey(t)).Get(context.Background(), "anthropic")
	if err == nil {
		t.Errorf("got %q with the wrong key, want an error", got)
	}
}

func TestSealedRowCannotMoveToAnotherName(t *testing.T) {
	db := openDB(t)
	s := NewSealed(db, randomKey(t))
	if err := s.Set(context.Background(), "anthropic", "sk-1"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE secrets SET name = 'openai'`); err != nil {
		t.Fatal(err)
	}
	if got, err := s.Get(context.Background(), "openai"); err == nil {
		t.Errorf("got %q for a swapped row, want an error", got)
	}
}

func TestKeychainMissing(t *testing.T) {
	keyring.MockInit()
	ctx := context.Background()
	var k Keychain
	if got := mustGet(t, k, "anthropic"); got != "" {
		t.Errorf("got %q, want empty", got)
	}
	if err := k.Delete(ctx, "anthropic"); err != nil {
		t.Errorf("deleting a missing name: %v", err)
	}
}

func TestKeychainRoundTrip(t *testing.T) {
	keyring.MockInit()
	ctx := context.Background()
	var k Keychain
	if err := k.Set(ctx, "anthropic", "sk-1"); err != nil {
		t.Fatal(err)
	}
	if got := mustGet(t, k, "anthropic"); got != "sk-1" {
		t.Errorf("got %q, want sk-1", got)
	}
	if err := k.Delete(ctx, "anthropic"); err != nil {
		t.Fatal(err)
	}
	if got := mustGet(t, k, "anthropic"); got != "" {
		t.Errorf("got %q after delete, want empty", got)
	}
}

func TestParseKey(t *testing.T) {
	want := randomKey(t)
	got, err := ParseKey(base64.StdEncoding.EncodeToString(want[:]))
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Error("parsed key differs")
	}
	for name, in := range map[string]string{
		"short":      base64.StdEncoding.EncodeToString(make([]byte, 31)),
		"not base64": "!!!not base64!!!",
	} {
		if _, err := ParseKey(in); err == nil {
			t.Errorf("%s: want an error", name)
		}
	}
}
