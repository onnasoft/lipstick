package manager

import (
	"net"
	"net/http"
	"testing"
	"time"
)

// A disabled domain: its visitors get 503, its client is refused, and enabling it again
// brings both back.
func TestDisabledDomainRefusesVisitorsAndClient(t *testing.T) {
	backend, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer backend.Close()
	go func() {
		for {
			c, err := backend.Accept()
			if err != nil {
				return
			}
			go func() {
				defer c.Close()
				c.Write([]byte("HTTP/1.1 200 OK\r\nContent-Length: 2\r\nConnection: close\r\n\r\nok"))
			}()
		}
	}()

	st := startStack(t, stackOptions{})
	defer st.close()
	st.runClient(clientConfig{ProxyPass: backend.Addr().String()})
	c := st.visitorClient(t, st.caFile)
	c.Timeout = 10 * time.Second

	get := func() int {
		t.Helper()
		resp, err := c.Get("https://" + testDomain + "/")
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}
	if code := get(); code != http.StatusOK {
		t.Fatalf("before disabling: %d", code)
	}

	// What the admin API does on disable: set the flag, then drop the hub so the client goes.
	if err := st.auth.SetDomainDisabled(testDomain, true); err != nil {
		t.Fatal(err)
	}
	st.manager.DropHub(testDomain)

	if code := get(); code != http.StatusServiceUnavailable {
		t.Fatalf("visitor of a disabled domain got %d, want 503", code)
	}
	if n := st.manager.refusals.Totals()["domain_disabled"]; n < 1 {
		t.Fatalf("refusal not counted: %d", n)
	}

	// The client keeps retrying and is refused at the upgrade with its failure counted.
	before := tunnelSetupFailures.Count("domain_disabled")
	waitUntil(t, 20*time.Second, "the disabled client to be refused", func() bool {
		return tunnelSetupFailures.Count("domain_disabled") > before
	})

	if err := st.auth.SetDomainDisabled(testDomain, false); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(30 * time.Second)
	for get() != http.StatusOK {
		if time.Now().After(deadline) {
			t.Fatal("the domain did not serve again after it was enabled")
		}
		time.Sleep(200 * time.Millisecond)
	}
}
