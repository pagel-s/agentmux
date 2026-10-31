package managedcheck

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"agentmux/internal/managedconfig"
	"agentmux/internal/modelmeta"
)

type ModelDiscovery struct {
	Metadata modelmeta.Metadata `json:"metadata"`
	Source   string             `json:"source"`
	Warning  string             `json:"warning,omitempty"`
}

// DiscoverModelMetadata performs metadata requests only. Public destinations,
// redirects, proxy settings and DNS rebinding are excluded. Exact model IDs
// must match; names are never used to infer context or capabilities. Owner
// fields win over advertised values. Missing fields keep conservative defaults.
func DiscoverModelMetadata(ctx context.Context, connection managedconfig.Connection, model, key string) ModelDiscovery {
	result := ModelDiscovery{Metadata: connection.ModelMetadata, Source: "owner/default"}
	base, err := url.Parse(connection.BaseURL)
	if err != nil || base.Hostname() == "" || (base.Scheme != "http" && base.Scheme != "https") || base.User != nil || base.RawQuery != "" || base.Fragment != "" {
		result.Warning = "invalid metadata endpoint"
		return result
	}
	ctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	ips, private := privateEndpointIPs(ctx, base.Hostname())
	if !private {
		result.Warning = "public endpoint metadata was not contacted"
		return result
	}
	if len(ips) == 0 {
		result.Warning = "private metadata endpoint could not be resolved"
		return result
	}
	transport := &http.Transport{Proxy: nil, DisableKeepAlives: true, ResponseHeaderTimeout: probeTimeout, TLSHandshakeTimeout: probeTimeout, DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
		_, port, err := net.SplitHostPort(address)
		if err != nil {
			return nil, err
		}
		for _, ip := range ips {
			conn, err := (&net.Dialer{Timeout: probeTimeout / time.Duration(len(ips))}).DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
			if err == nil {
				return conn, nil
			}
		}
		return nil, errors.New("private metadata dial failed")
	}}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: probeTimeout, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	request := func(method string, target url.URL, body []byte) ([]byte, error) {
		req, err := http.NewRequestWithContext(ctx, method, target.String(), bytes.NewReader(body))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Accept", "application/json")
		if len(body) > 0 {
			req.Header.Set("Content-Type", "application/json")
		}
		if key != "" {
			req.Header.Set("Authorization", "Bearer "+key)
		}
		response, err := client.Do(req)
		if err != nil {
			return nil, err
		}
		defer response.Body.Close()
		if response.StatusCode != 200 {
			return nil, errors.New("metadata status not successful")
		}
		data, err := io.ReadAll(io.LimitReader(response.Body, 256*1024+1))
		if err != nil || len(data) > 256*1024 {
			return nil, errors.New("metadata response exceeds limit")
		}
		return data, nil
	}
	var advertised modelmeta.Metadata
	if connection.Protocol == "ollama" {
		show := *base
		show.Path = strings.TrimSuffix(strings.TrimRight(show.Path, "/"), "/v1") + "/api/show"
		show.RawPath = ""
		body, _ := json.Marshal(map[string]any{"model": model, "verbose": false})
		data, err := request(http.MethodPost, show, body)
		if err == nil {
			advertised, err = parseOllamaMetadata(data)
			if err == nil {
				result.Source = "ollama/api/show"
			}
		}
	}
	if result.Source == "owner/default" {
		models := *base
		models.Path = strings.TrimRight(models.Path, "/") + "/models"
		models.RawPath = ""
		data, err := request(http.MethodGet, models, nil)
		if err != nil {
			result.Warning = "metadata unavailable; conservative defaults or owner overrides retained"
			return result
		}
		advertised, err = parseModelListMetadata(data, model)
		if err != nil {
			result.Warning = "exact model metadata unavailable; conservative defaults or owner overrides retained"
			return result
		}
		result.Source = "openai/models"
	}
	result.Metadata = modelmeta.Overlay(advertised, connection.ModelMetadata)
	if err := result.Metadata.Validate(); err != nil {
		result.Metadata = connection.ModelMetadata
		result.Source = "owner/default"
		result.Warning = "advertised metadata exceeded supported bounds; owner/default values retained"
	} else if result.Metadata.Effective().ContextWindow <= 4096 {
		result.Warning = "Pi 1.4.0 reserves 4096 context tokens and may clamp this context to one output token; select a verified larger engine context or a compatible runtime; metadata was not enlarged"
	}
	return result
}

