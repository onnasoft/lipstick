package helper

import (
	"bufio"
	"bytes"
	"crypto/tls"
	"errors"
	"net"
	"net/http"
	"strings"
)

// NewListenerManagerTCP listens for TLS connections only. A nil tlsConfig is a
// programming error and is refused.
func NewListenerManagerTCP(addr string, tlsConfig *tls.Config) (*ListenerManager, error) {
	if tlsConfig == nil {
		return nil, errors.New("TLS configuration is required")
	}
	l, err := tls.Listen("tcp", addr, tlsConfig)
	if err != nil {
		return nil, err
	}
	return NewListenerManager(l), nil
}

type ListenerManager struct {
	net.Listener
	onClose    func()
	onHTTPConn func(net.Conn, *http.Request)
	onTCPConn  func(net.Conn)
	onListen   func()
}

func NewListenerManager(l net.Listener) *ListenerManager {
	return &ListenerManager{Listener: l}
}

func (l *ListenerManager) OnListen(fn func()) {
	l.onListen = fn
}

func (l *ListenerManager) OnClose(fn func()) {
	l.onClose = fn
}

func (l *ListenerManager) OnHTTPConn(fn func(net.Conn, *http.Request)) {
	l.onHTTPConn = fn
}

func (l *ListenerManager) OnTCPConn(fn func(net.Conn)) {
	l.onTCPConn = fn
}

func (l *ListenerManager) ListenAndServe() error {
	if l.onListen != nil {
		l.onListen()
	}
	handleConnection := func(conn net.Conn) {
		if err := l.handleConnection(conn); err != nil {
			conn.Close()
		}
	}

	for {
		conn, err := l.Accept()
		if err != nil {
			return l.handleAcceptError(err)
		}
		go handleConnection(conn)
	}
}

func (l *ListenerManager) handleAcceptError(err error) error {
	if l.onClose != nil {
		l.onClose()
	}
	return err
}

func (l *ListenerManager) handleConnection(conn net.Conn) error {
	buffer := make([]byte, 1024)
	n, err := conn.Read(buffer)
	if err != nil {
		return err
	}
	wconn := NewConnWithBuffer(conn, buffer[:n])
	if IsHTTPRequest(string(buffer[:n])) {
		return l.handleHTTPConn(wconn)
	}
	return l.handleTCPConn(wconn)
}

func (l *ListenerManager) handleHTTPConn(conn *ConnWithBuffer) error {
	if l.onHTTPConn != nil {
		b, err := ReadUntilHeadersEnd(conn)
		if err != nil {
			if errors.Is(err, ErrHeadersTooLarge) {
				conn.Write([]byte(RequestHeaderFieldsTooLargeResponse))
			}
			return err
		}
		conn.Unread(b)
		// Checked on the raw header block: the parser drops Content-Length when a request
		// also has Transfer-Encoding, so the parsed request no longer shows the conflict.
		if hasHeader(b, "Content-Length") && hasHeader(b, "Transfer-Encoding") {
			conn.Write([]byte(BadRequestResponse))
			return errors.New("Content-Length and Transfer-Encoding cannot both be sent")
		}
		req, err := http.ReadRequest(bufio.NewReader(bytes.NewBuffer(b)))
		if err != nil {
			// A request the parser cannot read (a bad Content-Length, a broken header)
			// gets a 400 before the connection is closed, instead of silence.
			conn.Write([]byte(BadRequestResponse))
			return err
		}
		if reason := invalidRequest(req); reason != "" {
			conn.Write([]byte(BadRequestResponse))
			return errors.New(reason)
		}
		l.onHTTPConn(conn, req)
	}
	return nil
}

func (l *ListenerManager) handleTCPConn(conn net.Conn) error {
	if l.onTCPConn != nil {
		l.onTCPConn(conn)
	}
	return nil
}

// invalidRequest names the reason a parsed request must not be served, or "" when it may be.
// Routing needs a Host. (Content-Length with Transfer-Encoding is refused on the raw headers
// in handleHTTPConn: they describe the body's end differently, so a proxy and a backend that
// disagree about it can be made to read one request as two.)
func invalidRequest(req *http.Request) string {
	if req.Host == "" {
		return "a Host header is required"
	}
	return ""
}

// hasHeader reports whether a raw header block contains the named field, in any case.
func hasHeader(block []byte, name string) bool {
	for _, line := range bytes.Split(block, []byte("\n")) {
		if i := bytes.IndexByte(line, ':'); i > 0 && strings.EqualFold(strings.TrimSpace(string(line[:i])), name) {
			return true
		}
	}
	return false
}
