package admin

import (
	"crypto/tls"
	"log"

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
	if a.tlsConfig == nil {
		log.Fatal("admin API has no TLS configuration; TLS is required")
	}
	logger.Default.Info("Listening admin (TLS) on ", a.addr)
	listener, err := tls.Listen("tcp", a.addr, a.tlsConfig)
	if err != nil {
		log.Fatalf("admin API cannot listen on %s: %v (check admin.address in the configuration)", a.addr, err)
	}

	if err := a.engine.RunListener(listener); err != nil {
		log.Fatalf("admin API stopped serving: %v", err)
	}
}
