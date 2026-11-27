package manager

import (
	"crypto/tls"
	"errors"
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

// dialServer opens a verified TLS connection to the host in req's URL and
// writes the request. The tunnel carries API keys and traffic, so there is no
// plain-text path.
func dialServer(req *http.Request) (*CustomConn, error) {
	if req.URL.Scheme != "https" {
		return nil, errors.New("server URL must start with https://")
	}

	host := req.URL.Host
	if host == "" {
		host = req.URL.Hostname()
	}
	if !strings.Contains(host, ":") {
		host += ":443"
	}

	conn, err := tls.Dial("tcp", host, tlsconf.Config(""))
	if err != nil {
		return nil, fmt.Errorf("error connecting to host: %w", tlsconf.Explain(err))
	}

	if err := req.Write(conn); err != nil {
		conn.Close()
		return nil, fmt.Errorf("error writing request to connection: %w", err)
	}

	return &CustomConn{Conn: conn}, nil
}

// Connect opens the control connection to the server manager.
func (m *HTTPManager) Connect(url string, headers http.Header) (*CustomConn, error) {
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("error creating request: %w", err)
	}
	req.Header = headers
	return dialServer(req)
}

// ConnectByAddress opens the reverse connection for a ticket. It always dials
// the host from the server URL, never the address the server advertises: that
// address can be a private IP without a port, which cannot be dialed.
func (m *HTTPManager) ConnectByAddress(url string, headers http.Header) (*CustomConn, error) {
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("error creating request: %w", err)
	}
	req.Header = headers
	return dialServer(req)
}
