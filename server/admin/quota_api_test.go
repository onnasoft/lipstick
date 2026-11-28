package admin

import (
	"net/http"
	"testing"
)

// Bandwidth and quota are whole numbers, 0 or more. Anything else is refused.
func TestBandwidthAndQuotaAreValidated(t *testing.T) {
	a := newTestAdmin(testKey)
	for _, body := range []string{
		`{"bandwidthBytesPerSecond":-1}`,
		`{"monthlyQuotaBytes":1.5}`,
		`{"monthlyQuotaBytes":"lots"}`,
	} {
		if rec := send(a, http.MethodPatch, "/domains/app.example.com", testKey, body); rec.Code != http.StatusBadRequest {
			t.Errorf("%s: %d, want 400", body, rec.Code)
		}
	}
	if rec := send(a, http.MethodPatch, "/domains/app.example.com", testKey, `{"bandwidthBytesPerSecond":1048576,"monthlyQuotaBytes":10737418240}`); rec.Code != http.StatusOK {
		t.Fatalf("valid values: %d %s", rec.Code, rec.Body.String())
	}
}