func parseModelListMetadata(data []byte, id string) (modelmeta.Metadata, error) {
	var list struct {
		Data []map[string]json.RawMessage `json:"data"`
	}
	if json.Unmarshal(data, &list) != nil {
		return modelmeta.Metadata{}, errors.New("invalid models list")
	}
	for _, record := range list.Data {
		var found string
		json.Unmarshal(record["id"], &found)
		if found != id {
			continue
		}
		metadata := parseMetadataRecord(record)
		return metadata, metadata.Validate()
	}
	return modelmeta.Metadata{}, errors.New("model not advertised")
}

func parseMetadataRecord(record map[string]json.RawMessage) modelmeta.Metadata {
	var m modelmeta.Metadata
	positive := func(keys ...string) int {
		for _, key := range keys {
			var value int
			if json.Unmarshal(record[key], &value) == nil && value > 0 {
				return value
			}
		}
		return 0
	}
	m.ContextWindow = positive("context_window", "contextWindow", "max_model_len", "context_length", "max_context_length", "max_input_tokens")
	m.MaxOutput = positive("max_output_tokens", "max_completion_tokens", "maxTokens", "max_tokens")
	for _, key := range []string{"input", "input_modalities"} {
		if json.Unmarshal(record[key], &m.Input) == nil && len(m.Input) > 0 {
			break
		}
	}
	boolValue := func(keys ...string) *bool {
		for _, key := range keys {
			if bytes.Equal(bytes.TrimSpace(record[key]), []byte("null")) {
				continue
			}
			var value bool
			if json.Unmarshal(record[key], &value) == nil {
				return &value
			}
		}
		return nil
	}
	m.Reasoning = boolValue("reasoning", "supports_reasoning")
	m.Tools = boolValue("tools", "supports_tools", "supports_tool_calling")
	return m
}

func parseOllamaMetadata(data []byte) (modelmeta.Metadata, error) {
	var info struct {
		ModelInfo    map[string]json.RawMessage `json:"model_info"`
		Capabilities []string                   `json:"capabilities"`
		Parameters   string                     `json:"parameters"`
	}
	if json.Unmarshal(data, &info) != nil || info.ModelInfo == nil {
		return modelmeta.Metadata{}, errors.New("invalid Ollama metadata")
	}
	m := modelmeta.Metadata{}
	context := 0
	for key, raw := range info.ModelInfo {
		if strings.HasSuffix(key, ".context_length") {
			var value int
			if json.Unmarshal(raw, &value) == nil && value > 0 && (context == 0 || value < context) {
				context = value
			}
		}
	}
	m.ContextWindow = context
	for _, line := range strings.Split(info.Parameters, "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 {
			continue
		}
		value, err := strconv.Atoi(fields[1])
		if err != nil || value <= 0 {
			continue
		}
		switch fields[0] {
		case "num_ctx":
			if m.ContextWindow == 0 || value < m.ContextWindow {
				m.ContextWindow = value
			}
		case "num_predict":
			m.MaxOutput = value
		}
	}
	if info.Capabilities != nil {
		reasoning, tools := false, false
		m.Input = []string{"text"}
		for _, capability := range info.Capabilities {
			switch capability {
			case "vision":
				m.Input = append(m.Input, "image")
			case "thinking":
				reasoning = true
			case "tools":
				tools = true
			}
		}
		m.Reasoning = &reasoning
		m.Tools = &tools
	}
	return m, m.Validate()
}
