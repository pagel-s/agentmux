package mcp

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"agentmux/internal/toolruntime"
	"agentmux/internal/tools"
)

const protocolVersion = "2024-11-05"

type Server struct {
	Version  string
	Executor toolruntime.Executor
}

type request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Result  any             `json:"result,omitempty"`
	Error   *responseError  `json:"error,omitempty"`
}

type responseError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type initializeResult struct {
	ProtocolVersion string         `json:"protocolVersion"`
	Capabilities    map[string]any `json:"capabilities"`
	ServerInfo      implementation `json:"serverInfo"`
}

type implementation struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

func New(version string, executor toolruntime.Executor) *Server {
	return &Server{
		Version:  version,
		Executor: executor,
	}
}

func (s *Server) Serve(reader io.Reader, writer io.Writer) error {
	scanner := bufio.NewScanner(reader)
	buf := make([]byte, 0, 64*1024)
	scanner.Buffer(buf, 1024*1024)

	encoder := json.NewEncoder(writer)
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}

		responses, err := s.handleMessage(line)
		if err != nil {
			return err
		}
		for _, resp := range responses {
			if err := encoder.Encode(resp); err != nil {
				return err
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	return nil
}

func (s *Server) handleMessage(line []byte) ([]response, error) {
	if len(line) == 0 {
		return nil, nil
	}

	if line[0] == '[' {
		var batch []json.RawMessage
		if err := json.Unmarshal(line, &batch); err != nil {
			return []response{protocolError(nil, -32700, "invalid json-rpc batch")}, nil
		}

		out := make([]response, 0, len(batch))
		for _, item := range batch {
			resp, ok := s.handleRequest(item)
			if ok {
				out = append(out, resp)
			}
		}
		return out, nil
	}

	resp, ok := s.handleRequest(line)
	if !ok {
		return nil, nil
	}
	return []response{resp}, nil
}

func (s *Server) handleRequest(raw []byte) (response, bool) {
	var req request
	if err := json.Unmarshal(raw, &req); err != nil {
		return protocolError(nil, -32700, "invalid json-rpc request"), true
	}
	if req.JSONRPC != "2.0" || strings.TrimSpace(req.Method) == "" {
		return protocolError(req.ID, -32600, "invalid json-rpc envelope"), true
	}

	isNotification := len(bytes.TrimSpace(req.ID)) == 0

	switch req.Method {
	case "initialize":
		if isNotification {
			return response{}, false
		}
		return response{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result: initializeResult{
				ProtocolVersion: protocolVersion,
				Capabilities: map[string]any{
					"tools": map[string]any{
						"listChanged": false,
					},
				},
				ServerInfo: implementation{
					Name:    "agentmux",
					Version: defaultString(s.Version, "dev"),
				},
			},
		}, true
	case "notifications/initialized", "$/cancelRequest":
		return response{}, false
	case "ping":
		if isNotification {
			return response{}, false
		}
		return response{JSONRPC: "2.0", ID: req.ID, Result: map[string]any{}}, true
	case "tools/list":
		if isNotification {
			return response{}, false
		}
		return response{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result: map[string]any{
				"tools": listTools(),
			},
		}, true
	case "tools/call":
		if isNotification {
			return response{}, false
		}
		result, callErr := s.handleToolCall(req.Params)
		if callErr != nil {
			return protocolError(req.ID, -32602, callErr.Error()), true
		}
		return response{JSONRPC: "2.0", ID: req.ID, Result: result}, true
	default:
		if isNotification {
			return response{}, false
		}
		return protocolError(req.ID, -32601, "method not found"), true
	}
}

func (s *Server) handleToolCall(raw json.RawMessage) (map[string]any, error) {
	var params struct {
		Name      string         `json:"name"`
		Arguments map[string]any `json:"arguments"`
	}
	if err := json.Unmarshal(raw, &params); err != nil {
		return nil, fmt.Errorf("invalid tool call params: %w", err)
	}
	spec, ok := tools.Lookup(params.Name)
	if !ok {
		return nil, fmt.Errorf("unknown tool %q", params.Name)
	}

	callResult, callErr := s.Executor.ExecuteArgs(spec.Name, defaultMap(params.Arguments))
	if callErr != nil {
		return structuredResult(callErr.DetailsJSON(), true), nil
	}
	return structuredResult(callResult, false), nil
}

func listTools() []map[string]any {
	specs := tools.Specs()
	out := make([]map[string]any, 0, len(specs))
	for _, spec := range specs {
		out = append(out, map[string]any{
			"name":        spec.Name,
			"title":       spec.Name,
			"description": spec.Description,
			"inputSchema": map[string]any{
				"type":       "object",
				"properties": schemaProperties(spec.Parameters),
				"required":   schemaRequired(spec.Parameters),
			},
		})
	}
	return out
}

func schemaProperties(params []tools.ToolParameter) map[string]any {
	properties := make(map[string]any, len(params))
	for _, param := range params {
		properties[param.Name] = map[string]any{
			"type":        param.Type,
			"description": param.Description,
		}
	}
	return properties
}

func schemaRequired(params []tools.ToolParameter) []string {
	required := make([]string, 0, len(params))
	for _, param := range params {
		if param.Required {
			required = append(required, param.Name)
		}
	}
	return required
}

func protocolError(id json.RawMessage, code int, message string) response {
	return response{
		JSONRPC: "2.0",
		ID:      id,
		Error: &responseError{
			Code:    code,
			Message: message,
		},
	}
}

func defaultMap(values map[string]any) map[string]any {
	if values == nil {
		return map[string]any{}
	}
	return values
}

func defaultString(value string, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}

func structuredResult(payload json.RawMessage, isError bool) map[string]any {
	var structured any
	if len(payload) > 0 && json.Valid(payload) {
		_ = json.Unmarshal(payload, &structured)
	}
	text := string(payload)
	if structured != nil {
		if pretty, err := json.MarshalIndent(structured, "", "  "); err == nil {
			text = string(pretty)
		}
	}

	result := map[string]any{
		"content": []map[string]any{
			{"type": "text", "text": text},
		},
	}
	if structuredMap, ok := structured.(map[string]any); ok {
		result["structuredContent"] = structuredMap
	}
	if isError {
		result["isError"] = true
	}
	return result
}
