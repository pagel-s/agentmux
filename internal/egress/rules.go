// Package egress supplies a fail-closed, per-worker HTTP proxy. A worker must
// also run in a network namespace without host networking: proxy environment
// variables alone are not an isolation boundary.
package egress

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Endpoint grants an exact origin, not a URL path. IPs optionally pins the
// owner's explicit addresses (and permits private endpoints). Without pins,
// DNS is resolved once at startup and non-public answers are rejected.
type Endpoint struct {
	Origin string   `json:"origin" yaml:"origin"`
	IPs    []string `json:"ips,omitempty" yaml:"ips,omitempty"`
}

type rule struct {
	scheme, host, port, authority string
	ips                           []netip.Addr
}

// Validate checks syntax without accessing DNS or the network.
func Validate(endpoints []Endpoint) error {
	if len(endpoints) == 0 || len(endpoints) > 32 {
		return errors.New("egress requires 1–32 exact origins")
	}
	seen := map[string]bool{}
	for _, e := range endpoints {
		r, err := parseOrigin(e.Origin)
		if err != nil {
			return err
		}
		key := r.scheme + "://" + r.authority
		if seen[key] {
			return errors.New("duplicate egress origin")
		}
		seen[key] = true
		if len(e.IPs) > 16 {
			return errors.New("egress origin has too many pinned addresses")
		}
		for _, value := range e.IPs {
			ip, err := netip.ParseAddr(value)
			if err != nil || !usable(ip) {
				return errors.New("invalid pinned egress address")
			}
			if literal, e := netip.ParseAddr(r.host); e == nil && literal.Unmap() != ip.Unmap() {
				return errors.New("IP-literal egress origin cannot pin a different address")
			}
		}
	}
	return nil
}

// IsSubset checks configuration attenuation without DNS or network access.
// A child cannot replace a parent's DNS selection with arbitrary explicit
// pins, or discard the parent's explicit pins and re-resolve its hostname.
func IsSubset(child, parent []Endpoint) bool {
	if len(child) == 0 {
		return true
	}
	if Validate(child) != nil || Validate(parent) != nil {
		return false
	}
	parents := map[string]Endpoint{}
	for _, e := range parent {
		r, _ := parseOrigin(e.Origin)
		parents[r.scheme+"://"+r.authority] = e
	}
	for _, e := range child {
		r, _ := parseOrigin(e.Origin)
		p, ok := parents[r.scheme+"://"+r.authority]
		if !ok {
			return false
		}
		_, literalErr := netip.ParseAddr(r.host)
		if literalErr == nil {
			continue
		}
		if (len(e.IPs) == 0) != (len(p.IPs) == 0) {
			return false
		}
		pins := map[netip.Addr]bool{}
		for _, value := range p.IPs {
			ip, _ := netip.ParseAddr(value)
			pins[ip.Unmap()] = true
		}
		for _, value := range e.IPs {
			ip, _ := netip.ParseAddr(value)
			if !pins[ip.Unmap()] {
				return false
			}
		}
	}
	return true
}

func parseOrigin(origin string) (rule, error) {
	u, err := url.Parse(origin)
	if err != nil || u.Opaque != "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") || u.RawPath != "" || (u.Scheme != "http" && u.Scheme != "https") {
		return rule{}, errors.New("egress origin must be an exact http(s) origin without credentials, query or path")
	}
	host := strings.ToLower(u.Hostname())
	if host == "" || strings.HasSuffix(host, ".") || strings.ContainsAny(host, "%\\ \t\r\n*") {
		return rule{}, errors.New("invalid egress hostname")
	}
	for _, c := range host {
		if c > 127 {
			return rule{}, errors.New("egress hostname must use ASCII/Punycode")
		}
	}
	if _, err := netip.ParseAddr(host); err != nil {
		for _, label := range strings.Split(host, ".") {
			if label == "" || len(label) > 63 || strings.HasPrefix(label, "-") || strings.HasSuffix(label, "-") {
				return rule{}, errors.New("invalid egress hostname")
			}
			for _, c := range label {
				if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-') {
					return rule{}, errors.New("invalid egress hostname")
				}
			}
		}
	}
	port := u.Port()
	if port == "" {
		port = "80"
		if u.Scheme == "https" {
			port = "443"
		}
	}
	n, err := strconv.Atoi(port)
	if err != nil || n < 1 || n > 65535 {
		return rule{}, errors.New("invalid egress port")
	}
	port = strconv.Itoa(n)
	return rule{scheme: u.Scheme, host: host, port: port, authority: net.JoinHostPort(host, port)}, nil
}

func usable(ip netip.Addr) bool {
	return ip.IsValid() && !ip.IsUnspecified() && !ip.IsMulticast() && ip.Zone() == ""
}
func public(ip netip.Addr) bool {
	ip = ip.Unmap()
	return usable(ip) && ip.IsGlobalUnicast() && !ip.IsPrivate() && !ip.IsLoopback() && !ip.IsLinkLocalUnicast()
}

func resolve(ctx context.Context, endpoints []Endpoint) (map[string]rule, error) {
	if err := Validate(endpoints); err != nil {
		return nil, err
	}
	out := map[string]rule{}
	for _, e := range endpoints {
		r, _ := parseOrigin(e.Origin)
		if len(e.IPs) > 0 {
			for _, v := range e.IPs {
				ip, _ := netip.ParseAddr(v)
				r.ips = append(r.ips, ip.Unmap())
			}
		} else if ip, err := netip.ParseAddr(r.host); err == nil {
			if !usable(ip) {
				return nil, errors.New("invalid egress address")
			}
			r.ips = []netip.Addr{ip.Unmap()}
		} else {
			lookupCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
			ips, err := net.DefaultResolver.LookupNetIP(lookupCtx, "ip", r.host)
			cancel()
			if err != nil {
				return nil, fmt.Errorf("egress origin DNS lookup failed: %s", r.host)
			}
			for _, ip := range ips {
				if !public(ip) {
					return nil, fmt.Errorf("egress DNS origin requires explicit IP pins for non-public address: %s", r.host)
				}
				r.ips = append(r.ips, ip.Unmap())
			}
		}
		if len(r.ips) == 0 || len(r.ips) > 16 {
			return nil, errors.New("egress origin requires 1–16 resolved addresses")
		}
		out[r.scheme+"://"+r.authority] = r
	}
	return out, nil
}
