package manager

import (
	"crypto/tls"
	"fmt"
	"net/http"
	"testing"
	"time"
)

// A tunnel client that is refused gets its reason counted: a wrong key counts as
// unauthorized, and a domain that does not exist as unknown_domain.
func TestRefusedTunnelSetupsAreCounted(t *testing.T) {
	st := startStack(t, stackOptions{})
	defer st.close()

	client := &http.Client{
		Timeout:   5 * time.Second,
		Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}},
	}
	upgrade := func(host, key string) {
		t.Helper()
		req, _ := http.NewRequest("GET", fmt.Sprintf("https://127.0.0.1:%s/", st.managerPort), nil)
		req.Host = host
		req.Header.Set("Connection", "Upgrade")
		req.Header.Set("Upgrade", "lipstick")
		if key != "" {
			req.Header.Set("Authorization", key)
		}
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
	}

	before := tunnelSetupFailures.Count("unauthorized")
	upgrade(testDomain, "wrong-key")
	if got := tunnelSetupFailures.Count("unauthorized") - before; got != 1 {
		t.Fatalf("unauthorized counted %d times, want 1", got)
	}

	before = tunnelSetupFailures.Count("unknown_domain")
	upgrade("no-such-domain.example", st.apiKey)
	if got := tunnelSetupFailures.Count("unknown_domain") - before; got != 1 {
		t.Fatalf("unknown_domain counted %d times, want 1", got)
	}
}
