package store_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/jameynakama/whatnow/internal/store"
)

func TestOpenCreatesSchema(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "w.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	for _, table := range []string{"tasks", "blockers", "events", "batches", "activity", "settings"} {
		var n int
		err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?`, table).Scan(&n)
		if err != nil {
			t.Fatal(err)
		}
		if n != 1 {
			t.Errorf("table %q missing", table)
		}
	}
}

func TestOpenTwiceKeepsData(t *testing.T) {
	path := filepath.Join(t.TempDir(), "w.db")
	db, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO batches (actor, created_at) VALUES ('user', 'x')`); err != nil {
		t.Fatal(err)
	}
	db.Close()

	db, err = store.Open(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer db.Close()
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM batches`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("batches = %d after reopen, want 1", n)
	}
}

func TestOpenPathWithSpaces(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "Application Support")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	db, err := store.Open(filepath.Join(dir, "w.db"))
	if err != nil {
		t.Fatalf("open path with spaces: %v", err)
	}
	db.Close()
}

func TestForeignKeysEnforced(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "w.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	_, err = db.Exec(`INSERT INTO blockers (task_id, blocked_by_id) VALUES (1, 2)`)
	if err == nil {
		t.Fatal("expected a foreign key error for blockers on missing tasks")
	}
}
