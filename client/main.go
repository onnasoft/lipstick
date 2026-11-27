package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/OnnaSoft/lipstick/client/config"
	"github.com/OnnaSoft/lipstick/client/handlers"
	"github.com/OnnaSoft/lipstick/client/manager"
	"github.com/OnnaSoft/lipstick/client/tlsconf"
	"github.com/OnnaSoft/lipstick/helper"
)

var httpmanager = manager.NewHTTPManager()

// configuration and serverURL are set in main, after validation, so a bad
// configuration is reported at startup instead of failing later.
var (
	configuration *config.Config
	serverURL     string
)

func main() {
	cfg, err := config.GetConfig()
	if err != nil {
		fmt.Fprintln(os.Stderr, "Error de configuración:")
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	configuration = cfg
	serverURL = cfg.ServerURL

	if err := tlsconf.Configure(cfg.CAFile); err != nil {
		fmt.Fprintln(os.Stderr, "Error de TLS:", err)
		os.Exit(1)
	}

	if config.CheckOnly() {
		fmt.Println("configuración válida")
		os.Exit(0)
	}

	interruptChannel := make(chan os.Signal, 1)
	signal.Notify(interruptChannel, os.Interrupt)

	fmt.Println(serverURL, configuration.ProxyPass)

	for _, proxyTarget := range configuration.ProxyPass {
		go startClient(proxyTarget)
	}
	fmt.Println("Presiona Ctrl+C para salir.")

	<-interruptChannel
	fmt.Println("Desconectando...")
}

const (
	minRetryDelay = 3 * time.Second
	maxRetryDelay = 60 * time.Second
)

// startClient keeps one control connection to the server for proxyTarget. After
// a failure it waits, doubling the wait up to maxRetryDelay. A connection that
// was established resets the wait.
func startClient(proxyTarget string) {
	fmt.Println("Connecting to", serverURL)
	startMux(proxyTarget)
	headers := http.Header{}
	headers.Set("authorization", configuration.APISecret)
	if configuration.IntranetPort > 0 {
		headers.Set("X-Lipstick-Intranet-Port", strconv.Itoa(configuration.IntranetPort))
	}
	if configuration.IntranetUDPPort > 0 {
		headers.Set("X-Lipstick-Intranet-UDP-Port", strconv.Itoa(configuration.IntranetUDPPort))
	}
	udpTarget = configuration.UDPTarget
	udpTargetTLS = configuration.UDPTargetTLS

	retryDelay := minRetryDelay
	for {
		conn, err := httpmanager.Connect(serverURL, headers)
		if err != nil {
			log.Printf("Error connecting to %s: %v (retrying in %s)\n", serverURL, err, retryDelay)
			time.Sleep(retryDelay)
			retryDelay = min(retryDelay*2, maxRetryDelay)
			continue
		}
		retryDelay = minRetryDelay

		fmt.Println("Connected to server at", serverURL)
		ctrl := &controlWriter{conn: conn}
		setReport(ctrl.reportRequest)
		go checkConnection(ctrl)
		handleTickets(conn, proxyTarget, ctrl.reportRequest)
		fmt.Println("Disconnected from server at", serverURL)
		time.Sleep(minRetryDelay)
	}
}

// controlWriter sends lines on the control connection. The keep-alive ping and
// the request reports come from different goroutines, so each line is written
// under a lock, and a write has a deadline so a stuck server cannot block it.
type controlWriter struct {
	mu   sync.Mutex
	conn net.Conn
}

func (c *controlWriter) send(line string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.conn.SetWriteDeadline(time.Now().Add(controlWriteTimeout))
	_, err := c.conn.Write([]byte(line))
	return err
}

// reportRequest sends one finished request to the server as "log {json}".
func (c *controlWriter) reportRequest(r handlers.RequestReport) {
	data, err := json.Marshal(r)
	if err != nil {
		return
	}
	if err := c.send("log " + string(data) + "\n"); err != nil {
		log.Printf("could not report a request: %v", err)
	}
}

// controlWriteTimeout bounds one write on the control connection.
const controlWriteTimeout = 5 * time.Second

// checkConnection sends a ping every 30 seconds. The server drops a client that
// stays silent for 90 seconds.
func checkConnection(ctrl *controlWriter) {
	for {
		if err := ctrl.send("ping\n"); err != nil {
			return
		}
		time.Sleep(30 * time.Second)
	}
}

// readMessage reads one ticket line from the server. The server sends only the
// ticket: a hex string, so it cannot contain separators or URL characters.
// readMessage reads one line from the server: "ticket port". The port is the
// server port the visitor came in on. A missing or invalid port is reported as 0.
func readMessage(reader *bufio.Reader) (string, int, error) {
	line, err := reader.ReadString('\n')
	if err != nil {
		return "", 0, fmt.Errorf("error reading until newline: %w", err)
	}
	line = strings.TrimRight(line, "\r\n")
	if line == "close" {
		return "", 0, errors.New("connection closed by server")
	}
	ticket, rest, _ := strings.Cut(line, " ")
	if !isTicket(ticket) {
		return "", 0, fmt.Errorf("invalid ticket from server: %q", line)
	}
	port := 0
	if n, err := strconv.Atoi(rest); err == nil && n >= 0 && n <= 65535 {
		port = n
	}
	return ticket, port, nil
}

// isTicket accepts 1 to 64 lowercase hex characters.
func isTicket(s string) bool {
	if len(s) == 0 || len(s) > 64 {
		return false
	}
	for _, c := range s {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			return false
		}
	}
	return true
}

func handleTickets(connection net.Conn, proxyTarget string, report func(handlers.RequestReport)) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("ticket loop stopped: %v", r)
		}
	}()
	defer connection.Close()

	reader := bufio.NewReader(connection)
	for {
		ticket, port, err := readMessage(reader)
		if err != nil {
			fmt.Println(err)
			return
		}

		protocol, targetAddress := helper.ParseTargetEndpoint(proxyTarget)
		go establishConnection(protocol, targetAddress, ticket, port, report)
	}
}

func establishConnection(protocol, proxyTarget, ticket string, port int, report func(handlers.RequestReport)) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("connection for ticket %s failed: %v", ticket, r)
		}
	}()
	url := serverURL + "/" + ticket

	// The proof shows the server this client knows the key, without sending it.
	headers := http.Header{}
	headers.Set(helper.ProofHeader, helper.TunnelProof(configuration.APISecret, ticket))
	connection, err := httpmanager.ConnectByAddress(url, headers)
	if err != nil {
		// No connection exists yet, so there is nothing to answer on.
		log.Printf("reverse connection failed: %v", err)
		return
	}
	defer connection.Close()

	serveVisitor(connection, protocol, proxyTarget, port, report)
}

// serveVisitor serves one visitor on an open reverse connection. It picks HTTP
// or TCP from the first bytes the visitor sends. Every request on an HTTP
// connection came in on the same server port, so port is set on each report.
func serveVisitor(connection net.Conn, protocol, proxyTarget string, port int, report func(handlers.RequestReport)) {
	b := make([]byte, 1024)
	n, err := connection.Read(b)
	if err != nil {
		connection.Write([]byte(helper.BadGatewayResponse))
		return
	}
	buff := b[:n]
	conn := helper.NewConnWithBuffer(connection, buff)

	if helper.IsHTTPRequest(string(buff)) {
		handlers.HandleHTTP(conn, proxyTarget, protocol, func(r handlers.RequestReport) {
			r.Port = port
			if report != nil {
				report(r)
			}
		})
		return
	}

	handlers.HandleTCP(conn, proxyTarget, protocol)
}
