package handlers

import (
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"time"

	"github.com/OnnaSoft/lipstick/client/tlsconf"
	"github.com/OnnaSoft/lipstick/helper"
)

// relayDrainTimeout bounds how long the client waits for the tunnel side after
// the service has finished its reply.
const relayDrainTimeout = 10 * time.Second

// HandleTCP relays a tunnel connection to a local service. Each side can finish
// on its own: when the tunnel side is done, the service gets end-of-file and can
// still send its reply, and when the service is done, the tunnel side is told.
func HandleTCP(connection net.Conn, proxyTarget, protocol string) {
	var err error
	var serverConnection net.Conn
	if protocol == "tcp" || protocol == "http" {
		serverConnection, err = net.Dial("tcp", proxyTarget)
	} else {
		serverConnection, err = tls.Dial("tcp", proxyTarget, tlsconf.Config(""))
	}
	if err != nil {
		fmt.Println("Error al conectar al servidor TCP:", err)
		sendErrorResponse(connection)
		return
	}
	defer serverConnection.Close()

	// tunnel -> service
	toService := make(chan struct{})
	go func() {
		defer close(toService)
		io.Copy(serverConnection, connection)
		helper.CloseWrite(serverConnection)
	}()

	// service -> tunnel: runs until the service finishes its reply.
	io.Copy(connection, serverConnection)
	helper.CloseWrite(connection)

	connection.SetReadDeadline(time.Now().Add(relayDrainTimeout))
	<-toService
}
