package host

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jameynakama/whatnow/internal/app"
	"github.com/jameynakama/whatnow/internal/llm"
	"github.com/jameynakama/whatnow/internal/llm/fake"
	"github.com/jameynakama/whatnow/internal/settings"
)

const idle = 15 * time.Minute

// clock is a settable time source that request goroutines can read while the test moves it.
type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *clock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

// opener opens real hosted Apps, counts calls per user, and serves them with handler.
type opener struct {
	mu      sync.Mutex
	calls   map[string]int
	paths   map[string]string
	handler func(*app.App) http.Handler
}

func newOpener(handler func(*app.App) http.Handler) *opener {
	return &opener{calls: map[string]int{}, paths: map[string]string{}, handler: handler}
}

func (o *opener) open(userID, dbPath string) (*app.App, http.Handler, error) {
	o.mu.Lock()
	o.calls[userID]++
	o.paths[userID] = dbPath
	o.mu.Unlock()
	a, err := app.Open(app.Options{DBPath: dbPath, NewClient: fakeClient, Now: time.Now, Loc: time.UTC, Hosted: &app.Hosted{}})
	if err != nil {
		return nil, nil, err
	}
	return a, o.handler(a), nil
}

func (o *opener) count(userID string) int {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.calls[userID]
}

func fakeClient(context.Context, settings.Clients, settings.Settings) (llm.Client, settings.ProviderConfig, error) {
	return fake.Client{}, settings.ProviderConfig{}, nil
}

func ok(*app.App) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})
}

func newRegistry(t *testing.T, open func(string, string) (*app.App, http.Handler, error), c *clock) *Registry {
	t.Helper()
	r := NewRegistry(t.TempDir(), open, idle, c.now)
	t.Cleanup(r.Close)
	return r
}

func serve(r *Registry, userID string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	r.Serve(w, httptest.NewRequest(http.MethodGet, "/", nil), userID)
	return w
}

func TestServeOpensOnce(t *testing.T) {
	o := newOpener(ok)
	r := newRegistry(t, o.open, &clock{t: *start()})

	var wg sync.WaitGroup
	for range 20 {
		wg.Go(func() { serve(r, "u1") })
	}
	wg.Wait()
	if n := o.count("u1"); n != 1 {
		t.Fatalf("open called %d times, want 1", n)
	}
}

func TestIdleCloses(t *testing.T) {
	c := &clock{t: *start()}
	o := newOpener(func(a *app.App) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodPost {
				if _, err := a.DB.Exec(`CREATE TABLE probe (x TEXT); INSERT INTO probe VALUES ('kept')`); err != nil {
					http.Error(w, err.Error(), http.StatusInternalServerError)
				}
				return
			}
			var x string
			if err := a.DB.QueryRow(`SELECT x FROM probe`).Scan(&x); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			w.Write([]byte(x))
		})
	})
	r := newRegistry(t, o.open, c)

	w := httptest.NewRecorder()
	r.Serve(w, httptest.NewRequest(http.MethodPost, "/", nil), "u1")
	if w.Code != http.StatusOK {
		t.Fatalf("write: %d %s", w.Code, w.Body)
	}
	c.advance(idle - time.Second)
	r.reap()
	if got := serve(r, "u1"); got.Body.String() != "kept" || o.count("u1") != 1 {
		t.Fatalf("before idle: body %q, opens %d; want the same App still open", got.Body, o.count("u1"))
	}

	c.advance(idle)
	r.reap()
	got := serve(r, "u1")
	if n := o.count("u1"); n != 2 {
		t.Fatalf("open called %d times, want 2 after the idle App closed", n)
	}
	if got.Body.String() != "kept" {
		t.Fatalf("after reopen: %d %q, want the user's data still there", got.Code, got.Body)
	}
}

func TestOpenRequestKeepsAppAlive(t *testing.T) {
	c := &clock{t: *start()}
	entered, release := make(chan struct{}), make(chan struct{})
	o := newOpener(func(a *app.App) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodPost {
				close(entered)
				<-release
			}
		})
	})
	r := newRegistry(t, o.open, c)

	done := make(chan struct{})
	go func() {
		defer close(done)
		r.Serve(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/", nil), "u1")
	}()
	<-entered
	c.advance(2 * idle)
	r.reap()
	close(release)
	<-done

	serve(r, "u1")
	if n := o.count("u1"); n != 1 {
		t.Fatalf("open called %d times, want 1: the App closed under an open request", n)
	}
}

func TestEvictEndsStream(t *testing.T) {
	for name, stop := range map[string]func(*Registry){
		"evict": func(r *Registry) { r.Evict("u1") },
		"close": func(r *Registry) { r.Close() },
	} {
		t.Run(name, func(t *testing.T) {
			entered := make(chan struct{})
			var ended atomic.Bool
			o := newOpener(func(a *app.App) http.Handler {
				return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.Method == http.MethodPost {
						close(entered)
						<-r.Context().Done()
						ended.Store(true)
					}
				})
			})
			r := newRegistry(t, o.open, &clock{t: *start()})

			go r.Serve(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/", nil), "u1")
			<-entered
			stop(r)
			if !ended.Load() {
				t.Fatal("returned before the stream ended")
			}
			serve(r, "u1")
			if n := o.count("u1"); n != 2 {
				t.Fatalf("open called %d times, want 2: the next request should reopen", n)
			}
		})
	}
}

