package config

import (
	"strings"
	"testing"
)

func withIntranet(p *IntranetPortsConfig) AppConfig {
	c := validConfig()
	c.IntranetPorts = p
	return c
}

func validIntranet() *IntranetPortsConfig {
	return &IntranetPortsConfig{
		BindAddress: "10.0.0.10",
		PortRange:   []int{5100, 5199},
		Domains: []IntranetDomainConfig{
			{Name: "impresora.oficina.example.com", AllowedCIDRs: []string{"10.20.0.0/16"}},
		},
	}
}

func TestIntranetPortsAreOptional(t *testing.T) {
	if err := validConfig().Validate(); err != nil {
		t.Fatalf("config without intranet_ports should be valid: %v", err)
	}
}

func TestIntranetPortsValid(t *testing.T) {
	if err := withIntranet(validIntranet()).Validate(); err != nil {
		t.Fatalf("valid intranet policy rejected: %v", err)
	}
}

func TestIntranetBindMustBeSpecific(t *testing.T) {
	for _, addr := range []string{"0.0.0.0", "::", "not-an-ip"} {
		p := validIntranet()
		p.BindAddress = addr
		if err := withIntranet(p).Validate(); err == nil || !strings.Contains(err.Error(), "bind_address") {
			t.Fatalf("bind %q accepted: %v", addr, err)
		}
	}
}

func TestIntranetPortRangeChecks(t *testing.T) {
	p := validIntranet()
	p.PortRange = []int{5199, 5100}
	if err := withIntranet(p).Validate(); err == nil || !strings.Contains(err.Error(), "port_range") {
		t.Fatalf("reversed range accepted: %v", err)
	}
	p.PortRange = []int{5050, 5060}
	if err := withIntranet(p).Validate(); err == nil || !strings.Contains(err.Error(), "overlaps") {
		t.Fatalf("range covering the proxy port accepted: %v", err)
	}
}

func TestIntranetDomainPolicy(t *testing.T) {
	p := validIntranet()
	p.Domains = []IntranetDomainConfig{{Name: "a.example.com"}}
	if err := withIntranet(p).Validate(); err == nil || !strings.Contains(err.Error(), "allowed_cidrs is required") {
		t.Fatalf("domain without networks accepted: %v", err)
	}

	p = validIntranet()
	p.Domains[0].AllowedCIDRs = []string{"0.0.0.0/0"}
	if err := withIntranet(p).Validate(); err == nil || !strings.Contains(err.Error(), "whole internet") {
		t.Fatalf("whole internet accepted: %v", err)
	}

	p = validIntranet()
	p.Domains = append(p.Domains, p.Domains[0])
	if err := withIntranet(p).Validate(); err == nil || !strings.Contains(err.Error(), "listed twice") {
		t.Fatalf("duplicate domain accepted: %v", err)
	}

	p = validIntranet()
	p.Domains = nil
	if err := withIntranet(p).Validate(); err == nil || !strings.Contains(err.Error(), "domains is empty") {
		t.Fatalf("empty domain list accepted: %v", err)
	}
}
