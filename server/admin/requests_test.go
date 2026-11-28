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
	a.SetRequestSource(func(q manager.RequestQuery) ([]manager.RequestRecord, error) {
		all := []manager.RequestRecord{
			{Time: time.Now(), Domain: "a.example.com", Method: "GET", Path: "/x", Status: 200},
			{Time: time.Now(), Domain: "b.example.com", Method: "POST", Path: "/y", Status: 201},
		}
		var out []manager.RequestRecord
		for _, r := range all {
			if q.Domain == "" || r.Domain == q.Domain {
				out = append(out, r)
			}
		}
		return out, nil
	})
	return a
}

func TestRequestsNeedNoKey(t *testing.T) {
	a := requestsAdmin()
	if rec := do(a, http.MethodGet, "/requests", ""); rec.Code != http.StatusOK {
		t.Fatalf("requests without key: got %d, want 200", rec.Code)
	}
}

func TestRequestsFilterByDomain(t *testing.T) {
	a := requestsAdmin()
	rec := do(a, http.MethodGet, "/requests?domain=b.example.com", "Bearer "+testKey)
	var got []manager.RequestRecord
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil || len(got) != 1 || got[0].Path != "/y" {
		t.Fatalf("filter wrong: %s (%v)", rec.Body.String(), err)
	}
}

func TestRequestsRejectBadLimit(t *testing.T) {
	a := requestsAdmin()
	for _, q := range []string{"limit=0", "limit=abc", "limit=-3"} {
		if rec := do(a, http.MethodGet, "/requests?"+q, "Bearer "+testKey); rec.Code != http.StatusBadRequest {
			t.Errorf("%s: got %d, want 400", q, rec.Code)
		}
	}
}

// A time range is passed to the source as a parsed time. A date in "to" covers the
// whole day, so the end is the next midnight.
func TestRequestsParseTimeRange(t *testing.T) {
	var got manager.RequestQuery
	a := newTestAdmin(testKey)
	a.SetRequestSource(func(q manager.RequestQuery) ([]manager.RequestRecord, error) {
		got = q
		return nil, nil
	})
	rec := do(a, http.MethodGet, "/requests?from=2026-11-01&to=2026-11-02", "Bearer "+testKey)
	if rec.Code != http.StatusOK {
		t.Fatalf("got %d: %s", rec.Code, rec.Body.String())
	}
	if got.From.Format("2006-01-02") != "2026-11-01" || got.To.Format("2006-01-02") != "2026-11-03" {
		t.Fatalf("range wrong: from=%v to=%v", got.From, got.To)
	}
}

func TestRequestsRejectBadTime(t *testing.T) {
	a := requestsAdmin()
	if rec := do(a, http.MethodGet, "/requests?from=yesterday", "Bearer "+testKey); rec.Code != http.StatusBadRequest {
		t.Fatalf("bad from: got %d, want 400", rec.Code)
	}
}
