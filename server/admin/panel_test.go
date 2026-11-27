package admin

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/OnnaSoft/lipstick/server/manager"
)

func statsAdmin() *Admin {
	a := newTestAdmin(testKey)
	a.SetStatsSource(func() []manager.DomainStats {
		return []manager.DomainStats{{Domain: "robot-42.example.com", Status: "active", Clients: 1, ActiveSessions: 3, IntranetPorts: []int{5105}}}
	})
	return a
}

func TestStatsRequiresAdminKey(t *testing.T) {
	a := statsAdmin()
	if rec := do(a, http.MethodGet, "/stats", ""); rec.Code != http.StatusUnauthorized {
		t.Fatalf("stats without key: got %d, want 401", rec.Code)
	}
	if rec := do(a, http.MethodGet, "/stats", "wrong"); rec.Code != http.StatusUnauthorized {
		t.Fatalf("stats with wrong key: got %d, want 401", rec.Code)
	}
}

func TestStatsReturnsLiveState(t *testing.T) {
	a := statsAdmin()
	rec := do(a, http.MethodGet, "/stats", testKey)
	if rec.Code != http.StatusOK {
		t.Fatalf("got %d, want 200", rec.Code)
	}
	var got []manager.DomainStats
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Domain != "robot-42.example.com" || got[0].ActiveSessions != 3 || got[0].IntranetPorts[0] != 5105 {
		t.Fatalf("unexpected stats: %s", rec.Body.String())
	}
}

// Without a source the endpoint answers an empty list, not an error.
func TestStatsWithoutSourceIsEmpty(t *testing.T) {
	a := newTestAdmin(testKey)
	rec := do(a, http.MethodGet, "/stats", testKey)
	if rec.Code != http.StatusOK || strings.TrimSpace(rec.Body.String()) != "[]" {
		t.Fatalf("got %d %q, want 200 []", rec.Code, rec.Body.String())
	}
}

// The panel is public HTML with no data in it, and carries the security headers.
func TestPanelHasNoDataAndSecurityHeaders(t *testing.T) {
	a := statsAdmin()
	rec := do(a, http.MethodGet, "/ui", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("panel: got %d", rec.Code)
	}
	body := rec.Body.String()
	if strings.Contains(body, "robot-42") || strings.Contains(body, testKey) {
		t.Fatal("panel HTML contains data or the admin key")
	}
	for _, h := range []string{"Content-Security-Policy", "X-Frame-Options", "X-Content-Type-Options"} {
		if rec.Header().Get(h) == "" {
			t.Errorf("missing header %s", h)
		}
	}
}
