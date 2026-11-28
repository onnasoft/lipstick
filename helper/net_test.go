package helper

import (
	"net"
	"testing"
)

// The release of a visitor's connection slot runs exactly once, when the connection closes.
func TestRemoteConnRunsOnCloseOnce(t *testing.T) {
	a, b := net.Pipe()
	defer b.Close()
	calls := 0
	rc := &RemoteConn{Conn: a, Domain: "x.example.com", OnClose: func() { calls++ }}
	rc.Close()
	rc.Close()
	if calls != 1 {
		t.Fatalf("OnClose ran %d times, want 1", calls)
	}
}
