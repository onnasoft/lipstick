package helper

import (
	"errors"
	"fmt"
	"net"
	"strings"
)

// ParseAccessRules parses a domain's allowed sources: a comma-separated list of
// CIDR networks, or single addresses (a single IPv4 becomes /32, an IPv6 /128).
// An empty string means no restriction and returns nil.
func ParseAccessRules(raw string) ([]*net.IPNet, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	var nets []*net.IPNet
	for _, item := range strings.Split(raw, ",") {
		n, err := parseOneRule(strings.TrimSpace(item))
		if err != nil {
			return nil, err
		}
		nets = append(nets, n)
	}
	return nets, nil
}

// ValidateAccessRules reports whether every item is a valid network or address.
func ValidateAccessRules(items []string) error {
	for _, item := range items {
		if _, err := parseOneRule(strings.TrimSpace(item)); err != nil {
			return err
		}
	}
	return nil
}

// NormalizeAccessRules returns the canonical comma-separated form stored for a domain.
func NormalizeAccessRules(items []string) (string, error) {
	var out []string
	for _, item := range items {
		n, err := parseOneRule(strings.TrimSpace(item))
		if err != nil {
			return "", err
		}
		out = append(out, n.String())
	}
	return strings.Join(out, ","), nil
}

func parseOneRule(item string) (*net.IPNet, error) {
	if item == "" {
		return nil, errors.New("empty rule")
	}
	if strings.Contains(item, "/") {
		_, n, err := net.ParseCIDR(item)
		if err != nil {
			return nil, fmt.Errorf("%q is not a valid CIDR network", item)
		}
		return n, nil
	}
	ip := net.ParseIP(item)
	if ip == nil {
		return nil, fmt.Errorf("%q is not an IP address or CIDR network", item)
	}
	if v4 := ip.To4(); v4 != nil {
		return &net.IPNet{IP: v4, Mask: net.CIDRMask(32, 32)}, nil
	}
	return &net.IPNet{IP: ip, Mask: net.CIDRMask(128, 128)}, nil
}

// SourceAllowed reports whether ip is inside one of the networks. An empty list
// allows everyone. A nil IP is refused when there is a list.
func SourceAllowed(ip net.IP, rules []*net.IPNet) bool {
	if len(rules) == 0 {
		return true
	}
	if ip == nil {
		return false
	}
	for _, n := range rules {
		if n.Contains(ip) {
			return true
		}
	}
	return false
}

// RemoteIP returns the IP of a connection's remote address, or nil.
func RemoteIP(addr net.Addr) net.IP {
	switch a := addr.(type) {
	case *net.TCPAddr:
		return a.IP
	case *net.UDPAddr:
		return a.IP
	}
	if addr == nil {
		return nil
	}
	host, _, err := net.SplitHostPort(addr.String())
	if err != nil {
		return nil
	}
	return net.ParseIP(host)
}
