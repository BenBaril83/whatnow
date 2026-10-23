package host

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/jameynakama/whatnow/internal/app"
)

// Registry keeps one open App per signed-in user, opening it on a user's first request and
// closing it once no request has been in flight for the idle timeout.
type Registry struct {
	dir  string
	open func(userID, dbPath string) (*app.App, http.Handler, error)
	idle time.Duration
	now  func() time.Time

	mu      sync.Mutex
	entries map[string]*entry
	// deleted are users whose data is gone. Their Apps are never opened again, so a request that
	// was already past the session check can't recreate the database.
	deleted map[string]bool
}

// errDeleted means the user was deleted.
var errDeleted = errors.New("host: user deleted")

// entry is one user's App. Every field but ready and requests is guarded by Registry.mu, and
// app, handler, and err are set before ready closes. An entry leaves the map before it is shut,
// so no request joins requests once shut has begun waiting on it.
type entry struct {
	ready     chan struct{}
	app       *app.App
	handler   http.Handler
	err       error
	inFlight  int
	idleSince time.Time
	ctx       context.Context
	cancel    context.CancelFunc
	requests  sync.WaitGroup
}

// NewRegistry keeps user databases under dir. open builds a user's App and its handler.
func NewRegistry(dir string, open func(userID, dbPath string) (*app.App, http.Handler, error), idle time.Duration, now func() time.Time) *Registry {
	return &Registry{dir: dir, open: open, idle: idle, now: now, entries: map[string]*entry{}, deleted: map[string]bool{}}
}

// DBPath is where the user's database lives.
func (r *Registry) DBPath(userID string) string {
	return filepath.Join(r.dir, "users", userID+".db")
}

// Serve runs req against the user's App, opening it if needed. The request's context is also
// cancelled when the App is evicted, so a long-lived stream ends with it.
func (r *Registry) Serve(w http.ResponseWriter, req *http.Request, userID string) {
	e, err := r.acquire(userID)
	if errors.Is(err, errDeleted) {
		http.Error(w, "sign in first", http.StatusUnauthorized)
		return
	}
	if err != nil {
		log.Printf("open app for %s: %v", userID, err)
		http.Error(w, "could not open your planner", http.StatusInternalServerError)
		return
	}
	defer r.release(e)

	ctx, cancel := context.WithCancel(req.Context())
	defer cancel()
	defer context.AfterFunc(e.ctx, cancel)()
	e.handler.ServeHTTP(w, req.WithContext(ctx))
}

// acquire returns the user's open entry with this request counted in flight.
func (r *Registry) acquire(userID string) (*entry, error) {
	for {
		r.mu.Lock()
		if r.deleted[userID] {
			r.mu.Unlock()
			return nil, errDeleted
		}
		e := r.entries[userID]
		if e == nil {
			e = &entry{ready: make(chan struct{})}
			r.entries[userID] = e
			r.mu.Unlock()
			r.load(userID, e)
		} else {
			r.mu.Unlock()
		}
		<-e.ready

		r.mu.Lock()
		if e.err != nil {
			r.mu.Unlock()
			return nil, e.err
		}
		// Evicted or reaped between opening and now: start over with a fresh entry.
		if r.entries[userID] != e {
			r.mu.Unlock()
			continue
		}
		e.inFlight++
		e.requests.Add(1)
		r.mu.Unlock()
		return e, nil
	}
}

// load opens the App outside the lock, so a slow open holds up only this user's requests.
func (r *Registry) load(userID string, e *entry) {
	a, h, err := r.open(userID, r.DBPath(userID))
	r.mu.Lock()
	defer r.mu.Unlock()
	defer close(e.ready)
	if err != nil {
		e.err = err
		if r.entries[userID] == e {
			delete(r.entries, userID)
		}
		return
	}
	e.app, e.handler = a, h
	e.ctx, e.cancel = context.WithCancel(context.Background())
	e.idleSince = r.now()
}

func (r *Registry) release(e *entry) {
	r.mu.Lock()
	e.inFlight--
	if e.inFlight == 0 {
		e.idleSince = r.now()
	}
	r.mu.Unlock()
	e.requests.Done()
}

// Evict cancels the user's in-flight requests, waits for them, and closes the App. The next
// Serve reopens it.
func (r *Registry) Evict(userID string) {
	r.mu.Lock()
	e := r.entries[userID]
	delete(r.entries, userID)
	r.mu.Unlock()
	if e != nil {
		shut(e)
	}
}

// EvictIfMoved evicts the user's App when the time zone saved in its database is no longer the
// one it opened with, since an App's zone is fixed when it opens. With no App open, or one still
// opening, there is nothing to do: the next one opens with the saved zone.
func (r *Registry) EvictIfMoved(ctx context.Context, userID string) {
	r.mu.Lock()
	var a *app.App
	if e := r.entries[userID]; e != nil {
		a = e.app
	}
	r.mu.Unlock()
	if a != nil && app.SavedLoc(ctx, a.DB).String() != a.Loc.String() {
		r.Evict(userID)
	}
}

// Delete closes the user's App for good, as Evict does, and removes the database. No later
// Serve opens it again.
func (r *Registry) Delete(userID string) error {
	r.mu.Lock()
	r.deleted[userID] = true
	e := r.entries[userID]
	delete(r.entries, userID)
	r.mu.Unlock()
	if e != nil {
		shut(e)
	}
	path := r.DBPath(userID)
	var errs []error
	for _, p := range []string{path, path + "-wal", path + "-shm"} {
		if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// Run closes idle Apps every minute until ctx is done.
func (r *Registry) Run(ctx context.Context) {
	tick := time.NewTicker(time.Minute)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			r.reap()
		}
	}
}

// reap closes every App with no request in flight for the idle timeout.
func (r *Registry) reap() {
	r.mu.Lock()
	var idle []*entry
	for id, e := range r.entries {
		if e.app != nil && e.inFlight == 0 && r.now().Sub(e.idleSince) >= r.idle {
			idle = append(idle, e)
			delete(r.entries, id)
		}
	}
	r.mu.Unlock()
	for _, e := range idle {
		shut(e)
	}
}

// Close cancels every in-flight request and closes every App.
func (r *Registry) Close() {
	r.mu.Lock()
	all := r.entries
	r.entries = map[string]*entry{}
	r.mu.Unlock()
	for _, e := range all {
		shut(e)
	}
}

// shut ends an entry already removed from the map: it cancels its requests, waits for them,
// and closes the App. An entry still opening is shut once its open finishes.
func shut(e *entry) {
	<-e.ready
	if e.err != nil {
		return
	}
	e.cancel()
	e.requests.Wait()
	if err := e.app.Close(); err != nil {
		log.Printf("close app %s: %v", e.app.DBPath, err)
	}
}
