package manager

import (
	"io"
	"net"
	"strings"
	"testing"
	"time"
)

func TestTicketsAreRandomHex(t *testing.T) {
	tm := &TickerManager{}
	seen := map[string]bool{}
	for i := 0; i < 10000; i++ {
		ticket, err := tm.generate()
		if err != nil {
			t.Fatal(err)
		}
		if len(ticket) != 2*ticketBytes {
			t.Fatalf("ticket %q has length %d, want %d", ticket, len(ticket), 2*ticketBytes)
		}
		if strings.Trim(ticket, "0123456789abcdef") != "" {
			t.Fatalf("ticket %q is not lowercase hex", ticket)
		}
		if seen[ticket] {
			t.Fatalf("duplicate ticket %q", ticket)
		}
		seen[ticket] = true
	}
}

func TestGuessedSequentialTicketIsRejected(t *testing.T) {
	hub := NewNetworkHub("localhost", nil, 0)

	// A visitor waiting for a tunnel client, registered under a real ticket.
	realTicket, err := hub.tickerManager.generate()
	if err != nil {
		t.Fatal(err)
	}
	visitorSide, visitorPeer := net.Pipe()
	defer visitorPeer.Close()
	hub.incomingClientConns[realTicket] = &pendingVisitor{conn: visitorSide, since: time.Now()}

	// An attacker tries the old sequential values. net.Pipe is unbuffered, so
	// drain the attacker side in the background or the rejection write blocks.
	attackerSide, attackerPeer := net.Pipe()
	defer attackerPeer.Close()
	go io.Copy(io.Discard, attackerPeer)
	for _, guess := range []string{"1", "2", "3"} {
		hub.handleServerRequest(&request{ticket: guess, conn: attackerSide})
	}

	// The real visitor connection must still be pending.
	if _, ok := hub.incomingClientConns[realTicket]; !ok {
		t.Fatal("real visitor connection was consumed by a guessed ticket")
	}
}
