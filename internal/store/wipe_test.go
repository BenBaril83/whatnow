package store_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/jameynakama/whatnow/internal/store"
)

func TestWipeEmptiesEveryTableButKeepsTheSchema(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "w.db")
	db, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	// A blocker references tasks, so the wipe has to get past foreign keys in either order.
	for _, q := range []string{
		`INSERT INTO tasks (id, title, raw_text, created_by, created_at, updated_at) VALUES (1, 'a', 'a', 'user', 'x', 'x'), (2, 'b', 'b', 'user', 'x', 'x')`,
		`INSERT INTO blockers (task_id, blocked_by_id) VALUES (1, 2)`,
		`INSERT INTO batches (actor, created_at) VALUES ('user', 'x')`,
		`INSERT INTO settings (id, body) VALUES (1, '{}')`,
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}

	if err := store.Wipe(ctx, db); err != nil {
		t.Fatal(err)
	}

	rows, err := db.Query(`SELECT name FROM sqlite_master WHERE type = 'table' AND name NOT LIKE 'sqlite_%'`)
	if err != nil {
		t.Fatal(err)
	}
	var tables []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatal(err)
		}
		tables = append(tables, name)
	}
	rows.Close()
	for _, table := range tables {
		var n int
		if err := db.QueryRow(`SELECT COUNT(*) FROM "` + table + `"`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if table == "schema_migrations" && n == 0 {
			t.Error("the migration record was wiped, so the next open would re-run every migration")
		}
		if table != "schema_migrations" && n != 0 {
			t.Errorf("%s still has %d rows", table, n)
		}
	}
	db.Close()

	db, err = store.Open(path)
	if err != nil {
		t.Fatalf("reopen after wipe: %v", err)
	}
	db.Close()
}
