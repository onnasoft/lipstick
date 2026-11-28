package main

import (
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"strconv"

	"net/http"
	_ "net/http/pprof"

	"github.com/OnnaSoft/lipstick/helper"
	"github.com/OnnaSoft/lipstick/logger"
	"github.com/OnnaSoft/lipstick/server/admin"
	"github.com/OnnaSoft/lipstick/server/config"
	"github.com/OnnaSoft/lipstick/server/db"
	"github.com/OnnaSoft/lipstick/server/manager"
)

func main() {
	if os.Getenv("DEBUG") == "true" {
		go func() {
			log.Println("Iniciando servidor pprof en :6060")
			log.Println(http.ListenAndServe(":6060", nil))
		}()
	}

	conf, err := config.GetConfig()
	if err != nil {
		fmt.Fprintln(os.Stderr, "Error de configuración:")
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	if config.CheckOnly() {
		fmt.Println("configuración válida")
		os.Exit(0)
	}

	tlsConfig, err := conf.TLS.Prepare()
	if err != nil {
		fmt.Fprintln(os.Stderr, "Error de TLS:", err)
		os.Exit(1)
	}

	rawProxy, err := net.Listen("tcp", conf.Proxy.Address)
	if err != nil {
		fmt.Fprintf(os.Stderr, "No se puede escuchar el proxy en %s: %v (revisa proxy.address)\n", conf.Proxy.Address, err)
		os.Exit(1)
	}
	sniProxy := helper.NewSNIListener(rawProxy, tlsConfig)
	proxy := helper.NewListenerManager(sniProxy)
	var dtlsProxy net.PacketConn
	if conf.Proxy.UDP {
		dtlsProxy, err = net.ListenPacket("udp", conf.Proxy.Address)
		if err != nil {
			fmt.Fprintf(os.Stderr, "No se puede escuchar DTLS en %s: %v (revisa proxy.address)\n", conf.Proxy.Address, err)
			os.Exit(1)
		}
	}

	interrupt := make(chan os.Signal, 1)
	signal.Notify(interrupt, os.Interrupt)

	db.Migrate(conf.Database)
	conn, err := db.GetConnection(conf.Database)
	if err != nil {
		log.Fatal(err)
	}

	// The presence bus is NATS. It is opened before the manager variable shadows the package.
	var presenceBus manager.PresenceBus
	if conf.Cluster != nil {
		presenceBus, err = manager.NatsBus()
		if err != nil {
			log.Fatalf("cluster: %v", err)
		}
	}
	requestStore := manager.NewGormRequestStore(conn)
	manager := manager.SetupManager(tlsConfig)
	sniProxy.SetRoute(manager.RoutePassthrough)
	manager.StartRequestPersistence(requestStore)
	if conf.Cluster != nil {
		if err := manager.StartCluster(*conf.Cluster, tlsConfig, presenceBus); err != nil {
			log.Fatalf("cluster: %v", err)
		}
	}
	admin := admin.SetupAdmin(conf.Admin.Address, conf.AdminSecretKey, tlsConfig)
	admin.SetDomainChangedHook(manager.DropHub)
	admin.SetStatsSource(manager.Snapshot)
	admin.SetRequestSource(manager.RecentRequests)

	if conf.IntranetPorts != nil {
		policy := make(map[string][]*net.IPNet, len(conf.IntranetPorts.Domains))
		for _, d := range conf.IntranetPorts.Domains {
			policy[d.Name] = d.DomainNetworks()
		}
		manager.ConfigureIntranetPorts(conf.IntranetPorts.BindAddress, conf.IntranetPorts.PortRange[0], conf.IntranetPorts.PortRange[1], policy)
		logger.Default.Info("Intranet ports enabled on", conf.IntranetPorts.BindAddress, "range", conf.IntranetPorts.PortRange)
	}

	proxy.OnListen(func() { logger.Default.Info("Listening proxy on ", conf.Proxy.Address) })
	proxy.OnClose(func() { logger.Default.Info("Proxy closed") })
	proxy.OnTCPConn(func(c net.Conn) {
		go manager.HandleTCPConn(c)
	})
	proxy.OnHTTPConn(func(c net.Conn, req *http.Request) {
		go manager.HandleHTTPConn(c, req)
	})

	go manager.Listen()
	go admin.Listen()
	go proxy.ListenAndServe()
	if dtlsProxy != nil {
		_, portText, _ := net.SplitHostPort(conf.Proxy.Address)
		dtlsPort, _ := strconv.Atoi(portText)
		go manager.ServeDTLSProxy(dtlsProxy, tlsConfig, dtlsPort)
		logger.Default.Info("Listening DTLS proxy on", conf.Proxy.Address)
	}
	<-interrupt
	fmt.Println("Desconectando...")
	manager.Close()
}
