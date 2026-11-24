package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

const (
	defaultBaseURL   = "https://ai.pricememo.cn"
	maxResponseBytes = 8 << 20
)

var pathSegment = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

type priceMemoClient struct {
	baseURL *url.URL
	http    *http.Client
}

func newPriceMemoClient(baseURL string) (*priceMemoClient, error) {
	base, err := url.Parse(baseURL)
	if err != nil || base.Hostname() == "" || (base.Scheme != "https" && base.Scheme != "http") ||
		base.User != nil || base.RawQuery != "" || base.ForceQuery || base.Fragment != "" {
		return nil, fmt.Errorf("PRICEMEMO_BASE_URL must be an HTTP(S) base URL without credentials, query or fragment")
	}
	return &priceMemoClient{
		baseURL: base,
		http: &http.Client{
			Timeout: 15 * time.Second,
			CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
	}, nil
}

func (c *priceMemoClient) get(ctx context.Context, query url.Values, segments ...string) (json.RawMessage, error) {
	for _, segment := range segments {
		if !pathSegment.MatchString(segment) {
			return nil, fmt.Errorf("invalid identifier in PriceMemo path")
		}
	}
	endpoint := c.baseURL.JoinPath("/api/v1", strings.Join(segments, "/"))
	endpoint.RawQuery = query.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("create PriceMemo request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "ai-price-memory-mcp/0.2.0")

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request PriceMemo: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		if resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusUnprocessableEntity {
			var problem struct {
				Detail json.RawMessage `json:"detail"`
			}
			if json.NewDecoder(io.LimitReader(resp.Body, 4096)).Decode(&problem) == nil && len(problem.Detail) > 0 {
				return nil, fmt.Errorf("PriceMemo HTTP %d: %s", resp.StatusCode, problem.Detail)
			}
		}
		return nil, fmt.Errorf("PriceMemo HTTP %d %s", resp.StatusCode, http.StatusText(resp.StatusCode))
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read PriceMemo response: %w", err)
	}
	if len(body) > maxResponseBytes {
		return nil, fmt.Errorf("PriceMemo response exceeded %d bytes", maxResponseBytes)
	}
	body = bytes.TrimSpace(body)
	if len(body) == 0 || body[0] != '{' || !json.Valid(body) {
		return nil, fmt.Errorf("PriceMemo response is not a JSON object")
	}
	return json.RawMessage(body), nil
}

func getJSON[T any](ctx context.Context, client *priceMemoClient, query url.Values, segments ...string) (T, error) {
	var result T
	body, err := client.get(ctx, query, segments...)
	if err != nil {
		return result, err
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return result, fmt.Errorf("decode PriceMemo response: %w", err)
	}
	return result, nil
}
