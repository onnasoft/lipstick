package db

import (
	"strings"
	"testing"

	"github.com/OnnaSoft/lipstick/server/config"
)

// The configured port must reach the connection; before the fix it was dropped and the
// default 5432 was always used.
func TestDSNCarriesTheConfiguredPort(t *testing.T) {
	conf := config.DatabaseConfig{Host: "db.internal", Port: 6543, User: "u", Database: "d", SSLMode: "disable", Password: "p"}
	dsn := databaseDSN(conf)
	if !strings.Contains(dsn, "port=6543") {
		t.Fatalf("port missing from %q", dsn)
	}
}
