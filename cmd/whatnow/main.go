// Command whatnow runs the whatnow server.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/jameynakama/whatnow/internal/agent"
	"github.com/jameynakama/whatnow/internal/cleanup"
	"github.com/jameynakama/whatnow/internal/httpapi"
	"github.com/jameynakama/whatnow/internal/llm"
	"github.com/jameynakama/whatnow/internal/planner"
	"github.com/jameynakama/whatnow/internal/service"
	"github.com/jameynakama/whatnow/internal/settings"
	"github.com/jameynakama/whatnow/internal/store"
	"github.com/jameynakama/whatnow/internal/stream"
)

func main() {
	if len(os.Args) < 2 || os.Args[1] != "serve" {
		fmt.Fprintln(os.Stderr, "usage: whatnow serve [-addr 127.0.0.1:7777] [-db PATH]")
		os.Exit(2)
	}
	flags := flag.NewFlagSet("serve", flag.ExitOnError)
	addr := flags.String("addr", "127.0.0.1:7777", "listen address; keep it on loopback")
	dbPath := flags.String("db", "", "database path (default <user config dir>/whatnow/whatnow.db)")
	flags.Parse(os.Args[2:])

	if err := serve(*addr, *dbPath); err != nil {
		log.Fatal(err)
	}
}

func serve(addr, dbPath string) error {
	if dbPath == "" {
		dir, err := os.UserConfigDir()
		if err != nil {
			return err
		}
		dbPath = filepath.Join(dir, "whatnow", "whatnow.db")
	}
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o700); err != nil {
		return err
	}
	db, err := store.Open(dbPath)
	if err != nil {
		return err
	}
	defer db.Close()

	svc := service.New(db, time.Now)
	hub := stream.NewHub()
	active := func(ctx context.Context) (llm.Client, settings.ProviderConfig, settings.Settings, error) {
		return settings.Active(ctx, db)
	}
	runner := cleanup.New(svc, hub, active, time.Local, time.Now)
	go runner.Run(context.Background())
	plans := planner.New(svc, hub, active, time.Local, time.Now)
	watched, _ := hub.Subscribe("batch.applied", "batch.undone") // for the life of the process
	go plans.Run(context.Background(), watched)

	handler := httpapi.New(httpapi.Deps{
		Service:   svc,
		Hub:       hub,
		DB:        db,
		Loc:       time.Local,
		OnCapture: runner.Enqueue,
		NewClient: settings.NewClient,
		Planner:   plans,
		Agent:     agent.New(svc, hub, plans, time.Local, time.Now),
	})
	// No WriteTimeout: it would cut off the long-lived SSE stream.
	srv := &http.Server{Addr: addr, Handler: handler, ReadHeaderTimeout: 5 * time.Second}
	log.Printf("whatnow listening on http://%s (db %s)", addr, dbPath)
	return srv.ListenAndServe()
}
