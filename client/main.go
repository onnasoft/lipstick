package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
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

func startClient(proxyTarget string) {
	fmt.Println("Connecting to", serverURL)
	retryDelay := 3 * time.Second
	headers := http.Header{}
	headers.Set("authorization", configuration.APISecret)

	for {
		conn, err := httpmanager.Connect(serverURL, headers)
		if err != nil {
			log.Printf("Error connecting to %s: %v\n", serverURL, err)
			time.Sleep(retryDelay)
			continue
		}
		helper.ReadUntilHeadersEnd(conn)

		fmt.Println("Connected to server at", serverURL)
		go checkConnection(conn)
		handleTickets(conn, proxyTarget)
		fmt.Println("Disconnected from server at", serverURL)
		time.Sleep(retryDelay)
	}
}

func checkConnection(connection io.ReadWriter) {
	writeMessage := []byte("ping")
	for {
		_, err := connection.Write(writeMessage)
		if err != nil {
			return
		}
		time.Sleep(30 * time.Second)
	}
}

// readMessage reads one ticket line from the server. The server sends only the
// ticket: a hex string, so it cannot contain separators or URL characters.
func readMessage(reader *bufio.Reader) (string, error) {
	line, err := reader.ReadString('\n')
	if err != nil {
		return "", fmt.Errorf("error reading until newline: %w", err)
	}
	line = strings.TrimRight(line, "\r\n")
	if line == "close" {
		return "", errors.New("connection closed by server")
	}
	if !isTicket(line) {
		return "", fmt.Errorf("invalid ticket from server: %q", line)
	}
	return line, nil
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

func handleTickets(connection net.Conn, proxyTarget string) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("ticket loop stopped: %v", r)
		}
	}()
	defer connection.Close()

	reader := bufio.NewReader(connection)
	for {
		ticket, err := readMessage(reader)
		if err != nil {
			fmt.Println(err)
			return
		}

		protocol, targetAddress := helper.ParseTargetEndpoint(proxyTarget)
		go establishConnection(protocol, targetAddress, ticket)
	}
}

func establishConnection(protocol, proxyTarget, ticket string) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("connection for ticket %s failed: %v", ticket, r)
		}
	}()
	url := serverURL + "/" + ticket

	connection, err := httpmanager.ConnectByAddress(url, nil)
	if err != nil {
		// No connection exists yet, so there is nothing to answer on.
		log.Printf("reverse connection failed: %v", err)
		return
	}
	defer connection.Close()

	b := make([]byte, 1024)
	n, err := connection.Read(b)
	if err != nil {
		connection.Write([]byte(helper.BadGatewayResponse))
		return
	}
	buff := b[:n]
	conn := helper.NewConnWithBuffer(connection, buff)

	if helper.IsHTTPRequest(string(buff)) {
		handlers.HandleHTTP(conn, proxyTarget, protocol)
		return
	}

	handlers.HandleTCP(conn, proxyTarget, protocol)
}
