package handlers

import (
	"bufio"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/OnnaSoft/lipstick/client/tlsconf"
	"github.com/OnnaSoft/lipstick/helper"
)

var client = &http.Client{
	Transport: &http.Transport{
		MaxIdleConns:        1000,
		IdleConnTimeout:     90 * time.Second,
		DisableKeepAlives:   false,
		MaxIdleConnsPerHost: 100,
		// DialTLSContext runs for each new connection, so the trust policy set
		// at startup (-ca) is always used.
		DialTLSContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			dialer := &tls.Dialer{
				NetDialer: &net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second},
				Config:    tlsconf.Config(""),
			}
			return dialer.DialContext(ctx, network, addr)
		},
		Dial: (&net.Dialer{
			Timeout:   30 * time.Second,
			KeepAlive: 30 * time.Second,
		}).Dial,
		WriteBufferSize: 1024,
		ReadBufferSize:  1024,
	},
}

// RequestReport describes one finished request. It never includes headers or
// bodies, so it cannot carry cookies or credentials. The query string is also
// left out of Path.
type RequestReport struct {
	Method        string `json:"method"`
	Path          string `json:"path"`
	Status        int    `json:"status"`
	DurationMS    int64  `json:"durationMs"`
	ResponseBytes int64  `json:"responseBytes"`
}

// HandleHTTP serves the requests of one visitor connection, one after another,
// until the visitor closes it or a message says the connection must end. Each
// finished request is passed to report, which may be nil.
//
// The reader is shared by all requests. A buffered reader reads ahead, so a new
// reader per request would lose the start of the next request.
func HandleHTTP(connection net.Conn, proxyTarget, protocol string, report func(RequestReport)) {
	reader := bufio.NewReader(connection)
	for {
		req, err := http.ReadRequest(reader)
		if err != nil {
			// The visitor closed between requests: a normal end of keep-alive.
			if errors.Is(err, io.EOF) || errors.Is(err, net.ErrClosed) {
				return
			}
			fmt.Println("Error parsing HTTP request:", err)
			sendErrorResponse(connection)
			return
		}
		if !serveRequest(connection, reader, req, proxyTarget, protocol, report) {
			return
		}
	}
}

// serveRequest forwards one request to the local service and writes its
// response. It reports whether the connection can carry another request.
func serveRequest(connection net.Conn, reader *bufio.Reader, req *http.Request, proxyTarget, protocol string, report func(RequestReport)) bool {
	started := time.Now()
	emit := func(status int, responseBytes int64) {
		if report == nil {
			return
		}
		path := req.URL.Path
		if path == "" {
			path = "/"
		}
		report(RequestReport{
			Method:        req.Method,
			Path:          path,
			Status:        status,
			DurationMS:    time.Since(started).Milliseconds(),
			ResponseBytes: responseBytes,
		})
	}

	if protocol != "http" && protocol != "https" {
		protocol = "http"
	}
	host := strings.Split(proxyTarget, ":")[0]
	serverURL := protocol + "://" + proxyTarget + req.URL.RequestURI()

	out, err := http.NewRequest(req.Method, serverURL, req.Body)
	if err != nil {
		fmt.Println("Error creating request to server:", err)
		sendErrorResponse(connection)
		return false
	}
	out.Header = req.Header.Clone()
	// The body length and framing are passed on as they were received. Letting
	// Go pick them would send chunked data to servers that expect a length.
	out.Header.Del("Content-Length")
	out.Header.Del("Transfer-Encoding")
	out.ContentLength = req.ContentLength
	out.TransferEncoding = req.TransferEncoding
	out.Host = host

	if isWebSocket(req) {
		handleWebSocket(connection, reader, proxyTarget, protocol, out)
		return false
	}

	resp, err := client.Do(out)
	if err != nil {
		fmt.Println("Error forwarding request to server:", err)
		sendErrorResponse(connection)
		emit(http.StatusBadGateway, -1)
		return false
	}
	defer resp.Body.Close()

	if err := resp.Write(connection); err != nil {
		emit(resp.StatusCode, -1)
		return false
	}
	emit(resp.StatusCode, resp.ContentLength)
	// Drain what the service did not read, so the next request starts clean.
	io.Copy(io.Discard, req.Body)

	if req.Close || resp.Close {
		return false
	}
	// A body with no length and no chunking ends when the connection closes.
	if resp.ContentLength < 0 && len(resp.TransferEncoding) == 0 && resp.Request.Method != http.MethodHead {
		return false
	}
	return true
}

func isWebSocket(req *http.Request) bool {
	hconn := strings.ToLower(req.Header.Get("Connection"))
	hupgrade := strings.ToLower(req.Header.Get("Upgrade"))
	return strings.Contains(hconn, "upgrade") && slices.Contains([]string{"websocket"}, hupgrade)
}

// handleWebSocket hands the connection to the service after the upgrade request.
// The reader is used as the visitor side, so bytes read ahead are not lost.
func handleWebSocket(connection net.Conn, reader io.Reader, proxyTarget, protocol string, requestToServer *http.Request) {
	var serverConnection net.Conn
	var err error

	if protocol == "http" {
		serverConnection, err = net.Dial("tcp", proxyTarget)
	} else {
		serverConnection, err = tls.Dial("tcp", proxyTarget, tlsconf.Config(""))
	}
	if err != nil {
		fmt.Println("Error connecting to server:", err)
		sendErrorResponse(connection)
		return
	}
	defer serverConnection.Close()

	buf := helper.HTTPRequestToString(requestToServer)
	_, err = serverConnection.Write([]byte(buf))
	if err != nil {
		fmt.Println("Error sending HTTP request to server:", err)
		sendErrorResponse(connection)
		return
	}

	go io.Copy(connection, serverConnection)
	io.Copy(serverConnection, reader)

	fmt.Println("Fin de la conexión WebSocket")
}

func sendErrorResponse(connection net.Conn) {
	connection.Write([]byte(helper.BadGatewayResponse))
}
