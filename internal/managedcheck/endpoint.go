package managedcheck

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"agentmux/internal/managedconfig"
)

// Public hosts are never contacted automatically. Private DNS names are
// resolved once and every answer must stay private; dialing uses that pinned
// answer, so credentials cannot follow a DNS rebinding or HTTP redirect.
func privateEndpointIPs(ctx context.Context, host string) ([]net.IP, bool) {
	if ip := net.ParseIP(host); ip != nil {
		if ip.IsLoopback() || ip.IsPrivate() {
			return []net.IP{ip}, true
		}
		return nil, false
	}
	host = strings.ToLower(host)
	if host != "localhost" && !strings.HasSuffix(host, ".localhost") && !strings.HasSuffix(host, ".internal") && !strings.HasSuffix(host, ".local") && !strings.HasSuffix(host, ".lan") {
		return nil, false
	}
	addresses, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil || len(addresses) == 0 {
		return nil, true
	}
	var ips []net.IP
	for _, address := range addresses {
		if !address.IP.IsLoopback() && !address.IP.IsPrivate() {
			return nil, false
		}
		ips = append(ips, address.IP)
	}
	return ips, true
}

func probeEndpoint(ctx context.Context, connection managedconfig.Connection, model, key string) (string, string) {
	return probeEndpointUsing(ctx, connection, model, key, nil)
}

func probeEndpointUsing(ctx context.Context, connection managedconfig.Connection, model, key string, do func(*http.Request) (*http.Response, error)) (string, string) {
	base, err := url.Parse(connection.BaseURL)
	if err != nil || base.Hostname() == "" || (base.Scheme != "http" && base.Scheme != "https") || base.User != nil || base.RawQuery != "" || base.Fragment != "" {
		return StatusError, "configured model endpoint URL is invalid"
	}
	ctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	ips, private := privateEndpointIPs(ctx, base.Hostname())
	if !private {
		return StatusWarning, "external or unclassified endpoint was not contacted; provider readiness is unverified"
	}
	if len(ips) == 0 {
		return StatusError, "configured private model endpoint could not be resolved within the metadata deadline"
	}
	base.Path = strings.TrimRight(base.Path, "/")
	if connection.Protocol == "anthropic" && !strings.HasSuffix(base.Path, "/v1") {
		base.Path += "/v1"
	}
	base.Path += "/models"
	base.RawPath = ""
	transport := &http.Transport{
		Proxy: nil, DisableKeepAlives: true,
		ResponseHeaderTimeout: probeTimeout, TLSHandshakeTimeout: probeTimeout,
		DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			_, port, err := net.SplitHostPort(address)
			if err != nil {
				return nil, err
			}
			var last error
			for _, ip := range ips {
				connection, err := (&net.Dialer{Timeout: probeTimeout / time.Duration(len(ips))}).DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
				if err == nil {
					return connection, nil
				}
				last = err
			}
			if last == nil {
				last = errors.New("no private endpoint address")
			}
			return nil, last
		},
	}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: probeTimeout, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base.String(), nil)
	if err != nil {
		return StatusError, "model metadata request could not be constructed"
	}
	req.Header.Set("Accept", "application/json")
	if connection.Protocol == "anthropic" {
		req.Header.Set("anthropic-version", "2023-06-01")
		if key != "" {
			req.Header.Set("x-api-key", key)
		}
	} else if key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	if do == nil {
		do = client.Do
	}
	response, err := do(req)
	if err != nil {
		return StatusError, "configured private model endpoint did not answer its bounded metadata probe"
	}
	defer response.Body.Close()
	switch {
	case response.StatusCode >= 300 && response.StatusCode < 400:
		return StatusWarning, "metadata endpoint redirected; redirect was not followed and readiness remains unverified"
	case response.StatusCode == http.StatusUnauthorized || response.StatusCode == http.StatusForbidden:
		return StatusError, "model endpoint rejected metadata authentication"
	case response.StatusCode == http.StatusNotFound || response.StatusCode == http.StatusMethodNotAllowed:
		return StatusWarning, "endpoint does not expose compatible model metadata; generation was not attempted"
	case response.StatusCode != http.StatusOK:
		return StatusError, "private model endpoint returned an unsuccessful metadata status"
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, 256*1024+1))
	if err != nil || len(data) > 256*1024 {
		return StatusWarning, "model metadata response was unreadable or exceeded the inspection limit"
	}
	var metadata struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if json.Unmarshal(data, &metadata) != nil || metadata.Data == nil {
		return StatusWarning, "endpoint replied but compatible model metadata could not be verified"
	}
	if len(metadata.Data) == 0 {
		return StatusError, "model metadata endpoint advertises no available models"
	}
	if model == "" {
		return StatusOK, "private model metadata endpoint is reachable; model selection remains harness-defined"
	}
	for _, available := range metadata.Data {
		if available.ID == model {
			return StatusOK, "private endpoint is reachable and advertises the configured model; no generation was requested"
		}
	}
	if connection.Protocol == "anthropic" {
		return StatusWarning, "endpoint is reachable; configured model or alias was not verified in its metadata"
	}
	return StatusError, "configured exact model ID is not advertised by the private endpoint"
}
