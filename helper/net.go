package helper

import (
	"net"
)

// CloseWrite half-closes c when the connection supports it, so the other side
// sees end-of-file but can still send. Connections that cannot half-close are
// closed, which is the only safe fallback.
func CloseWrite(c net.Conn) error {
	if cw, ok := c.(interface{ CloseWrite() error }); ok {
		return cw.CloseWrite()
	}
	return c.Close()
}

// CloseWrite delegates to the wrapped connection.
func (c *RemoteConn) CloseWrite() error { return CloseWrite(c.Conn) }

// CloseWrite delegates to the wrapped connection.
func (c *ConnWithBuffer) CloseWrite() error { return CloseWrite(c.Conn) }

// RemoteConn is a visitor connection that is waiting for a tunnel client.
type RemoteConn struct {
	Domain string
	net.Conn
}

type ConnWithBuffer struct {
	net.Conn
	buff []byte
}

func NewConnWithBuffer(conn net.Conn, buff []byte) *ConnWithBuffer {
	return &ConnWithBuffer{Conn: conn, buff: buff}
}

func (c *ConnWithBuffer) SetBuffer(buff []byte) {
	c.buff = buff
}

func (c *ConnWithBuffer) Read(b []byte) (n int, err error) {
	if len(c.buff) > 0 {
		n = copy(b, c.buff)
		c.buff = c.buff[n:]
		return n, nil
	}
	return c.Conn.Read(b)
}
