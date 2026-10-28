package server

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
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
	ts := httptest.NewServer(Handler(cfg, newMCP(cfg, ninjaone.New(u, "id", "secret", "", nil, nil)), ninjaone.New(u, "id", "secret", "", nil, nil), nil))
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

func newMCP(cfg *config.Config, c *ninjaone.Client) *mcp.Server {
	s, _ := New(cfg, c, nil)
	return s
}

func TestOAuthCallback(t *testing.T) {
	var exchanges int
	tenant := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		if r.URL.Path != "/ws/oauth/token" || r.PostForm.Get("grant_type") != "authorization_code" || r.PostForm.Get("code") != "the-code" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		exchanges++
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"access_token":"the-access-token","refresh_token":"the-refresh-token","expires_in":3600}`)
	}))
	t.Cleanup(tenant.Close)
	u, _ := url.Parse(tenant.URL)
	file := filepath.Join(t.TempDir(), "user-token.json")
	cfg := &config.Config{BaseURL: u, Path: "/mcp", MaxBulk: 50, Allow: map[string]bool{}, HTTP: true, HTTPAuthToken: "right-token",
		WebClientID: "web-id", WebClientSecret: "web-secret", UserTokenFile: file, OAuthRedirectURL: "http://127.0.0.1:8233/oauth/callback"}
	c := ninjaone.New(u, "id", "secret", "", tenant.Client(), nil)
	c.EnableUserContext(cfg.WebClientID, cfg.WebClientSecret, file)
	var logs strings.Builder
	ts := httptest.NewServer(Handler(cfg, newMCP(cfg, c), c, slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))))
	t.Cleanup(ts.Close)

	get := func(q string) (int, string) {
		resp, err := http.Get(ts.URL + "/oauth/callback?" + q)
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return resp.StatusCode, string(b)
	}
	raw, _ := c.AuthorizeURL(cfg.OAuthRedirectURL)
	au, _ := url.Parse(raw)
	state := au.Query().Get("state")

	// No bearer needed (a browser cannot send one), but a bad state is refused.
	if code, _ := get("code=the-code&state=wrong"); code != http.StatusBadRequest || exchanges != 0 {
		t.Errorf("wrong state: %d, exchanges %d", code, exchanges)
	}
	if code, body := get("error=injected-text&state=wrong"); code != http.StatusBadRequest || strings.Contains(body, "injected-text") {
		t.Errorf("error with a wrong state was echoed: %d %s", code, body)
	}
	if code, body := get("error=access_denied&state=" + state); code != http.StatusBadRequest || !strings.Contains(body, "access_denied") {
		t.Errorf("denied: %d %s", code, body)
	}
	raw, _ = c.AuthorizeURL(cfg.OAuthRedirectURL) // the user tries again with a fresh link
	au, _ = url.Parse(raw)
	state = au.Query().Get("state")
	code, body := get("code=the-code&state=" + state)
	if code != http.StatusOK || exchanges != 1 || !c.UserState().Authorized {
		t.Errorf("valid callback: %d %s, exchanges %d", code, body, exchanges)
	}
	for _, secret := range []string{"the-code", state, "the-access-token", "the-refresh-token", "web-secret"} {
		if strings.Contains(body, secret) || strings.Contains(logs.String(), secret) {
			t.Errorf("%q leaked into the page or the log:\n%s\n%s", secret, body, logs.String())
		}
	}
	// /mcp still needs the bearer.
	if cs, err := connect(ts, ""); err == nil {
		cs.Close()
		t.Error("/mcp accepted no bearer")
	}
}
