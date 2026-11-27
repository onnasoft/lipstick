package config

import (
	"strings"
	"testing"
)

func natsErrs(n NatsConfig, cluster bool) string {
	var parts []string
	for _, e := range n.validateAuth(cluster) {
		parts = append(parts, e.Error())
	}
	return strings.Join(parts, "; ")
}

// Credentials never travel over a plain connection.
func TestNatsCredentialsNeedTLS(t *testing.T) {
	msg := natsErrs(NatsConfig{URL: "nats://n:4222", Username: "u", Password: "p"}, false)
	if !strings.Contains(msg, "tls://") {
		t.Fatalf("plain URL with credentials accepted: %q", msg)
	}
	if msg := natsErrs(NatsConfig{URL: "tls://n:4222", Username: "u", Password: "p"}, false); msg != "" {
		t.Fatalf("valid TLS credentials rejected: %q", msg)
	}
}

func TestNatsCredentialsMustBePaired(t *testing.T) {
	msg := natsErrs(NatsConfig{URL: "tls://n:4222", Username: "u"}, false)
	if !strings.Contains(msg, "set together") {
		t.Fatalf("username without password accepted: %q", msg)
	}
}

func TestNatsUserAndTokenTogetherRefused(t *testing.T) {
	msg := natsErrs(NatsConfig{URL: "tls://n:4222", Username: "u", Password: "p", Token: "t"}, false)
	if !strings.Contains(msg, "not both") {
		t.Fatalf("user and token accepted together: %q", msg)
	}
}

// A cluster needs authenticated TLS NATS. A single server may run without credentials.
func TestClusterNeedsAuthenticatedTLSNats(t *testing.T) {
	if msg := natsErrs(NatsConfig{URL: "nats://n:4222"}, true); !strings.Contains(msg, "credentials") || !strings.Contains(msg, "tls://") {
		t.Fatalf("cluster without credentials or TLS accepted: %q", msg)
	}
	if msg := natsErrs(NatsConfig{URL: "nats://n:4222"}, false); msg != "" {
		t.Fatalf("single server without credentials rejected: %q", msg)
	}
	if msg := natsErrs(NatsConfig{URL: "tls://n:4222", Token: "t"}, true); msg != "" {
		t.Fatalf("cluster with token over TLS rejected: %q", msg)
	}
}
