package admin

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/OnnaSoft/lipstick/server/manager"
)

func requestsAdmin() *Admin {
	a := newTestAdmin(testKey)
	a.SetRequestSource(func(domain string, limit int) []manager.RequestRecord {
		all := []manager.RequestRecord{
			{Time: time.Now(), Domain: "a.example.com", Method: "GET", Path: "/x", Status: 200},
			{Time: time.Now(), Domain: "b.example.com", Method: "POST", Path: "/y", Status: 201},
		}
		var out []manager.RequestRecord
		for _, r := range all {
			if domain == "" || r.Domain == domain {
				out = append(out, r)
			}
		}
		return out
	})
	return a
}

func TestRequestsRequireAdminKey(t *testing.T) {
	a := requestsAdmin()
	if rec := do(a, http.MethodGet, "/requests", ""); rec.Code != http.StatusUnauthorized {
		t.Fatalf("requests without key: got %d, want 401", rec.Code)
	}
}

func TestRequestsFilterByDomain(t *testing.T) {
	a := requestsAdmin()
	rec := do(a, http.MethodGet, "/requests?domain=b.example.com", testKey)
	var got []manager.RequestRecord
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil || len(got) != 1 || got[0].Path != "/y" {
		t.Fatalf("filter wrong: %s (%v)", rec.Body.String(), err)
	}
}

func TestRequestsRejectBadLimit(t *testing.T) {
	a := requestsAdmin()
	for _, q := range []string{"limit=0", "limit=abc", "limit=-3"} {
		if rec := do(a, http.MethodGet, "/requests?"+q, testKey); rec.Code != http.StatusBadRequest {
			t.Errorf("%s: got %d, want 400", q, rec.Code)
		}
	}
}
