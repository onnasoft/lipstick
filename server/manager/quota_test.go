package manager

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/OnnaSoft/lipstick/server/auth"
	"github.com/OnnaSoft/lipstick/server/db"
	"github.com/OnnaSoft/lipstick/server/limits"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

// quotaManager returns a manager whose monthly usage is read from a SQLite database,
// with the given domain record in its store.
func quotaManager(t *testing.T, domain auth.Domain, defaultQuota int64) (*Manager, *gorm.DB) {
	t.Helper()
	conn, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "quota.db")), &gorm.Config{Logger: gormlogger.Discard})
	if err != nil {
		t.Fatal(err)
	}
	if err := conn.AutoMigrate(&db.DailyConsumption{}); err != nil {
		t.Fatal(err)
	}
	m := &Manager{authManager: &fakeAuth{domains: map[string]*auth.Domain{domain.Name: &domain}}}
	m.ConfigureLimits(limits.Settings{DomainMonthlyQuota: defaultQuota, DomainBandwidth: 0})
	m.SetQuotaStore(conn)
	return m, conn
}

func addUsage(t *testing.T, conn *gorm.DB, domain string, bytes int64) {
	t.Helper()
	now := time.Now()
	row := db.DailyConsumption{Domain: domain, Date: now.Truncate(24 * time.Hour), Month: now.Format("2006-01"), BytesUsed: bytes}
	if err := conn.Create(&row).Error; err != nil {
		t.Fatal(err)
	}
}

// Once a domain has used its monthly quota, new visitors are refused until the month
// changes. The usage comes from the database, so every server counts the same.
func TestMonthlyQuotaRefusesNewVisitors(t *testing.T) {
	m, conn := quotaManager(t, auth.Domain{Name: "app.example.com", MonthlyQuotaBytes: 1000}, 0)
	addUsage(t, conn, "app.example.com", 600)
	if r := m.quotaRefusal("app.example.com"); r != nil {
		t.Fatalf("600 of 1000 refused: %+v", r)
	}
	addUsage(t, conn, "app.example.com", 500)
	m.quota.cache = nil // the usage is cached for a few seconds; a fresh read sees the new row
	r := m.quotaRefusal("app.example.com")
	if r == nil || r.Reason != limits.ReasonQuota {
		t.Fatalf("1100 of 1000 admitted: %+v", r)
	}
	if r.RetryAfter <= 0 || r.RetryAfter > 32*24*time.Hour {
		t.Fatalf("retry after %v is not the start of next month", r.RetryAfter)
	}
}

// A domain that sets no quota uses the server default.
func TestQuotaFallsBackToServerDefault(t *testing.T) {
	m, conn := quotaManager(t, auth.Domain{Name: "app.example.com"}, 500)
	addUsage(t, conn, "app.example.com", 500)
	if r := m.quotaRefusal("app.example.com"); r == nil {
		t.Fatal("server default quota not applied")
	}
	other, _ := quotaManager(t, auth.Domain{Name: "app.example.com"}, 0)
	if r := other.quotaRefusal("app.example.com"); r != nil {
		t.Fatalf("no quota anywhere, yet refused: %+v", r)
	}
}

// A domain's own bandwidth wins over the server default; with neither, the pace is 0.
func TestBandwidthSettingWinsOverDefault(t *testing.T) {
	m, _ := quotaManager(t, auth.Domain{Name: "app.example.com", BandwidthBytesPerSecond: 2048}, 0)
	m.settings.DomainBandwidth = 4096
	if got := m.bandwidthFor("app.example.com"); got != 2048 {
		t.Fatalf("domain setting: %d", got)
	}
	if got := m.bandwidthFor("other.example.com"); got != 4096 {
		t.Fatalf("server default: %d", got)
	}
}

// Writes of a hub are paced: data beyond the domain's rate waits for the bucket.
func TestHubPacesVisitorDataToTheRate(t *testing.T) {
	hub := NewNetworkHub("app.example.com", nil, 64*1024)
	hub.pacer = limits.NewPacer(1000, nil)
	hub.pace(1000) // the first second is free
	start := time.Now()
	hub.pace(1000) // another second of data at 1000 bytes per second
	if waited := time.Since(start); waited < 900*time.Millisecond || waited > 1500*time.Millisecond {
		t.Fatalf("paced write waited %v, want about 1s", waited)
	}
}
