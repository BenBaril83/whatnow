package main

import (
	"net"
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
