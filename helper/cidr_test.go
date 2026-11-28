package helper

import (
	"net"
	"testing"
)

func TestEmptyRulesAllowEveryone(t *testing.T) {
	rules, err := ParseAccessRules("  ")
	if err != nil || rules != nil {
		t.Fatalf("empty rules: %v %v", rules, err)
	}
	if !SourceAllowed(net.ParseIP("203.0.113.9"), rules) {
		t.Fatal("no rules must allow everyone")
	}
}

func TestRulesAcceptNetworksAndSingleAddresses(t *testing.T) {
	rules, err := ParseAccessRules("10.0.0.0/8, 192.168.1.7, 2001:db8::/32")
	if err != nil {
		t.Fatal(err)
	}
	cases := map[string]bool{
		"10.200.1.1":  true,
		"192.168.1.7": true,
		"192.168.1.8": false, // a single address is exactly that address
		"8.8.8.8":     false,
		"2001:db8::1": true,
		"2001:db9::1": false,
	}
	for ip, want := range cases {
		if got := SourceAllowed(net.ParseIP(ip), rules); got != want {
			t.Errorf("%s: got %v want %v", ip, got, want)
		}
	}
}

func TestRulesRefuseInvalidItems(t *testing.T) {
	for _, bad := range []string{"10.0.0.0/33", "not-an-ip", "10.0.0.1/", "*", "10.0.0.0/8,,"} {
		if _, err := ParseAccessRules(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestNormalizeGivesCanonicalForm(t *testing.T) {
	got, err := NormalizeAccessRules([]string{"192.168.1.7", "10.1.2.3/8"})
	if err != nil {
		t.Fatal(err)
	}
	if got != "192.168.1.7/32,10.0.0.0/8" {
		t.Fatalf("canonical form %q", got)
	}
}

func TestNilIPRefusedWhenRulesExist(t *testing.T) {
	rules, _ := ParseAccessRules("10.0.0.0/8")
	if SourceAllowed(nil, rules) {
		t.Fatal("unknown source allowed")
	}
}

func TestRemoteIPOfAddresses(t *testing.T) {
	if ip := RemoteIP(&net.TCPAddr{IP: net.ParseIP("10.0.0.1"), Port: 1}); !ip.Equal(net.ParseIP("10.0.0.1")) {
		t.Fatal("TCP address")
	}
	if ip := RemoteIP(&net.UDPAddr{IP: net.ParseIP("10.0.0.2"), Port: 1}); !ip.Equal(net.ParseIP("10.0.0.2")) {
		t.Fatal("UDP address")
	}
	if ip := RemoteIP(nil); ip != nil {
		t.Fatal("nil address")
	}
}
