package manager

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
)

// ticketBytes is the number of random bytes in a ticket: 128 bits, which
// cannot be guessed by trying values.
const ticketBytes = 16

// TickerManager issues tickets. A ticket is the only link between a visitor's
// connection and the reverse connection a tunnel client opens, so it must be
// unpredictable. Sequential values would let anyone who can reach the manager
// port attach to another visitor's connection.
type TickerManager struct{}

func (tm *TickerManager) generate() (string, error) {
	b := make([]byte, ticketBytes)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("cannot generate ticket: %w", err)
	}
	return hex.EncodeToString(b), nil
}
