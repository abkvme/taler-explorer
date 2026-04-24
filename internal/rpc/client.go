package rpc

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync/atomic"
	"time"
)

type Client struct {
	url     string
	user    string
	pass    string
	http    *http.Client
	counter atomic.Uint64
}

func New(url, user, pass string, timeout time.Duration) *Client {
	return &Client{
		url:  url,
		user: user,
		pass: pass,
		http: &http.Client{Timeout: timeout},
	}
}

type rpcRequest struct {
	Jsonrpc string `json:"jsonrpc"`
	ID      uint64 `json:"id"`
	Method  string `json:"method"`
	Params  any    `json:"params"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *rpcError) Error() string { return fmt.Sprintf("rpc error %d: %s", e.Code, e.Message) }

type rpcResponse struct {
	Result json.RawMessage `json:"result"`
	Error  *rpcError       `json:"error"`
	ID     uint64          `json:"id"`
}

// Call invokes method with params and decodes the result into out (may be nil).
func (c *Client) Call(ctx context.Context, method string, params any, out any) error {
	if params == nil {
		params = []any{}
	}
	body, err := json.Marshal(rpcRequest{
		Jsonrpc: "1.0",
		ID:      c.counter.Add(1),
		Method:  method,
		Params:  params,
	})
	if err != nil {
		return fmt.Errorf("marshal %s: %w", method, err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.user != "" || c.pass != "" {
		req.SetBasicAuth(c.user, c.pass)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("%s: %w", method, err)
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("%s: read body: %w", method, err)
	}

	// Even on 500 the daemon usually returns a valid JSON-RPC envelope; try to
	// parse it before treating the status code as fatal.
	var rr rpcResponse
	if err := json.Unmarshal(data, &rr); err != nil {
		return fmt.Errorf("%s: decode response (http %d): %w", method, resp.StatusCode, err)
	}
	if rr.Error != nil {
		return rr.Error
	}
	if resp.StatusCode >= 400 {
		return fmt.Errorf("%s: http %d", method, resp.StatusCode)
	}
	if out != nil && len(rr.Result) > 0 && string(rr.Result) != "null" {
		if err := json.Unmarshal(rr.Result, out); err != nil {
			return fmt.Errorf("%s: decode result: %w", method, err)
		}
	}
	return nil
}
