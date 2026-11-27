package helper

import (
	"net"
	"time"
)

// MuxStream is a stream of a multiplexed session, such as a yamux stream. It has
// every net.Conn method except the addresses.
type MuxStream interface {
	Read(b []byte) (int, error)
	Write(b []byte) (int, error)
	Close() error
	SetDeadline(t time.Time) error
	SetReadDeadline(t time.Time) error
	SetWriteDeadline(t time.Time) error
}

// StreamConn is a MuxStream as a net.Conn. Addresses come from the connection
// that carries the stream. Close is a half-close in yamux: it stops writes and
// keeps reads open, so relays that half-close keep working.
type StreamConn struct {
	MuxStream
	Carrier net.Conn // the connection that carries the stream
}

// NewStreamConn wraps s as a net.Conn.
func NewStreamConn(s MuxStream, carrier net.Conn) *StreamConn {
	return &StreamConn{MuxStream: s, Carrier: carrier}
}

// LocalAddr returns the local address of the carrying connection.
func (s *StreamConn) LocalAddr() net.Addr { return s.Carrier.LocalAddr() }

// RemoteAddr returns the remote address of the carrying connection.
func (s *StreamConn) RemoteAddr() net.Addr { return s.Carrier.RemoteAddr() }
