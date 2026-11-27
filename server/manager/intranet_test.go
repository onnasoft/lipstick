package manager

import (
	"io"
	"net"
	"strconv"
	"strings"
	"testing"
	"time"
)

func mustNet(t *testing.T, cidr string) *net.IPNet {
	t.Helper()
	_, n, err := net.ParseCIDR(cidr)
	if err != nil {
		t.Fatal(err)
	}
	return n
}

// freePort returns a port that nothing listens on right now.
func freePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port
}

func newIntranetManager(t *testing.T, port int, policy map[string][]*net.IPNet) *Manager {
	t.Helper()
	m := &Manager{}
	m.ConfigureIntranetPorts("127.0.0.1", port, port, policy)
	t.Cleanup(func() { m.releaseAllIntranetPorts() })
	return m
}

func readOrTimeout(t *testing.T, c net.Conn) string {
	t.Helper()
	c.SetReadDeadline(time.Now().Add(3 * time.Second))
	b, _ := io.ReadAll(c)
	return string(b)
}

func TestReserveRefusesDomainNotInPolicy(t *testing.T) {
	port := freePort(t)
	m := newIntranetManager(t, port, map[string][]*net.IPNet{"a.example.com": {mustNet(t, "127.0.0.0/8")}})
	if err := m.reserveIntranetPort("other.example.com", port); err != ErrPortNotAllowed {
		t.Fatalf("domain outside policy: %v", err)
	}
}

func TestReserveRefusesPortOutsideRange(t *testing.T) {
	port := freePort(t)
	m := newIntranetManager(t, port, map[string][]*net.IPNet{"a.example.com": {mustNet(t, "127.0.0.0/8")}})
	if err := m.reserveIntranetPort("a.example.com", port+1); err != ErrPortOutOfRange {
		t.Fatalf("port outside range: %v", err)
	}
}

// Two domains cannot share a port. The second one gets the taken error.
func TestReserveRefusesTakenPort(t *testing.T) {
	port := freePort(t)
	policy := map[string][]*net.IPNet{
		"a.example.com": {mustNet(t, "127.0.0.0/8")},
		"b.example.com": {mustNet(t, "127.0.0.0/8")},
	}
	m := newIntranetManager(t, port, policy)
	if err := m.reserveIntranetPort("a.example.com", port); err != nil {
		t.Fatalf("first reservation failed: %v", err)
	}
	if err := m.reserveIntranetPort("b.example.com", port); err != ErrPortTaken {
		t.Fatalf("second domain on the same port: %v", err)
	}
	// The same domain asking again is not an error.
	if err := m.reserveIntranetPort("a.example.com", port); err != nil {
		t.Fatalf("same domain asking again: %v", err)
	}
}

// A reserved port answers allowed sources, and gets 502 when no client is
// connected for the domain.
func TestReservedPortServesAllowedSource(t *testing.T) {
	port := freePort(t)
	m := newIntranetManager(t, port, map[string][]*net.IPNet{"a.example.com": {mustNet(t, "127.0.0.0/8")}})
	if err := m.reserveIntranetPort("a.example.com", port); err != nil {
		t.Fatal(err)
	}
	c, err := net.Dial("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	c.Write([]byte("GET / HTTP/1.1\r\nHost: x\r\n\r\n"))
	if got := readOrTimeout(t, c); !strings.Contains(got, "502") {
		t.Fatalf("allowed source got %q, want 502 with no client", got)
	}
}

// A source outside the allowed networks is closed without a reply.
func TestReservedPortRefusesOtherSources(t *testing.T) {
	port := freePort(t)
	m := newIntranetManager(t, port, map[string][]*net.IPNet{"a.example.com": {mustNet(t, "10.20.0.0/16")}})
	if err := m.reserveIntranetPort("a.example.com", port); err != nil {
		t.Fatal(err)
	}
	c, err := net.Dial("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	c.Write([]byte("GET / HTTP/1.1\r\nHost: x\r\n\r\n"))
	if got := readOrTimeout(t, c); got != "" {
		t.Fatalf("refused source got a reply: %q", got)
	}
}

// When the domain has no clients left, its ports close and can be taken again.
func TestReleaseFreesThePort(t *testing.T) {
	port := freePort(t)
	m := newIntranetManager(t, port, map[string][]*net.IPNet{"a.example.com": {mustNet(t, "127.0.0.0/8")}})
	if err := m.reserveIntranetPort("a.example.com", port); err != nil {
		t.Fatal(err)
	}
	m.releaseIntranetPorts("a.example.com")
	if _, err := net.Dial("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port))); err == nil {
		t.Fatal("port still accepts connections after release")
	}
	if err := m.reserveIntranetPort("a.example.com", port); err != nil {
		t.Fatalf("port not free after release: %v", err)
	}
}

func TestSourceAllowedHandlesIPv4AndUnknownAddresses(t *testing.T) {
	nets := []*net.IPNet{mustNet(t, "192.168.1.0/24")}
	if !sourceAllowed(&net.TCPAddr{IP: net.ParseIP("192.168.1.9"), Port: 1}, nets) {
		t.Fatal("address inside the range refused")
	}
	if sourceAllowed(&net.TCPAddr{IP: net.ParseIP("192.168.2.9"), Port: 1}, nets) {
		t.Fatal("address outside the range accepted")
	}
	if sourceAllowed(&net.UnixAddr{Name: "x", Net: "unix"}, nets) {
		t.Fatal("non-TCP address accepted")
	}
}
