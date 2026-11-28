package main

import (
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"strconv"
	"time"

	"net/http"
	_ "net/http/pprof"

	"github.com/OnnaSoft/lipstick/helper"
	"github.com/OnnaSoft/lipstick/logger"
	"github.com/OnnaSoft/lipstick/server/admin"
	"github.com/OnnaSoft/lipstick/server/audit"
	"github.com/OnnaSoft/lipstick/server/auth"
	"github.com/OnnaSoft/lipstick/server/config"
	"github.com/OnnaSoft/lipstick/server/db"
	"github.com/OnnaSoft/lipstick/server/limits"
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

	if conf.AdminSecretKey != "" {
		log.Println("note: admin_secret_key is no longer used. It only protected the admin API, which now uses accounts (lipstick-server admin create). Tunnel clients are not affected: they authenticate with the key of their domain. You can delete the line from config.yml.")
	}

	if config.CheckOnly() {
		fmt.Println("configuración válida")
		os.Exit(0)
	}

	// Console commands for admin accounts. They run here, on the server machine,
	// and never open a network port.
	// flag.Args holds what is left after the flags, so "-c file admin create ..." works.
	if flag.NArg() > 0 && flag.Arg(0) == "admin" {
		db.Migrate(conf.Database)
		conn, err := db.GetConnection(conf.Database)
		if err != nil {
			fmt.Fprintln(os.Stderr, "Error de base de datos:", err)
			os.Exit(1)
		}
		os.Exit(admin.RunConsole(auth.NewAdminStore(conn), flag.Args()[1:], admin.ReadTerminalPassword, os.Stdout, os.Stderr))
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
	// Read before the variable named manager shadows the package.
	clusterStatus := func() ([]manager.DomainStats, error) { return manager.ReadClusterStatus(conn, time.Now()) }
	manager := manager.SetupManager(tlsConfig)
	sniProxy.SetRoute(manager.RoutePassthrough)
	manager.StartRequestPersistence(requestStore)
	if conf.Cluster != nil {
		if err := manager.StartCluster(*conf.Cluster, tlsConfig, presenceBus); err != nil {
			log.Fatalf("cluster: %v", err)
		}
	}
	admin := admin.SetupAdmin(conf.Admin.Address, auth.MakeAdminStore(), tlsConfig)
	admin.SetDomainChangedHook(manager.DropHub)
	// The state of every server goes to the database, and the admin reads it back from
	// there, so any server of a cluster answers with the same numbers.
	hostname, _ := os.Hostname()
	manager.StartNodeStatus(conn, hostname+"/"+conf.Manager.Address)
	admin.SetStatsSource(clusterStatus)
	admin.SetRequestSource(manager.RecentRequests)
	// Every change is recorded before it is made; without this, changes are refused.
	admin.SetAudit(audit.NewStore(conn))
	// Visitor limits (per address and per domain, connections) and the admin request limit.
	limitSettings := limits.Settings(conf.Limits)
	manager.ConfigureLimits(limitSettings)
	manager.SetQuotaStore(conn)
	admin.SetRateLimit(limits.AdminLimiter(limitSettings, nil))
	checks := map[string]func() error{
		"database": func() error {
			sqlDB, err := conn.DB()
			if err != nil {
				return err
			}
			return sqlDB.Ping()
		},
	}
	if presenceBus != nil {
		checks["nats"] = func() error {
			if b, ok := presenceBus.(interface{ Connected() bool }); ok && b.Connected() {
				return nil
			}
			return errors.New("NATS is not connected")
		}
	}
	admin.SetHealthChecks(checks)

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
