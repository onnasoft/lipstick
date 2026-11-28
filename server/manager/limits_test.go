package manager

import (
	"bufio"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/OnnaSoft/lipstick/server/limits"
)

// A visitor beyond its rate is answered 429 with Retry-After and never reaches the tunnel.
func TestRefusedVisitorGets429WithRetryAfter(t *testing.T) {
	m := &Manager{}
	m.ConfigureLimits(limits.Settings{VisitorIPRate: 1, VisitorIPBurst: 1})

	first, firstPeer := net.Pipe()
	defer firstPeer.Close()
	if _, ok := m.admitVisitor(first, "app.example.com", true); !ok {
		t.Fatal("first visitor refused")
	}

	second, secondPeer := net.Pipe()
	defer secondPeer.Close()
	got := make(chan string, 1)
	go func() {
		reader := bufio.NewReader(secondPeer)
		var answer strings.Builder
		for {
			l, err := reader.ReadString('\n')
			answer.WriteString(l)
			if err != nil || l == "\r\n" {
				break
			}
		}
		got <- answer.String()
	}()
	if _, ok := m.admitVisitor(second, "app.example.com", true); ok {
		t.Fatal("second visitor admitted over the rate")
	}
	select {
	case answer := <-got:
		if !strings.HasPrefix(answer, "HTTP/1.1 429") || !strings.Contains(answer, "Retry-After: 1") {
			t.Fatalf("refusal: %q", answer)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("no refusal answer")
	}
}

// A connection slot is held while a visitor is connected and freed when it closes.
func TestConnectionSlotIsFreedWhenVisitorCloses(t *testing.T) {
	m := &Manager{}
	m.ConfigureLimits(limits.Settings{ConnsPerDomain: 1})

	a, aPeer := net.Pipe()
	defer aPeer.Close()
	rc, ok := m.admitVisitor(a, "app.example.com", false)
	if !ok {
		t.Fatal("first connection refused")
	}
	b, bPeer := net.Pipe()
	defer bPeer.Close()
	if _, ok := m.admitVisitor(b, "app.example.com", false); ok {
		t.Fatal("second connection admitted over the domain's limit")
	}
	rc.Close()
	c, cPeer := net.Pipe()
	defer cPeer.Close()
	if _, ok := m.admitVisitor(c, "app.example.com", false); !ok {
		t.Fatal("slot not freed when the visitor closed")
	}
	// A connection the route already admitted is not counted again.
	if again, ok := m.admitVisitor(rc, "app.example.com", false); !ok || again != rc {
		t.Fatal("an admitted connection was counted twice")
	}
}
