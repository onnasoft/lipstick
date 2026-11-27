package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/OnnaSoft/lipstick/client/handlers"
	"github.com/OnnaSoft/lipstick/client/tlsconf"
	"github.com/OnnaSoft/lipstick/helper"
	"github.com/hashicorp/yamux"
	"github.com/pion/dtls/v3"
)

// The client keeps muxSessions authenticated TLS connections to the server, and
// runs a yamux session on each. The server opens one stream per visitor, and the
// client serves each stream. No new TLS connection is needed per visitor.
const muxSessions = 8

const (
	muxMinBackoff = time.Second
	muxMaxBackoff = 10 * time.Second
	// A session that lived longer than this was healthy, so the backoff resets.
	muxHealthyAfter = 5 * time.Second
)

// Report function of the current control connection. It changes when the control
// connection is replaced, so the mux reads it each time it needs it.
var (
	reportMu sync.RWMutex
	reportFn func(handlers.RequestReport)
)

func setReport(fn func(handlers.RequestReport)) {
	reportMu.Lock()
	defer reportMu.Unlock()
	reportFn = fn
}

func currentReport() func(handlers.RequestReport) {
	reportMu.RLock()
	defer reportMu.RUnlock()
	return reportFn
}

// startMux starts the sessions. It is called once per process.
func startMux(proxyTarget string) {
	for i := 0; i < muxSessions; i++ {
		go muxLoop(proxyTarget)
	}
}

// muxLoop keeps one session alive: connect, serve streams, reconnect when it ends.
func muxLoop(proxyTarget string) {
	backoff := muxMinBackoff
	for {
		carrier, err := dialMux()
		if err != nil {
			time.Sleep(backoff)
			backoff = min(backoff*2, muxMaxBackoff)
			continue
		}
		session, err := yamux.Client(carrier, muxClientConfig())
		if err != nil {
			carrier.Close()
			time.Sleep(backoff)
			backoff = min(backoff*2, muxMaxBackoff)
			continue
		}
		started := time.Now()
		serveMuxSession(session, carrier, proxyTarget)
		session.Close()
		if time.Since(started) >= muxHealthyAfter {
			backoff = muxMinBackoff
		} else {
			time.Sleep(backoff)
			backoff = min(backoff*2, muxMaxBackoff)
		}
	}
}

// serveMuxSession accepts streams until the session ends. Each stream is one visitor.
func serveMuxSession(session *yamux.Session, carrier net.Conn, proxyTarget string) {
	for {
		stream, err := session.AcceptStream()
		if err != nil {
			return
		}
		go serveMuxStream(stream, carrier, proxyTarget)
	}
}

// serveMuxStream serves one visitor. The server's first line gives the port the
// visitor came in on. Bytes after that line are passed on, not lost.
func serveMuxStream(stream *yamux.Stream, carrier net.Conn, proxyTarget string) {
	defer stream.Close()
	netStream := helper.NewStreamConn(stream, carrier)

	reader := bufio.NewReader(netStream)
	line, err := reader.ReadString('\n')
	if err != nil {
		return
	}
	kind, port, err := parseAssignment(line)
	if err != nil {
		log.Printf("mux: %v", err)
		return
	}
	pending, _ := reader.Peek(reader.Buffered())
	wrapped := helper.NewConnWithBuffer(netStream, append([]byte(nil), pending...))
	if kind == "LIPSTICK-UDP" {
		serveUDPFlow(wrapped, udpTarget, udpTargetTLS)
		return
	}
	protocol, targetAddress := helper.ParseTargetEndpoint(proxyTarget)
	if kind == "LIPSTICK-PASS" {
		// The visitor's TLS is passed on as it is: the service ends it, so the
		// connection to the service is plain TCP, whatever the scheme of proxy_pass.
		handlers.HandleTCP(wrapped, targetAddress, "http")
		return
	}
	serveVisitor(wrapped, protocol, targetAddress, port, currentReport())
}

// udpTarget is the local UDP service for datagrams from the server, and udpTargetTLS
// says whether it speaks DTLS. Both are set once from the configuration at startup.
var (
	udpTarget    string
	udpTargetTLS bool
)

