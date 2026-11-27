package manager

import (
	"bufio"
	"bytes"
	"errors"
	"net"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/OnnaSoft/lipstick/helper"
	"github.com/OnnaSoft/lipstick/logger"
)

// acceptRetryDelay pauses after a transient Accept error, such as running out
// of file descriptors, so the loop does not spin.
const acceptRetryDelay = 100 * time.Millisecond

type CustomListener struct {
	net.Listener
	conn    chan net.Conn
	closed  bool
	manager *Manager
}

func NewCustomListener(l net.Listener, manager *Manager) *CustomListener {
	cl := &CustomListener{
		Listener: l,
		conn:     make(chan net.Conn),
		manager:  manager,
	}
	go cl.acceptConnections()
	return cl
}

// Accept only ever returns connections that gin should serve. Errors from a
// single bad client are handled inside handle and never reach this method:
// http.Server.Serve stops for good on any Accept error, which would take the
// whole manager down.
func (cl *CustomListener) Accept() (net.Conn, error) {
	conn, ok := <-cl.conn
	if !ok {
		return nil, errors.New("listener closed")
	}
	return conn, nil
}

func (cl *CustomListener) Close() error {
	if cl.closed {
		logger.Default.Debug("Listener already closed")
		return nil
	}
	cl.closed = true
	close(cl.conn)
	err := cl.Listener.Close()
	if err != nil {
		logger.Default.Error("Error closing listener:", err)
	}
	return err
}

// reject drops a connection that cannot be served. It only affects that one
// client; the listener keeps accepting.
func (cl *CustomListener) reject(conn net.Conn, reason string, err error) {
	logger.Default.Error(reason, err)
	conn.Close()
}

func (cl *CustomListener) handle(conn net.Conn) {
	buffer := make([]byte, 1024)
	n, err := conn.Read(buffer)
	if err != nil {
		cl.reject(conn, "Error reading from connection:", err)
		return
	}

	requestLine, url, valid := parseRequest(buffer[:n])
	if !valid {
		cl.reject(conn, "Invalid request received:", errors.New(requestLine))
		return
	}

	urlsToIgnore := []string{"/", "/health"}
	if slices.Contains(urlsToIgnore, url) {
		logger.Default.Debug("Request to ignored URL:", url)
		cl.conn <- helper.NewConnWithBuffer(conn, buffer[:n])
		return
	}

	if strings.HasPrefix(url, "/") && len(url) > 1 && strings.Count(url, "/") == 1 {
		ticket := url[1:]
		b, err := helper.ReadUntilHeadersEnd(helper.NewConnWithBuffer(conn, buffer[:n]))
		if err != nil {
			cl.reject(conn, "Error reading headers from connection:", err)
			return
		}
		req, err := http.ReadRequest(bufio.NewReader(bytes.NewReader(b)))
		if err != nil {
			cl.reject(conn, "Error reading HTTP request:", err)
			return
		}
		logger.Default.Debug("Handling tunnel with ticket:", ticket)
		cl.manager.handleTunnel(conn, req, ticket)
		return
	}

	cl.reject(conn, "Invalid request with line:", errors.New(requestLine))
}

func (cl *CustomListener) acceptConnections() {
	for {
		conn, err := cl.Listener.Accept()
		if err != nil {
			// The underlying listener can be closed without going through
			// CustomListener.Close. Stop then, instead of retrying forever.
			if cl.closed || errors.Is(err, net.ErrClosed) {
				logger.Default.Debug("Stopped accepting connections, listener is closed")
				return
			}
			logger.Default.Error("Error accepting connection:", err)
			time.Sleep(acceptRetryDelay)
			continue
		}
		logger.Default.Debug("New connection accepted")
		go cl.handle(conn)
	}
}

func parseRequest(buffer []byte) (requestLine, url string, valid bool) {
	lines := bytes.Split(buffer, []byte("\n"))
	if len(lines) == 0 {
		logger.Default.Error("Received empty request buffer")
		return "", "", false
	}
	requestLine = string(lines[0])
	parts := strings.Fields(requestLine)
	if len(parts) < 2 {
		logger.Default.Error("Invalid request line format:", requestLine)
		return requestLine, "", false
	}
	url = strings.Split(parts[1], "?")[0]
	return requestLine, url, true
}
