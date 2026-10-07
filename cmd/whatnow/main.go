// Command whatnow runs the whatnow server, or the terminal companion.
package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/jameynakama/whatnow/internal/app"
	"github.com/jameynakama/whatnow/internal/httpapi"
	"github.com/jameynakama/whatnow/internal/settings"
	"github.com/jameynakama/whatnow/internal/tui"
	"github.com/jameynakama/whatnow/internal/web"
)

// newClient makes the model client. The e2e build swaps in the fake.
var newClient = settings.NewClient

const defaultAddr = "127.0.0.1:7777"

const usage = "usage: whatnow serve [-addr 127.0.0.1:7777] [-db PATH]\n       whatnow tui [-db PATH]"

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, usage)
		os.Exit(2)
	}
	flags := flag.NewFlagSet(os.Args[1], flag.ExitOnError)
	dbPath := flags.String("db", "", "database path (default <user config dir>/whatnow/whatnow.db)")
	var err error
	switch os.Args[1] {
	case "serve":
		addr := flags.String("addr", defaultAddr, "listen address; keep it on loopback")
		flags.Parse(os.Args[2:])
		err = serve(*addr, *dbPath)
	case "tui":
		flags.Parse(os.Args[2:])
		err = runTUI(*dbPath)
	default:
		fmt.Fprintln(os.Stderr, usage)
		os.Exit(2)
	}
	if err != nil {
		log.Fatal(err)
	}
}

func open(dbPath string) (*app.App, error) {
	return app.Open(app.Options{DBPath: dbPath, NewClient: newClient, Now: time.Now, Loc: time.Local})
}

func serve(addr, dbPath string) error {
	a, err := open(dbPath)
	if err != nil {
		return err
	}
	defer a.Close()

	handler := httpapi.New(httpapi.Deps{
		Service:   a.Service,
		Hub:       a.Hub,
		DB:        a.DB,
		Loc:       a.Loc,
		OnCapture: a.Cleanup.Enqueue,
		Clean:     a.Cleanup.Clean,
		NewClient: newClient,
		Planner:   a.Planner,
		Agent:     a.Agent,
		Static:    web.Handler(),
	})
	// No WriteTimeout: it would cut off the long-lived SSE stream.
	srv := &http.Server{Addr: addr, Handler: handler, ReadHeaderTimeout: 5 * time.Second}
	log.Printf("whatnow listening on http://%s (db %s)", addr, a.DBPath)
	return srv.ListenAndServe()
}

func runTUI(dbPath string) error {
	a, err := open(dbPath)
	if err != nil {
		return err
	}
	defer a.Close()
	// Background work logs as it goes. Under the TUI, those lines would land on the screen.
	logs, err := tea.LogToFile(filepath.Join(filepath.Dir(a.DBPath), "tui.log"), "")
	if err != nil {
		return err
	}
	defer logs.Close()
	return tui.Run(a, "http://"+defaultAddr)
}