// udpFlowIdle matches the server: a flow with no datagrams for this long is closed.
const udpFlowIdle = 60 * time.Second

// serveUDPFlow carries the datagrams of one source between the server stream and
// the local UDP service. Each flow has its own UDP socket, so replies from the
// service come back to the flow that asked.
func serveUDPFlow(conn net.Conn, target string, useTLS bool) {
	if target == "" {
		return
	}
	sock, err := dialUDPTarget(target, useTLS)
	if err != nil {
		log.Printf("udp: cannot reach %s: %v", target, err)
		return
	}
	defer sock.Close()

	// Server to service. The stream ends when the server closes the flow.
	go func() {
		for {
			p, err := helper.ReadFrame(conn)
			if err != nil {
				sock.Close()
				return
			}
			sock.Write(p)
		}
	}()

	// Service to server. A flow quiet for udpFlowIdle ends.
	buf := make([]byte, helper.MaxDatagram)
	for {
		sock.SetReadDeadline(time.Now().Add(udpFlowIdle))
		n, err := sock.Read(buf)
		if err != nil {
			return
		}
		if err := helper.WriteFrame(conn, buf[:n]); err != nil {
			return
		}
	}
}

// dialUDPTarget connects to the local UDP service: plain UDP, or DTLS verified
// against the configured CA and the target's host name.
func dialUDPTarget(target string, useTLS bool) (net.Conn, error) {
	if !useTLS {
		raddr, err := net.ResolveUDPAddr("udp", target)
		if err != nil {
			return nil, err
		}
		return net.DialUDP("udp", nil, raddr)
	}
	host, _, err := net.SplitHostPort(target)
	if err != nil {
		return nil, err
	}
	raddr, err := net.ResolveUDPAddr("udp", target)
	if err != nil {
		return nil, err
	}
	conn, err := dtls.DialWithOptions("udp", raddr,
		dtls.WithRootCAs(tlsconf.Roots()),
		dtls.WithServerName(host))
	if err != nil {
		return nil, err
	}
	// The handshake runs now, so a certificate the CA does not sign is reported here.
	if err := conn.Handshake(); err != nil {
		conn.Close()
		return nil, fmt.Errorf("DTLS to %s failed: %w", target, err)
	}
	return conn, nil
}

// dialMux opens one authenticated connection for a mux session, with a fresh
// nonce and its proof. The server then speaks yamux on it.
func dialMux() (net.Conn, error) {
	nonce, err := helper.NewMuxNonce(time.Now())
	if err != nil {
		return nil, err
	}
	headers := http.Header{}
	headers.Set(helper.MuxNonceHeader, nonce)
	headers.Set(helper.ProofHeader, helper.MuxProof(configuration.APISecret, nonce))
	if passthroughOK.Load() {
		headers.Set(helper.PassthroughHeader, "1")
	}
	conn, err := httpmanager.ConnectByAddress(serverURL+"/mux", headers)
	if err != nil {
		return nil, err
	}
	return conn, nil
}

func muxClientConfig() *yamux.Config {
	cfg := yamux.DefaultConfig()
	cfg.EnableKeepAlive = true
	cfg.KeepAliveInterval = 15 * time.Second
	cfg.ConnectionWriteTimeout = 10 * time.Second
	cfg.LogOutput = io.Discard
	return cfg
}

// parseAssignment reads the first line of a stream: "LIPSTICK-PORT <port>" for a
// TCP visitor, or "LIPSTICK-UDP <port>" for a UDP flow. It is an error unless the
// port is a valid number.
func parseAssignment(line string) (string, int, error) {
	line = strings.TrimRight(line, "\r\n")
	kind, rest, ok := strings.Cut(line, " ")
	if !ok || (kind != "LIPSTICK-PORT" && kind != "LIPSTICK-UDP" && kind != "LIPSTICK-PASS") {
		return "", 0, fmt.Errorf("unexpected line on mux stream: %q", line)
	}
	n, err := strconv.Atoi(rest)
	if err != nil || n < 0 || n > 65535 {
		return "", 0, errors.New("invalid port on mux stream")
	}
	return kind, n, nil
}
