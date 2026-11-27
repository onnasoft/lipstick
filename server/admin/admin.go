package admin

import (
	"crypto/tls"
	"log"
	"net"

	"github.com/OnnaSoft/lipstick/logger"
	"github.com/OnnaSoft/lipstick/server/auth"
	"github.com/gin-gonic/gin"
)

type Admin struct {
	engine      *gin.Engine
	authManager auth.AuthManager
	addr        string
	adminKey    string
	tlsConfig   *tls.Config
}

// SetupAdmin builds the admin API. adminKey is the value clients must send in
// the Authorization header. When tlsConfig is not nil, the API is served over
// TLS, so the key is not sent in plain text.
func SetupAdmin(addr, adminKey string, tlsConfig *tls.Config) *Admin {
	gin.SetMode(gin.ReleaseMode)

	admin := &Admin{
		authManager: auth.MakeAuthManager(),
		addr:        addr,
		adminKey:    adminKey,
		tlsConfig:   tlsConfig,
	}

	configureRouter(admin)

	return admin
}

func (a *Admin) Listen() {
	var listener net.Listener
	var err error
	if a.tlsConfig != nil {
		listener, err = tls.Listen("tcp", a.addr, a.tlsConfig)
		logger.Default.Info("Listening admin (TLS) on ", a.addr)
	} else {
		listener, err = net.Listen("tcp", a.addr)
		logger.Default.Info("Listening admin on ", a.addr)
	}
	if err != nil {
		log.Fatalf("admin API cannot listen on %s: %v (check admin.address in the configuration)", a.addr, err)
	}

	if err := a.engine.RunListener(listener); err != nil {
		log.Fatalf("admin API stopped serving: %v", err)
	}
}
