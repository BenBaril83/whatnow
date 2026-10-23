// Package secrets stores API keys and integration secrets: in the OS keychain locally, sealed in
// the user's database when hosted.
package secrets

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"

	"github.com/zalando/go-keyring"
)

const keychainService = "whatnow"

// Store holds named secrets. Get returns "" for a name that isn't set, and deleting a missing name
// is not an error.
type Store interface {
	Get(ctx context.Context, name string) (string, error)
	Set(ctx context.Context, name, value string) error
	Delete(ctx context.Context, name string) error
}

// Keychain is a Store over the OS keychain.
type Keychain struct{}

func (Keychain) Get(_ context.Context, name string) (string, error) {
	v, err := keyring.Get(keychainService, name)
	if errors.Is(err, keyring.ErrNotFound) {
		return "", nil
	}
	return v, err
}

func (Keychain) Set(_ context.Context, name, value string) error {
	return keyring.Set(keychainService, name, value)
}

func (Keychain) Delete(_ context.Context, name string) error {
	err := keyring.Delete(keychainService, name)
	if errors.Is(err, keyring.ErrNotFound) {
		return nil
	}
	return err
}

// Sealed is a Store that keeps AES-GCM sealed rows in the secrets table.
type Sealed struct {
	db  *sql.DB
	gcm cipher.AEAD
}

func NewSealed(db *sql.DB, key [32]byte) *Sealed {
	block, err := aes.NewCipher(key[:])
	if err != nil {
		panic(err) // unreachable: the key is always 32 bytes
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		panic(err) // unreachable: AES has a 16-byte block
	}
	return &Sealed{db: db, gcm: gcm}
}

func (s *Sealed) Get(ctx context.Context, name string) (string, error) {
	var sealed []byte
	err := s.db.QueryRowContext(ctx, `SELECT sealed FROM secrets WHERE name = ?`, name).Scan(&sealed)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	n := s.gcm.NonceSize()
	if len(sealed) < n {
		return "", fmt.Errorf("secret %q is truncated", name)
	}
	plain, err := s.gcm.Open(nil, sealed[:n], sealed[n:], []byte(name))
	if err != nil {
		return "", fmt.Errorf("open secret %q: %w", name, err)
	}
	return string(plain), nil
}

// Set seals the value with the name as additional data, so a row can't be moved onto another name.
func (s *Sealed) Set(ctx context.Context, name, value string) error {
	nonce := make([]byte, s.gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return err
	}
	sealed := s.gcm.Seal(nonce, nonce, []byte(value), []byte(name))
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO secrets (name, sealed) VALUES (?, ?)
		 ON CONFLICT(name) DO UPDATE SET sealed = excluded.sealed`, name, sealed)
	return err
}

func (s *Sealed) Delete(ctx context.Context, name string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM secrets WHERE name = ?`, name)
	return err
}

// ParseKey decodes a seal key: standard base64 of exactly 32 bytes.
func ParseKey(s string) ([32]byte, error) {
	var key [32]byte
	raw, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		return key, fmt.Errorf("seal key is not base64: %w", err)
	}
	if len(raw) != len(key) {
		return key, fmt.Errorf("seal key is %d bytes, want %d", len(raw), len(key))
	}
	copy(key[:], raw)
	return key, nil
}
