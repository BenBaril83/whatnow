// Package outbound provides an HTTP client that can't reach private networks, for requests to
// user-controlled URLs in hosted mode.
package outbound

import (
	"errors"
	"net"
	"net/http"
	"net/netip"
	"syscall"
	"time"
)

// ErrBlocked is returned when a request would reach a non-public address.
var ErrBlocked = errors.New("address not allowed")

// carrierGradeNAT is shared address space that isn't Private() but isn't public either.
var carrierGradeNAT = netip.MustParsePrefix("100.64.0.0/10")

// Client returns a client that only dials public unicast addresses. A timeout of 0 means no
// overall timeout, for streamed replies.
func Client(timeout time.Duration) *http.Client {
	return newClient(timeout, isPublic)
}

func newClient(timeout time.Duration, allow func(netip.Addr) bool) *http.Client {
	// Checking in Control, on the address actually being dialed, covers DNS rebinding and every
	// redirect hop. Proxy stays nil so an environment proxy can't route around it.
	dialer := &net.Dialer{
		Timeout:   30 * time.Second,
		KeepAlive: 30 * time.Second,
		Control: func(_, address string, _ syscall.RawConn) error {
			ap, err := netip.ParseAddrPort(address)
			if err != nil || !allow(ap.Addr()) {
				return ErrBlocked
			}
			return nil
		},
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	transport.DialContext = dialer.DialContext
	return &http.Client{Transport: transport, Timeout: timeout}
}

func isPublic(addr netip.Addr) bool {
	addr = addr.Unmap() // ::ffff:127.0.0.1 is loopback
	return addr.IsGlobalUnicast() && !addr.IsPrivate() && !addr.IsLoopback() &&
		!addr.IsLinkLocalUnicast() && !addr.IsUnspecified() && !carrierGradeNAT.Contains(addr)
}
