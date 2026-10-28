package ninjaone

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

const webSecret = "w3b-client-secret"

// userTenant adds a Web app's refresh_token grant to tenant. Each refresh
// rotates the refresh token (rt1 -> rt2 -> ...) and issues user<n>.
type userTenant struct {
	tenant
	refreshes atomic.Int32
	reject    bool   // answer refreshes with 400 invalid_grant
	rotate    bool   // include a new refresh_token in responses
	current   string // the refresh token the tenant accepts
}

func (f *userTenant) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/ws/oauth/token" {
		r.ParseForm()
		if g := r.PostForm.Get("grant_type"); g == "refresh_token" {
			if f.reject || r.PostForm.Get("client_secret") != webSecret || r.PostForm.Get("refresh_token") != f.current {
				w.WriteHeader(http.StatusBadRequest)
				io.WriteString(w, `{"error":"invalid_grant"}`)
				return
			}
			n := strconv.Itoa(int(f.refreshes.Add(1)))
			resp := map[string]any{"access_token": "user" + n, "expires_in": 3600, "token_type": "Bearer"}
			if f.rotate {
				f.current = "rt" + n
				resp["refresh_token"] = f.current
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(resp)
			return
		}
	}
	f.tenant.ServeHTTP(w, r)
}

// newUserTest is a client with user context on, whose token file holds rt0.
func newUserTest(t *testing.T, f *userTenant) (*Client, string) {
	t.Helper()
	f.current = "rt0"
	file := filepath.Join(t.TempDir(), "user-token.json")
	if err := os.WriteFile(file, []byte(`{"refresh_token":"rt0"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	u, _ := url.Parse(srv.URL)
	c := New(u, "id", secret, "", srv.Client(), nil)
	c.EnableUserContext("web-id", webSecret, file)
	return c, file
}

func storedRefresh(t *testing.T, file string) string {
	t.Helper()
	b, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	var v struct {
		RefreshToken string `json:"refresh_token"`
	}
	json.Unmarshal(b, &v)
	return v.RefreshToken
}

func TestDoUserRotatesAndPersistsBeforeSending(t *testing.T) {
	var onDisk, bearer string
	f := &userTenant{rotate: true}
	c, file := newUserTest(t, f)
	f.api = func(w http.ResponseWriter, r *http.Request) {
		onDisk, bearer = storedRefresh(t, file), r.Header.Get("Authorization")
		io.WriteString(w, `{}`)
	}

	if _, err := c.DoUser(context.Background(), http.MethodPost, "/v2/device/1/script/run", nil, map[string]any{}); err != nil {
		t.Fatal(err)
	}
	if bearer != "Bearer user1" {
		t.Errorf("bearer = %q, want the user token", bearer)
	}
	if onDisk != "rt1" {
		t.Errorf("refresh token on disk when the request arrived = %q, want the rotated rt1", onDisk)
	}
	if fi, _ := os.Stat(file); fi.Mode().Perm() != 0o600 {
		t.Errorf("token file mode = %v", fi.Mode().Perm())
	}
}

func TestDoUserConcurrentCallsRefreshOnceAndKeepUnrotatedToken(t *testing.T) {
	f := &userTenant{} // NinjaOne answers without a new refresh_token
	c, file := newUserTest(t, f)
	f.api = func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, `[]`) }

	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			if _, err := c.DoUser(context.Background(), http.MethodGet, "/v2/x", nil, nil); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	if n := f.refreshes.Load(); n != 1 {
		t.Errorf("refreshes = %d, want 1", n)
	}
	if rt := storedRefresh(t, file); rt != "rt0" {
		t.Errorf("stored refresh token = %q, want rt0 kept", rt)
	}
}

func TestRejectedRefreshAsksToReauthorizeAndKeepsFile(t *testing.T) {
	var sent atomic.Int32
	f := &userTenant{reject: true}
	c, file := newUserTest(t, f)
	f.api = func(w http.ResponseWriter, r *http.Request) { sent.Add(1) }

	_, err := c.DoUser(context.Background(), http.MethodPost, "/v2/device/1/script/run", nil, map[string]any{})
	if err == nil || !strings.Contains(err.Error(), "action authorize") {
		t.Errorf("err = %v, want the re-authorize hint", err)
	}
	if sent.Load() != 0 {
		t.Error("the write was sent without a user token")
	}
	if storedRefresh(t, file) != "rt0" {
		t.Error("token file changed after a rejected refresh")
	}
	if s := c.UserState(); !s.Configured || s.Authorized {
		t.Errorf("UserState = %+v, want configured, not authorized", s)
	}
}

func TestNoTokenFileIsNotAuthorized(t *testing.T) {
	var sent atomic.Int32
	f := &userTenant{}
	c, file := newUserTest(t, f)
	f.api = func(w http.ResponseWriter, r *http.Request) { sent.Add(1) }
	os.Remove(file)

	_, err := c.DoUser(context.Background(), http.MethodPost, "/v2/x", nil, map[string]any{})
	if !errors.Is(err, ErrNotAuthorized) || sent.Load() != 0 || f.refreshes.Load() != 0 {
		t.Errorf("err = %v, sent = %d, refreshes = %d", err, sent.Load(), f.refreshes.Load())
	}
	if s := c.UserState(); s.Authorized {
		t.Errorf("UserState = %+v", s)
	}
	if _, err := newTest(t, &tenant{}).DoUser(context.Background(), http.MethodGet, "/v2/x", nil, nil); err == nil {
		t.Error("DoUser without user context succeeded")
	}
}

func TestUserSecretsNeverInErrors(t *testing.T) {
	f := &userTenant{rotate: true}
	c, _ := newUserTest(t, f)
	f.api = func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		io.WriteString(w, `{"resultCode":"BAD","errorMessage":"got `+webSecret+` and `+strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")+`"}`)
	}
	_, err := c.DoUser(context.Background(), http.MethodPost, "/v2/x", nil, map[string]any{})
	if err == nil || strings.Contains(err.Error(), webSecret) || strings.Contains(err.Error(), "user1") {
		t.Errorf("leaked: %v", err)
	}
}