func TestEvictWithoutEntry(t *testing.T) {
	r := newRegistry(t, newOpener(ok).open, &clock{t: *start()})
	r.Evict("nobody")
}

func TestUsersAreSeparate(t *testing.T) {
	o := newOpener(ok)
	r := newRegistry(t, o.open, &clock{t: *start()})

	serve(r, "u1")
	serve(r, "u2")
	if o.count("u1") != 1 || o.count("u2") != 1 {
		t.Fatalf("opens: u1=%d u2=%d, want 1 each", o.count("u1"), o.count("u2"))
	}
	if o.paths["u1"] == o.paths["u2"] {
		t.Fatalf("both users got %s", o.paths["u1"])
	}
	if want := filepath.Join(r.dir, "users", "u1.db"); o.paths["u1"] != want {
		t.Fatalf("u1 path %s, want %s", o.paths["u1"], want)
	}
}

func TestFailedOpenRetries(t *testing.T) {
	o := newOpener(ok)
	fail := true
	r := newRegistry(t, func(userID, dbPath string) (*app.App, http.Handler, error) {
		if fail {
			fail = false
			return nil, nil, errors.New("disk on fire")
		}
		return o.open(userID, dbPath)
	}, &clock{t: *start()})

	if w := serve(r, "u1"); w.Code != http.StatusInternalServerError {
		t.Fatalf("failed open: status %d, want 500", w.Code)
	}
	if w := serve(r, "u1"); w.Code != http.StatusOK || o.count("u1") != 1 {
		t.Fatalf("retry: status %d, opens %d; want a fresh open", w.Code, o.count("u1"))
	}
}

func TestSlowOpenDoesNotBlockOtherUsers(t *testing.T) {
	o := newOpener(ok)
	entered, release := make(chan struct{}), make(chan struct{})
	r := newRegistry(t, func(userID, dbPath string) (*app.App, http.Handler, error) {
		if userID == "slow" {
			close(entered)
			<-release
		}
		return o.open(userID, dbPath)
	}, &clock{t: *start()})

	done := make(chan struct{})
	go func() {
		defer close(done)
		serve(r, "slow")
	}()
	<-entered
	serve(r, "fast")
	close(release)
	<-done
}

// noDatabase fails the test if any of the user's database files exist.
func noDatabase(t *testing.T, r *Registry, userID string) {
	t.Helper()
	for _, suffix := range []string{"", "-wal", "-shm"} {
		if _, err := os.Stat(r.DBPath(userID) + suffix); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("database%s stat: %v, want none", suffix, err)
		}
	}
}

func TestDeleteRemovesForGood(t *testing.T) {
	o := newOpener(ok)
	r := newRegistry(t, o.open, &clock{t: *start()})
	serve(r, "u1")
	serve(r, "u2")

	if err := r.Delete("u1"); err != nil {
		t.Fatal(err)
	}
	noDatabase(t, r, "u1")
	if w := serve(r, "u1"); w.Code != http.StatusUnauthorized || o.count("u1") != 1 {
		t.Fatalf("after delete: status %d, opens %d; want 401 without opening", w.Code, o.count("u1"))
	}
	noDatabase(t, r, "u1")
	if w := serve(r, "u2"); w.Code != http.StatusOK || o.count("u2") != 1 {
		t.Fatalf("other user: status %d, opens %d; want their App still open", w.Code, o.count("u2"))
	}
}

func TestDeleteDuringOpen(t *testing.T) {
	o := newOpener(ok)
	entered, release := make(chan struct{}), make(chan struct{})
	var first sync.Once
	r := newRegistry(t, func(userID, dbPath string) (*app.App, http.Handler, error) {
		first.Do(func() { close(entered) })
		<-release
		return o.open(userID, dbPath)
	}, &clock{t: *start()})

	served := make(chan *httptest.ResponseRecorder)
	go func() { served <- serve(r, "u1") }()
	<-entered
	deleted := make(chan error)
	go func() { deleted <- r.Delete("u1") }()
	// Let the open finish only once the user is marked deleted, as a request that passed the
	// session check just before the account went would.
	for {
		r.mu.Lock()
		marked := r.deleted["u1"]
		r.mu.Unlock()
		if marked {
			break
		}
		runtime.Gosched()
	}
	close(release)

	if w := <-served; w.Code != http.StatusUnauthorized {
		t.Fatalf("request racing the delete: status %d, want 401", w.Code)
	}
	if err := <-deleted; err != nil {
		t.Fatal(err)
	}
	noDatabase(t, r, "u1")
	if n := o.count("u1"); n != 1 {
		t.Fatalf("open called %d times, want only the one in flight", n)
	}
}
