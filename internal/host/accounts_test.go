package host

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func newAccounts(t *testing.T, clock *time.Time) *Accounts {
	t.Helper()
	a, err := OpenAccounts(filepath.Join(t.TempDir(), "accounts.db"), func() time.Time { return *clock })
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { a.Close() })
	return a
}

func start() *time.Time {
	now := time.Date(2026, 10, 22, 9, 0, 0, 0, time.UTC)
	return &now
}

func TestSignInCreatesThenFinds(t *testing.T) {
	a := newAccounts(t, start())
	ctx := context.Background()

	u, created, err := a.SignIn(ctx, Identity{Subject: "g1", Email: "a@x.io", Name: "Ann"}, 10)
	if err != nil || !created {
		t.Fatalf("first sign-in: created=%v err=%v", created, err)
	}
	if len(u.ID) != 32 {
		t.Fatalf("ID %q should be 32 hex characters", u.ID)
	}

	again, created, err := a.SignIn(ctx, Identity{Subject: "g1", Email: "b@x.io", Name: "Anne"}, 10)
	if err != nil || created {
		t.Fatalf("second sign-in: created=%v err=%v", created, err)
	}
	if again.ID != u.ID || again.Name != "Anne" || again.Email != "b@x.io" {
		t.Fatalf("got %+v, want same ID with refreshed name and email", again)
	}
}

func TestSignInFull(t *testing.T) {
	a := newAccounts(t, start())
	ctx := context.Background()
	if _, _, err := a.SignIn(ctx, Identity{Subject: "g1", Email: "a@x.io", Name: "Ann"}, 1); err != nil {
		t.Fatal(err)
	}
	if _, _, err := a.SignIn(ctx, Identity{Subject: "g2", Email: "b@x.io", Name: "Bo"}, 1); !errors.Is(err, ErrFull) {
		t.Fatalf("new subject: err = %v, want ErrFull", err)
	}
	if _, created, err := a.SignIn(ctx, Identity{Subject: "g1", Email: "a@x.io", Name: "Ann"}, 1); err != nil || created {
		t.Fatalf("existing subject: created=%v err=%v", created, err)
	}
}

func TestSessionRoundTrip(t *testing.T) {
	a := newAccounts(t, start())
	ctx := context.Background()
	u, _, _ := a.SignIn(ctx, Identity{Subject: "g1", Email: "a@x.io", Name: "Ann"}, 10)

	token, err := a.CreateSession(ctx, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	got, err := a.UserForSession(ctx, token)
	if err != nil || got.ID != u.ID {
		t.Fatalf("got %+v, %v", got, err)
	}
	var n int
	if err := a.db.QueryRow(`SELECT COUNT(*) FROM sessions WHERE token_hash = ?`, token).Scan(&n); err != nil || n != 0 {
		t.Fatalf("the token itself is stored: n=%d err=%v", n, err)
	}
	if _, err := a.UserForSession(ctx, "bogus"); !errors.Is(err, ErrNoSession) {
		t.Fatalf("unknown token: err = %v, want ErrNoSession", err)
	}

	if err := a.DeleteSession(ctx, token); err != nil {
		t.Fatal(err)
	}
	if _, err := a.UserForSession(ctx, token); !errors.Is(err, ErrNoSession) {
		t.Fatalf("deleted session: err = %v, want ErrNoSession", err)
	}
}

func TestSessionExpires(t *testing.T) {
	clock := start()
	a := newAccounts(t, clock)
	ctx := context.Background()
	u, _, _ := a.SignIn(ctx, Identity{Subject: "g1", Email: "a@x.io", Name: "Ann"}, 10)
	token, _ := a.CreateSession(ctx, u.ID)

	*clock = clock.Add(89 * 24 * time.Hour)
	if _, err := a.UserForSession(ctx, token); err != nil {
		t.Fatalf("day 89: %v", err)
	}
	*clock = clock.Add(2 * 24 * time.Hour)
	if _, err := a.UserForSession(ctx, token); !errors.Is(err, ErrNoSession) {
		t.Fatalf("day 91: err = %v, want ErrNoSession", err)
	}
}

func TestDeleteUserEndsSessions(t *testing.T) {
	a := newAccounts(t, start())
	ctx := context.Background()
	u, _, _ := a.SignIn(ctx, Identity{Subject: "g1", Email: "a@x.io", Name: "Ann"}, 10)
	token, _ := a.CreateSession(ctx, u.ID)

	if err := a.DeleteUser(ctx, u.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := a.UserForSession(ctx, token); !errors.Is(err, ErrNoSession) {
		t.Fatalf("err = %v, want ErrNoSession", err)
	}
}
