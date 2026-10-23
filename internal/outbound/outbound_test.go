package outbound

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"testing"
	"time"
)

func TestClientRefuses(t *testing.T) {
	for _, addr := range []string{
		"127.0.0.1", "[::1]", "10.0.0.1", "192.168.1.1", "172.16.0.1",
		"169.254.169.254", "100.64.0.1", "[fc00::1]", "0.0.0.0",
		"[::ffff:127.0.0.1]", "[::ffff:10.0.0.1]",
	} {
		t.Run(addr, func(t *testing.T) {
			resp, err := Client(time.Second).Get("http://" + addr + "/")
			if err == nil {
				resp.Body.Close()
			}
			if !errors.Is(err, ErrBlocked) {
				t.Fatalf("err = %v, want ErrBlocked", err)
			}
		})
	}
}

func TestClientRefusesHostnameResolvingPrivate(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer srv.Close()
	u, _ := url.Parse(srv.URL)

	resp, err := Client(time.Second).Get("http://localhost:" + u.Port() + "/")
	if err == nil {
		resp.Body.Close()
	}
	if !errors.Is(err, ErrBlocked) {
		t.Fatalf("err = %v, want ErrBlocked", err)
	}
}

func TestClientRefusesRedirectToPrivate(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://169.254.169.254/", http.StatusFound)
	}))
	defer srv.Close()
	u, _ := url.Parse(srv.URL)
	server := netip.MustParseAddr(u.Hostname())

	// Only the test server's loopback address is let through; the redirect target is judged normally.
	client := newClient(time.Second, func(a netip.Addr) bool { return a == server || isPublic(a) })
	resp, err := client.Get(srv.URL)
	if err == nil {
		resp.Body.Close()
	}
	if !errors.Is(err, ErrBlocked) {
		t.Fatalf("err = %v, want ErrBlocked", err)
	}
}

func TestClientZeroTimeoutMeansNone(t *testing.T) {
	if got := Client(0).Timeout; got != 0 {
		t.Fatalf("Timeout = %v, want 0", got)
	}
	if got := Client(5 * time.Second).Timeout; got != 5*time.Second {
		t.Fatalf("Timeout = %v, want 5s", got)
	}
}

func TestIsPublic(t *testing.T) {
	for addr, want := range map[string]bool{
		"8.8.8.8":              true,
		"2606:4700:4700::1111": true,
		"100.63.255.255":       true,
		"100.128.0.0":          true,
		"100.127.255.255":      false,
		"224.0.0.1":            false,
		"::ffff:8.8.8.8":       true,
		"::ffff:192.168.0.1":   false,
	} {
		if got := isPublic(netip.MustParseAddr(addr)); got != want {
			t.Errorf("isPublic(%s) = %v, want %v", addr, got, want)
		}
	}
}
