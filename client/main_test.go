package main

import (
	"bufio"
	"strings"
	"testing"
)

func reader(s string) *bufio.Reader { return bufio.NewReader(strings.NewReader(s)) }

// A ticket line is read as is. Bad lines are errors, never panics or empty tickets.
func TestReadMessageAcceptsTicket(t *testing.T) {
	got, err := readMessage(reader("3f9a0c\n"))
	if err != nil || got != "3f9a0c" {
		t.Fatalf("got %q, %v", got, err)
	}
}

func TestReadMessageRejectsBadLines(t *testing.T) {
	for _, line := range []string{"\n", "10.0.0.5:3f9a\n", "../../admin\n", "ABC\n", "close\n", "zz\n"} {
		if got, err := readMessage(reader(line)); err == nil {
			t.Errorf("line %q accepted as ticket %q", line, got)
		}
	}
}

func TestReadMessageFailsAtEOF(t *testing.T) {
	if _, err := readMessage(reader("3f9a")); err == nil {
		t.Fatal("unterminated line at EOF accepted")
	}
}

func TestIsTicketLimits(t *testing.T) {
	if isTicket(strings.Repeat("a", 65)) {
		t.Fatal("65-character ticket accepted")
	}
	if !isTicket(strings.Repeat("a", 64)) {
		t.Fatal("64-character ticket rejected")
	}
}
