package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/charmbracelet/x/term"

	"github.com/jameynakama/whatnow/internal/integrations"
	"github.com/jameynakama/whatnow/internal/integrations/jira"
	"github.com/jameynakama/whatnow/internal/secrets"
)

const jiraUsage = "usage: whatnow jira setup   save where to search and where to send, then send\n       whatnow jira push    send your Jira search to hosted whatnow"

// pushConfig is where `whatnow jira push` searches and sends. The key and the Jira token are in
// the keychain, never in this file.
type pushConfig struct {
	Whatnow string `json:"whatnow"`
	Jira    string `json:"jira"`
	JQL     string `json:"jql"`
}

const (
	pushConfigFile = "jira-push.json"
	keySecret      = "jira-push/key"
	tokenSecret    = "jira-push/token"
)

// runJira runs `whatnow jira`, which sends tickets from a computer that can reach Jira to a hosted
// whatnow that can't. dir holds the config file.
func runJira(ctx context.Context, args []string, in io.Reader, out io.Writer, dir string, vault secrets.Store, client *http.Client) error {
	if len(args) != 1 || (args[0] != "setup" && args[0] != "push") {
		fmt.Fprintln(out, jiraUsage)
		return errors.New("unknown jira command")
	}
	if args[0] == "setup" {
		return setup(ctx, in, out, dir, vault, client)
	}
	cfg, err := loadPushConfig(dir)
	if errors.Is(err, os.ErrNotExist) {
		return errors.New("not set up yet: run `whatnow jira setup`")
	}
	if err != nil {
		return err
	}
	key, err := vault.Get(ctx, keySecret)
	if err != nil {
		return err
	}
	token, err := vault.Get(ctx, tokenSecret)
	if err != nil {
		return err
	}
	return push(ctx, cfg, key, token, client, out)
}

// setup asks for each setting, offering what's saved, saves them, and sends once so a typo shows.
func setup(ctx context.Context, in io.Reader, out io.Writer, dir string, vault secrets.Store, client *http.Client) error {
	cfg, err := loadPushConfig(dir)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if cfg.Whatnow == "" {
		cfg.Whatnow = "https://whatnow.day"
	}
	key, err := vault.Get(ctx, keySecret)
	if err != nil {
		return err
	}
	token, err := vault.Get(ctx, tokenSecret)
	if err != nil {
		return err
	}
	lines := bufio.NewScanner(in)
	// Typed at a terminal, the key and the token don't echo, so they stay off the screen.
	tty, _ := in.(*os.File)
	if tty != nil && !term.IsTerminal(tty.Fd()) {
		tty = nil
	}
	ask := func(prompt, current string, secret bool) string {
		switch {
		case secret && current != "":
			fmt.Fprintf(out, "%s (saved; enter to keep): ", prompt)
		case current != "":
			fmt.Fprintf(out, "%s [%s]: ", prompt, current)
		default:
			fmt.Fprintf(out, "%s: ", prompt)
		}
		if secret && tty != nil {
			b, err := term.ReadPassword(tty.Fd())
			fmt.Fprintln(out)
			if v := strings.TrimSpace(string(b)); err == nil && v != "" {
				return v
			}
			return current
		}
		if lines.Scan() {
			if v := strings.TrimSpace(lines.Text()); v != "" {
				return v
			}
		}
		return current
	}
	cfg.Whatnow = ask("Your whatnow address", cfg.Whatnow, false)
	key = ask("The key from Jira in whatnow settings", key, true)
	cfg.Jira = ask("Your Jira's address", cfg.Jira, false)
	token = ask("Your Jira personal access token", token, true)
	cfg.JQL = ask("Search (JQL)", cfg.JQL, false)

	if err := vault.Set(ctx, keySecret, key); err != nil {
		return err
	}
	if err := vault.Set(ctx, tokenSecret, token); err != nil {
		return err
	}
	b, err := json.Marshal(cfg)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, pushConfigFile), b, 0o600); err != nil {
		return err
	}
	return push(ctx, cfg, key, token, client, out)
}

func loadPushConfig(dir string) (pushConfig, error) {
	var cfg pushConfig
	b, err := os.ReadFile(filepath.Join(dir, pushConfigFile))
	if err != nil {
		return cfg, err
	}
	return cfg, json.Unmarshal(b, &cfg)
}

// push runs the Jira search and sends what it finds. Its output and errors never hold the key or
// the token.
func push(ctx context.Context, cfg pushConfig, key, token string, client *http.Client, out io.Writer) error {
	src := jira.New(client).Pull(ctx, integrations.Config{Values: map[string]string{"url": cfg.Jira, "token": token, "jql": cfg.JQL}}, time.Time{}, time.Time{})[0]
	if src.Err != nil {
		return src.Err
	}
	base, err := url.Parse(strings.TrimRight(cfg.Whatnow, "/"))
	if err != nil || base.Host == "" {
		return errors.New("your whatnow address isn't a URL, such as https://whatnow.day")
	}
	body, err := json.Marshal(map[string]any{"tickets": src.Tickets, "more": src.More})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base.String()+"/api/v1/integrations/jira/push", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+key)
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("Can't reach whatnow at %s", base.Host)
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusNoContent:
	case http.StatusUnauthorized:
		return errors.New("whatnow didn't accept the key — create a new one in Jira settings")
	case http.StatusConflict:
		// whatnow says which: Jira is off, or set to reach Jira directly.
		var body struct {
			Error string `json:"error"`
		}
		if json.NewDecoder(resp.Body).Decode(&body) == nil && body.Error != "" {
			msg, _ := strings.CutPrefix(body.Error, "conflict: ")
			return errors.New(msg)
		}
		return errors.New("couldn't read whatnow's answer — is the address right?")
	default:
		return errors.New("couldn't read whatnow's answer — is the address right?")
	}
	fmt.Fprintf(out, "Sent %d tickets to %s\n", len(src.Tickets), base.Host)
	if src.More > 0 {
		fmt.Fprintf(out, "%d more matched — narrow the search\n", src.More)
	}
	return nil
}
