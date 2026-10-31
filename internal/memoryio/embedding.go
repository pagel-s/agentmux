package memoryio

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"math"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Embed invokes only the explicitly selected OpenAI-compatible embeddings
// endpoint. The caller opts into sending authorized content to that provider.
func Embed(ctx context.Context, base, model, key, text string) ([]float64, error) {
	u, err := url.Parse(base)
	if err != nil || u == nil || (u.Scheme != "https" && u.Scheme != "http") || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return nil, errors.New("embedding base must be an http(s) URL without credentials, query or fragment")
	}
	if len(model) < 1 || len(model) > 128 || len(text) > 512<<10 {
		return nil, errors.New("embedding model or input exceeds bounds")
	}
	u.Path = strings.TrimRight(u.Path, "/") + "/embeddings"
	raw, _ := json.Marshal(map[string]any{"model": model, "input": text, "encoding_format": "float"})
	ctx, stop := context.WithTimeout(ctx, 30*time.Second)
	defer stop()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u.String(), bytes.NewReader(raw))
	if err != nil {
		return nil, errors.New("embedding request invalid")
	}
	req.Header.Set("Content-Type", "application/json")
	if key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	client := &http.Client{Timeout: 30 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("embedding redirects are not followed") }}
	res, err := client.Do(req)
	if err != nil {
		return nil, errors.New("embedding provider transport failed")
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, errors.New("embedding provider rejected the request")
	}
	body, err := io.ReadAll(io.LimitReader(res.Body, 2<<20+1))
	if err != nil || len(body) > 2<<20 {
		return nil, errors.New("embedding response exceeds bound")
	}
	var out struct {
		Data []struct {
			Embedding []float64 `json:"embedding"`
		} `json:"data"`
	}
	if json.Unmarshal(body, &out) != nil || len(out.Data) != 1 || len(out.Data[0].Embedding) < 1 || len(out.Data[0].Embedding) > 4096 {
		return nil, errors.New("embedding response shape invalid")
	}
	vector := out.Data[0].Embedding
	norm := 0.0
	for _, v := range vector {
		if math.IsNaN(v) || math.IsInf(v, 0) || math.Abs(v) > 1e6 {
			return nil, errors.New("embedding contains invalid values")
		}
		norm += v * v
	}
	if norm == 0 {
		return nil, errors.New("embedding has zero norm")
	}
	return vector, nil
}
