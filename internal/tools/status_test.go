package tools

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/lcleveland/ninjaone-mcp/internal/config"
)

func TestStatus(t *testing.T) {
	var path string
	cs := session(t, &config.Config{Region: "us2"}, func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path + "?" + r.URL.RawQuery
		jsonOK(w, `[{"id":1}]`)
	})
	out, isErr, _ := call(t, cs, "ninjaone_status", nil)
	if isErr || out["authenticated"] != true || out["api_reachable"] != true || out["region"] != "us2" {
		t.Fatalf("status = %v", out)
	}
	if path != "/v2/organizations?pageSize=1" {
		t.Errorf("probe = %s", path)
	}
	if s, _ := out["scopes"].([]any); len(s) != 3 {
		t.Errorf("scopes = %v", out["scopes"])
	}

	cs = session(t, nil, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		jsonOK(w, `{"resultCode":"permission_denied"}`)
	})
	out, isErr, _ = call(t, cs, "ninjaone_status", nil)
	if isErr || out["authenticated"] != true || out["api_reachable"] != false || out["detail"] == "" {
		t.Errorf("forbidden status = %v", out)
	}
}

func TestStatusUserContextAndAuthorize(t *testing.T) {
	out, _, _ := call(t, session(t, nil, nil), "ninjaone_status", nil)
	if out["user_context"] != nil {
		t.Errorf("user_context reported while off: %v", out["user_context"])
	}
	if _, isErr, _ := call(t, session(t, nil, nil), "ninjaone_status", map[string]any{"action": "authorize"}); !isErr {
		t.Error("authorize without user context succeeded")
	}

	cfg := userConfig(t, false)
	cs := session(t, cfg, func(w http.ResponseWriter, r *http.Request) { jsonOK(w, `[]`) })
	out, _, _ = call(t, cs, "ninjaone_status", nil)
	if uc, _ := out["user_context"].(map[string]any); uc["configured"] != true || uc["authorized"] != false {
		t.Errorf("user_context = %v", out["user_context"])
	}
	out, isErr, text := call(t, cs, "ninjaone_status", map[string]any{"action": "authorize"})
	raw, _ := out["authorize_url"].(string)
	u, _ := url.Parse(raw)
	if isErr || u == nil || u.Query().Get("redirect_uri") != cfg.OAuthRedirectURL || u.Query().Get("client_id") != "web-id" || u.Query().Get("state") == "" {
		t.Errorf("authorize: err=%v %s out=%v", isErr, text, out)
	}

	cfg = userConfig(t, true)
	cfg.HTTP = false
	cs = session(t, cfg, nil)
	if out, _, _ := call(t, cs, "ninjaone_status", nil); out["user_context"].(map[string]any)["authorized"] != true {
		t.Errorf("stored token not reported as authorized: %v", out["user_context"])
	}
	if _, isErr, text := call(t, cs, "ninjaone_status", map[string]any{"action": "authorize"}); !isErr || !strings.Contains(text, "--http") {
		t.Errorf("stdio authorize: err=%v %s", isErr, text)
	}
}
