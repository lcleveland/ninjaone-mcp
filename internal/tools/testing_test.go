package tools

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/lcleveland/ninjaone-mcp/internal/config"
	"github.com/lcleveland/ninjaone-mcp/internal/ninjaone"
)

// session starts a fake NinjaOne (token endpoint + h for /v2/) and an
// in-memory MCP client/server pair.
type sessionT = *mcp.ClientSession

func session(t *testing.T, cfg *config.Config, h http.HandlerFunc) *mcp.ClientSession {
	t.Helper()
	return sessionLog(t, cfg, nil, h)
}

func sessionLog(t *testing.T, cfg *config.Config, log *slog.Logger, h http.HandlerFunc) *mcp.ClientSession {
	t.Helper()
	if h == nil {
		h = func(w http.ResponseWriter, _ *http.Request) { io.WriteString(w, "[]") }
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/ws/oauth/token" {
			w.Header().Set("Content-Type", "application/json")
			io.WriteString(w, `{"access_token":"tok","expires_in":3600,"scope":"monitoring management control","token_type":"Bearer"}`)
			return
		}
		h(w, r)
	}))
	t.Cleanup(srv.Close)
	u, _ := url.Parse(srv.URL)
	if cfg == nil {
		cfg = &config.Config{}
	}
	if cfg.MaxBulk == 0 {
		cfg.MaxBulk = 50
	}
	if cfg.Allow == nil {
		cfg.Allow = map[string]bool{}
	}
	cfg.BaseURL = u
	c := ninjaone.New(u, "id", "secret", "", srv.Client(), nil)
	c.Attempts, c.BaseDelay = 1, time.Millisecond

	s := mcp.NewServer(&mcp.Implementation{Name: "test"}, nil)
	Register(s, Deps{Client: c, Config: cfg, Log: log})
	st, ct := mcp.NewInMemoryTransports()
	ctx := context.Background()
	if _, err := s.Connect(ctx, st, nil); err != nil {
		t.Fatal(err)
	}
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "client"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cs.Close() })
	return cs
}

// call invokes a tool and decodes its structured result, also returning
// IsError and the first text content.
func call(t *testing.T, cs *mcp.ClientSession, name string, args map[string]any) (map[string]any, bool, string) {
	t.Helper()
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	text := ""
	if len(res.Content) > 0 {
		if tc, ok := res.Content[0].(*mcp.TextContent); ok {
			text = tc.Text
		}
	}
	var out map[string]any
	if res.StructuredContent != nil {
		b, _ := json.Marshal(res.StructuredContent)
		json.Unmarshal(b, &out)
	}
	return out, res.IsError, text
}

func toolNames(t *testing.T, cs *mcp.ClientSession) map[string]*mcp.Tool {
	t.Helper()
	res, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	m := map[string]*mcp.Tool{}
	for _, tl := range res.Tools {
		m[tl.Name] = tl
	}
	return m
}

func jsonOK(w http.ResponseWriter, body string) {
	w.Header().Set("Content-Type", "application/json")
	io.WriteString(w, body)
}
