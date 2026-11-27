package manager

import (
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"strings"

	"github.com/OnnaSoft/lipstick/client/tlsconf"
)

type HTTPManager struct {
}

func NewHTTPManager() *HTTPManager {
	return &HTTPManager{}
}

type CustomConn struct {
	net.Conn
	buffer []byte
}

func (c *CustomConn) SetBuffer(buffer []byte) {
	c.buffer = buffer
}

func (c *CustomConn) Read(b []byte) (n int, err error) {
	if len(c.buffer) == 0 {
		return c.Conn.Read(b)
	}
	n = copy(b, c.buffer)
	c.buffer = c.buffer[n:]
	return n, nil
}

func (m *HTTPManager) Connect(url string, headers http.Header) (*CustomConn, error) {
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("error creating request: %w", err)
	}
	req.Header = headers

	host := req.URL.Host
	if host == "" {
		host = req.URL.Hostname()
	}
	if !strings.Contains(host, ":") {
		if req.URL.Port() == "" {
			host += ":443"
		} else {
			host += ":" + req.URL.Port()
		}
	}

	var conn net.Conn
	if req.URL.Scheme == "http" || req.URL.Scheme == "ws" {
		conn, err = net.Dial("tcp", host)
	} else {
		conn, err = tls.Dial("tcp", host, tlsconf.Config(""))
	}
	if err != nil {
		return nil, fmt.Errorf("error connecting to host: %w", tlsconf.Explain(err))
	}

	if err := req.Write(conn); err != nil {
		conn.Close()
		return nil, fmt.Errorf("error writing request to connection: %w", err)
	}

	return &CustomConn{Conn: conn}, nil
}

// ConnectByAddress opens the reverse connection for a ticket. It always dials
// the host from the server URL, the same way for plain and TLS connections:
// the address the server advertises can be a private IP without a port, which
// cannot be dialed.
func (m *HTTPManager) ConnectByAddress(url string, headers http.Header) (*CustomConn, error) {
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("error creating request: %w", err)
	}
	req.Header = headers

	host := req.URL.Host
	if host == "" {
		host = req.URL.Hostname()
	}
	if !strings.Contains(host, ":") {
		if req.URL.Port() == "" {
			host += ":443"
		} else {
			host += ":" + req.URL.Port()
		}
	}

	var conn net.Conn
	if req.URL.Scheme == "http" || req.URL.Scheme == "ws" {
		conn, err = net.Dial("tcp", host)
	} else {
		conn, err = tls.Dial("tcp", host, tlsconf.Config(""))
	}
	if err != nil {
		return nil, fmt.Errorf("error connecting to host: %w", tlsconf.Explain(err))
	}

	if err := req.Write(conn); err != nil {
		conn.Close()
		return nil, fmt.Errorf("error writing request to connection: %w", err)
	}

	return &CustomConn{Conn: conn}, nil
}
