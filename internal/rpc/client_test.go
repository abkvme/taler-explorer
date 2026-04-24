package rpc

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestCallSuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req rpcRequest
		body, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(body, &req); err != nil {
			t.Fatalf("unmarshal req: %v", err)
		}
		if req.Method != "getblockcount" {
			t.Fatalf("unexpected method %q", req.Method)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"result":42,"error":null,"id":1}`)
	}))
	defer srv.Close()

	c := New(srv.URL, "u", "p", 2*time.Second)
	h, err := c.GetBlockCount(context.Background())
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	if h != 42 {
		t.Fatalf("got %d want 42", h)
	}
}

func TestCallRPCError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"result":null,"error":{"code":-8,"message":"Block height out of range"},"id":1}`)
	}))
	defer srv.Close()

	c := New(srv.URL, "", "", time.Second)
	_, err := c.GetBlockHash(context.Background(), 10_000_000)
	if err == nil || !strings.Contains(err.Error(), "Block height out of range") {
		t.Fatalf("want rpc error, got %v", err)
	}
}

func TestAuthHeader(t *testing.T) {
	called := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		user, pass, ok := r.BasicAuth()
		if !ok || user != "alice" || pass != "secret" {
			t.Fatalf("bad auth: ok=%v user=%q pass=%q", ok, user, pass)
		}
		_, _ = io.WriteString(w, `{"result":1,"error":null,"id":1}`)
	}))
	defer srv.Close()

	c := New(srv.URL, "alice", "secret", time.Second)
	if _, err := c.GetBlockCount(context.Background()); err != nil {
		t.Fatalf("call: %v", err)
	}
	if !called {
		t.Fatal("server not hit")
	}
}
