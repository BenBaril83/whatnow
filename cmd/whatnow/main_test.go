package main

import (
	"net"
	"slices"
	"strings"
	"testing"
)

func TestListenUnlessTakenListensOnAFreeAddress(t *testing.T) {
	ln, err := listenUnlessTaken("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	if ln == nil {
		t.Fatal("got no listener on a free address")
	}
	ln.Close()
}

func TestListenUnlessTakenStandsAsideWhenTheAddressIsHeld(t *testing.T) {
	held, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()

	ln, err := listenUnlessTaken(held.Addr().String())
	if err != nil {
		t.Fatalf("got %v, want no error", err)
	}
	if ln != nil {
		ln.Close()
		t.Fatal("got a listener on a held address")
	}
}

func TestListenUnlessTakenReportsOtherFailures(t *testing.T) {
	if _, err := listenUnlessTaken("not-an-address"); err == nil {
		t.Fatal("got no error for a bad address")
	}
}

func TestCommand(t *testing.T) {
	cases := []struct {
		args     []string
		wantCmd  string
		wantArgs []string
	}{
		{nil, "tui", nil},
		{[]string{"-db", "x.db"}, "tui", []string{"-db", "x.db"}},
		{[]string{"serve", "-addr", "127.0.0.1:1"}, "serve", []string{"-addr", "127.0.0.1:1"}},
		{[]string{"tui"}, "tui", []string{}},
		{[]string{"nope"}, "nope", []string{}},
	}
	for _, c := range cases {
		cmd, args := command(c.args)
		if cmd != c.wantCmd || !slices.Equal(args, c.wantArgs) {
			t.Errorf("command(%q) = %q, %q; want %q, %q", c.args, cmd, args, c.wantCmd, c.wantArgs)
		}
	}
}

// validSealKey is base64 of 32 bytes.
const validSealKey = "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="

func TestHostConfigErrors(t *testing.T) {
	key := validSealKey
	env := map[string]string{
		"WHATNOW_GOOGLE_CLIENT_ID":     "id",
		"WHATNOW_GOOGLE_CLIENT_SECRET": "secret",
		"WHATNOW_SEAL_KEY":             key,
	}
	good := []string{"-data", "/tmp/d", "-public-url", "https://now.example.com"}
	tests := []struct {
		name    string
		args    []string
		unset   string
		sealKey string
		want    string
	}{
		{name: "missing data", args: []string{"-public-url", "https://now.example.com"}, want: "-data"},
		{name: "missing public url", args: []string{"-data", "/tmp/d"}, want: "-public-url"},
		{name: "http public url", args: []string{"-data", "/tmp/d", "-public-url", "http://now.example.com"}, want: "https"},
		{name: "missing client id", args: good, unset: "WHATNOW_GOOGLE_CLIENT_ID", want: "WHATNOW_GOOGLE_CLIENT_ID"},
		{name: "missing client secret", args: good, unset: "WHATNOW_GOOGLE_CLIENT_SECRET", want: "WHATNOW_GOOGLE_CLIENT_SECRET"},
		{name: "missing seal key", args: good, unset: "WHATNOW_SEAL_KEY", want: "WHATNOW_SEAL_KEY"},
		{name: "short seal key", args: good, sealKey: "abcd", want: "WHATNOW_SEAL_KEY"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			getenv := func(k string) string {
				if k == tt.unset {
					return ""
				}
				if k == "WHATNOW_SEAL_KEY" && tt.sealKey != "" {
					return tt.sealKey
				}
				return env[k]
			}
			_, err := hostConfig(tt.args, getenv)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("got %v, want an error naming %q", err, tt.want)
			}
		})
	}
}

func TestHostConfigReadsFlagsAndEnvironment(t *testing.T) {
	getenv := func(k string) string {
		return map[string]string{
			"WHATNOW_GOOGLE_CLIENT_ID":     "id",
			"WHATNOW_GOOGLE_CLIENT_SECRET": "secret",
			"WHATNOW_SEAL_KEY":             validSealKey,
		}[k]
	}
	cfg, err := hostConfig([]string{"-data", "/tmp/d", "-public-url", "https://now.example.com/", "-max-users", "5"}, getenv)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.maxUsers != 5 || cfg.dataDir != "/tmp/d" || cfg.addr != "127.0.0.1:8080" || cfg.clientID != "id" || cfg.clientSecret != "secret" {
		t.Fatalf("got %+v", cfg)
	}
	if got := cfg.redirectURL(); got != "https://now.example.com/auth/google/callback" {
		t.Fatalf("got redirect %q", got)
	}
}

func TestRefuseModelEnvironment(t *testing.T) {
	tests := []struct {
		name    string
		environ []string
		want    string
	}{
		{name: "clean", environ: []string{"HOME=/root", "WHATNOW_SEAL_KEY=k", "MY_OPENAI_NOTE=x"}},
		{name: "anthropic", environ: []string{"HOME=/root", "ANTHROPIC_BASE_URL=https://proxy"}, want: "ANTHROPIC_BASE_URL"},
		{name: "openai, even empty", environ: []string{"OPENAI_ORG_ID="}, want: "OPENAI_ORG_ID"},
		{name: "several", environ: []string{"OPENAI_API_KEY=k", "ANTHROPIC_CUSTOM_HEADERS=x"}, want: "ANTHROPIC_CUSTOM_HEADERS, OPENAI_API_KEY"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := refuseModelEnvironment(tt.environ)
			if tt.want == "" {
				if err != nil {
					t.Fatalf("got %v, want none", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("got %v, want an error naming %q", err, tt.want)
			}
		})
	}
}

func TestHostConfigLowercasesThePublicHost(t *testing.T) {
	getenv := func(k string) string {
		return map[string]string{
			"WHATNOW_GOOGLE_CLIENT_ID":     "id",
			"WHATNOW_GOOGLE_CLIENT_SECRET": "secret",
			"WHATNOW_SEAL_KEY":             validSealKey,
		}[k]
	}
	cfg, err := hostConfig([]string{"-data", "/tmp/d", "-public-url", "https://Now.Example.COM"}, getenv)
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.publicURL.Host; got != "now.example.com" {
		t.Fatalf("got host %q, want it lowercased", got)
	}
}
