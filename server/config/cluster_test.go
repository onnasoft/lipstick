package config

import (
	"strings"
	"testing"
)

func clusterConfig(c *ClusterConfig) AppConfig {
	cfg := validConfig()
	cfg.Cluster = c
	return cfg
}

func TestClusterAbsentIsValid(t *testing.T) {
	if err := clusterConfig(nil).Validate(); err != nil {
		t.Fatalf("single server rejected: %v", err)
	}
}

func TestClusterNeedsAdvertiseAddress(t *testing.T) {
	err := clusterConfig(&ClusterConfig{}).Validate()
	if err == nil || !strings.Contains(err.Error(), "advertise_address") {
		t.Fatalf("missing advertise_address not reported: %v", err)
	}
}

func TestClusterAdvertiseAddressMustBeHostPort(t *testing.T) {
	err := clusterConfig(&ClusterConfig{AdvertiseAddress: "server-a"}).Validate()
	if err == nil || !strings.Contains(err.Error(), "host:port") {
		t.Fatalf("address without port not reported: %v", err)
	}
	if err := clusterConfig(&ClusterConfig{AdvertiseAddress: "server-a.internal:5051"}).Validate(); err != nil {
		t.Fatalf("valid address rejected: %v", err)
	}
}
