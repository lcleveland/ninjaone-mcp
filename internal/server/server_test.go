package server

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/lcleveland/ninjaone-mcp/internal/config"
	"github.com/lcleveland/ninjaone-mcp/internal/ninjaone"
)

func newServer(t *testing.T, cfg *config.Config) *httptest.Server {
	t.Helper()
	u, _ := url.Parse("https://us2.ninjarmm.com")
	cfg.BaseURL, cfg.Path, cfg.MaxBulk = u, "/mcp", 50
	if cfg.Allow == nil {
		cfg.Allow = map[string]bool{}
	}
	s, _ := New(cfg, ninjaone.New(u, "id", "secret", "", nil, nil), nil)
	ts := httptest.NewServer(Handler(cfg, s, nil))
	t.Cleanup(ts.Close)
	return ts
}

type bearer struct {
	tok  string
	next http.RoundTripper
}

func (b bearer) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("Authorization", "Bearer "+b.tok)
	return b.next.RoundTrip(r)
}

func connect(ts *httptest.Server, tok string) (*mcp.ClientSession, error) {
	hc := ts.Client()
	if tok != "" {
		hc = &http.Client{Transport: bearer{tok, http.DefaultTransport}}
	}
	return mcp.NewClient(&mcp.Implementation{Name: "t"}, nil).Connect(context.Background(),
		&mcp.StreamableClientTransport{Endpoint: ts.URL + "/mcp", HTTPClient: hc}, nil)
}

func TestHTTPInitializeAndTools(t *testing.T) {
	ts := newServer(t, &config.Config{})
	cs, err := connect(ts, "")
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()
	res, err := cs.ListTools(context.Background(), nil)
	if err != nil || len(res.Tools) < 10 {
		t.Fatalf("tools: %v %v", len(res.Tools), err)
	}
	if !strings.Contains(cs.InitializeResult().Instructions, "read-only") {
		t.Errorf("instructions: %s", cs.InitializeResult().Instructions)
	}
	resp, _ := http.Get(ts.URL + "/healthz")
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 || !strings.Contains(string(b), `"ok"`) {
		t.Errorf("healthz: %d %s", resp.StatusCode, b)
	}
}

func TestHTTPBearer(t *testing.T) {
	ts := newServer(t, &config.Config{HTTPAuthToken: "right-token"})
	for _, tok := range []string{"", "wrong-token"} {
		if cs, err := connect(ts, tok); err == nil {
			cs.Close()
			t.Errorf("token %q accepted", tok)
		}
	}
	cs, err := connect(ts, "right-token")
	if err != nil {
		t.Fatal(err)
	}
	cs.Close()
	// healthz stays open for liveness probes.
	if resp, _ := http.Get(ts.URL + "/healthz"); resp.StatusCode != 200 {
		t.Errorf("healthz behind auth: %d", resp.StatusCode)
	}
}

func TestInstructionsNameCapabilities(t *testing.T) {
	u, _ := url.Parse("https://us2.ninjarmm.com")
	s := instructions(&config.Config{BaseURL: u, Allow: map[string]bool{"scripts": true, "tickets": true}})
	if !strings.Contains(s, "tickets, scripts") || !strings.Contains(s, "ninjaone_activity") {
		t.Errorf("instructions: %s", s)
	}
}
