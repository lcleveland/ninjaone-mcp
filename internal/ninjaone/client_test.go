package ninjaone

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const secret = "s3cr3t-client-secret"

// tenant is a fake NinjaOne: a token endpoint plus whatever api handles.
type tenant struct {
	tokens   atomic.Int32
	expires  int
	tokenErr int // status to fail the token endpoint with
	api      http.HandlerFunc
}

func (f *tenant) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/ws/oauth/token" {
		r.ParseForm()
		if f.tokenErr != 0 {
			w.WriteHeader(f.tokenErr)
			io.WriteString(w, `{"resultCode":"Client app not exist","incidentId":"WEB-1"}`)
			return
		}
		if r.PostForm.Get("client_secret") != secret || r.PostForm.Get("grant_type") != "client_credentials" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		n := f.tokens.Add(1)
		exp := f.expires
		if exp == 0 {
			exp = 3600
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"access_token":"tok`+strconv.Itoa(int(n))+`","expires_in":`+strconv.Itoa(exp)+`,"scope":"monitoring management","token_type":"Bearer"}`)
		return
	}
	f.api(w, r)
}

func newTest(t *testing.T, f *tenant) *Client {
	t.Helper()
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	u, _ := url.Parse(srv.URL)
	c := New(u, "id", secret, "", srv.Client(), nil)
	c.BaseDelay, c.MaxDelay = time.Millisecond, time.Millisecond
	return c
}

func TestTokenCachedAndRefreshedEarly(t *testing.T) {
	f := &tenant{api: func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer tok") {
			t.Errorf("auth header %q", r.Header.Get("Authorization"))
		}
		io.WriteString(w, `[]`)
	}}
	c := newTest(t, f)
	ctx := context.Background()
	for range 3 {
		if _, err := c.Do(ctx, http.MethodGet, "/v2/devices", nil, nil); err != nil {
			t.Fatal(err)
		}
	}
	if n := f.tokens.Load(); n != 1 {
		t.Errorf("tokens fetched = %d, want 1 (cached)", n)
	}
	tok, err := c.Authenticate(ctx)
	if err != nil || tok.Scope != "monitoring management" {
		t.Errorf("Authenticate = %+v, %v", tok, err)
	}

	f.expires = 30 // inside the one-minute refresh margin
	c.token = ""
	c.Do(ctx, http.MethodGet, "/v2/devices", nil, nil)
	c.Do(ctx, http.MethodGet, "/v2/devices", nil, nil)
	if n := f.tokens.Load(); n != 3 {
		t.Errorf("tokens fetched = %d, want 3 (refreshed each time near expiry)", n)
	}
}

func TestReauthOnce401(t *testing.T) {
	var calls atomic.Int32
	f := &tenant{api: func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			w.WriteHeader(http.StatusUnauthorized)
			io.WriteString(w, `{"error":"not_authenticated","error_description":"Invalid credentials","error_code":10}`)
			return
		}
		io.WriteString(w, `[]`)
	}}
	c := newTest(t, f)
	if _, err := c.Do(context.Background(), http.MethodPost, "/v2/x", nil, map[string]any{}); err != nil {
		t.Fatal(err)
	}
	if f.tokens.Load() != 2 {
		t.Errorf("tokens = %d, want 2", f.tokens.Load())
	}

	// A 401 that persists is reported, not looped on.
	f.api = func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusUnauthorized) }
	_, err := c.Do(context.Background(), http.MethodGet, "/v2/x", nil, nil)
	var ae *APIError
	if !errors.As(err, &ae) || ae.Status != 401 {
		t.Errorf("err = %v", err)
	}
}

func TestRetryGETOnlyIncludingHTMLThrottle(t *testing.T) {
	var calls atomic.Int32
	f := &tenant{api: func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) < 3 {
			// NinjaOne's throttle page: HTML with a 200.
			w.Header().Set("Content-Type", "text/html")
			io.WriteString(w, "<html>Too many requests</html>")
			return
		}
		io.WriteString(w, `[{"id":1}]`)
	}}
	c := newTest(t, f)
	resp, err := c.Do(context.Background(), http.MethodGet, "/v2/devices", nil, nil)
	if err != nil || string(resp.Body) != `[{"id":1}]` {
		t.Fatalf("got %v, %v", resp, err)
	}
	if calls.Load() != 3 {
		t.Errorf("calls = %d, want 3", calls.Load())
	}

	calls.Store(0)
	f.api = func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}
	_, err = c.Do(context.Background(), http.MethodPost, "/v2/device/1/reboot/NORMAL", nil, map[string]any{"reason": "x"})
	if err == nil || calls.Load() != 1 {
		t.Errorf("POST: calls = %d err = %v; a mutation must never be retried", calls.Load(), err)
	}

	calls.Store(0)
	f.api = func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "text/html")
		io.WriteString(w, "<html>rate limit</html>")
	}
	_, err = c.Do(context.Background(), http.MethodPost, "/v2/x", nil, map[string]any{})
	var ae *APIError
	if !errors.As(err, &ae) || !ae.Throttled || calls.Load() != 1 || !strings.Contains(ae.Hint, "never retried") {
		t.Errorf("throttled POST: calls=%d err=%v", calls.Load(), err)
	}
}

func TestErrorShapesAndHints(t *testing.T) {
	cases := []struct {
		status int
		body   string
		want   []string
	}{
		{400, `{"error":"invalid_header","error_description":"Invalid 'Authorization' header","error_code":1}`, []string{"invalid_header", "server bug"}},
		{404, `{"resultCode":"NOT_FOUND","incidentId":"WEB-42"}`, []string{"NOT_FOUND", "incidentId WEB-42", "this NinjaOne instance"}},
		{500, `{"resultCode":"permission_denied"}`, []string{"permission_denied", "lacks permission"}},
		{403, `{"resultCode":"user_context_required"}`, []string{"signed-in NinjaOne user"}},
		{400, `{"errorCode":"BAD","message":"invoice locked"}`, []string{"BAD", "invoice locked"}},
		{409, `{"resultCode":"FAILURE","errorMessage":"version mismatch","errorDetails":{"field":"version"}}`, []string{"version mismatch", `"field":"version"`, "stale"}},
	}
	for _, tc := range cases {
		f := &tenant{api: func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("x-nj-request-id", "req-9")
			w.WriteHeader(tc.status)
			io.WriteString(w, tc.body)
		}}
		_, err := newTest(t, f).Do(context.Background(), http.MethodPatch, "/v2/x", nil, map[string]any{})
		var ae *APIError
		if !errors.As(err, &ae) || ae.RequestID != "req-9" {
			t.Fatalf("%d: %v", tc.status, err)
		}
		for _, w := range tc.want {
			if !strings.Contains(err.Error(), w) {
				t.Errorf("%d %s: %q missing %q", tc.status, tc.body, err, w)
			}
		}
	}
}

func TestWrongRegionHint(t *testing.T) {
	c := newTest(t, &tenant{tokenErr: 404})
	_, err := c.Do(context.Background(), http.MethodGet, "/v2/devices", nil, nil)
	if err == nil || !strings.Contains(err.Error(), "wrong region") {
		t.Errorf("err = %v", err)
	}
}

func TestSecretNeverInErrors(t *testing.T) {
	f := &tenant{api: func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		// A server echoing credentials back must not leak them to the model.
		io.WriteString(w, `{"resultCode":"BAD","errorMessage":"got `+secret+` and `+strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")+`"}`)
	}}
	_, err := newTest(t, f).Do(context.Background(), http.MethodGet, "/v2/x", nil, nil)
	if err == nil || strings.Contains(err.Error(), secret) || strings.Contains(err.Error(), "tok1") {
		t.Errorf("leaked: %v", err)
	}
}

func TestQueryEncodesSpacesAsPercent20(t *testing.T) {
	var raw string
	f := &tenant{api: func(w http.ResponseWriter, r *http.Request) {
		raw = r.URL.RawQuery
		io.WriteString(w, `[]`)
	}}
	c := newTest(t, f)
	c.Do(context.Background(), http.MethodGet, "/v2/devices", url.Values{"df": {"org = 1 AND class in (MAC,WINDOWS_SERVER)"}, "q": {"a+b"}}, nil)
	if strings.Contains(raw, "+") || !strings.Contains(raw, "org%20%3D%201%20AND") || !strings.Contains(raw, "a%2Bb") {
		t.Errorf("raw query = %s", raw)
	}
}
