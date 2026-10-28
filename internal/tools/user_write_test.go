package tools

import (
	"net/http"
	"strings"
	"testing"

	"github.com/lcleveland/ninjaone-mcp/internal/config"
)

// bearers records "METHOD /path Bearer <token>" for every API request.
func bearers(seen *[]string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		*seen = append(*seen, r.Method+" "+r.URL.Path+" "+r.Header.Get("Authorization"))
		switch {
		case r.URL.Path == "/v2/device/5":
			jsonOK(w, `{"id":5,"displayName":"FS01"}`)
		case r.Method == http.MethodGet:
			jsonOK(w, `[]`)
		default:
			w.WriteHeader(http.StatusNoContent)
		}
	}
}

func userAllow(t *testing.T, authorized bool, caps ...string) *config.Config {
	cfg := userConfig(t, authorized)
	for _, c := range caps {
		if cfg.Allow == nil {
			cfg.Allow = map[string]bool{}
		}
		cfg.Allow[c] = true
	}
	return cfg
}

func TestUserContextWritesUseTheUserToken(t *testing.T) {
	var seen []string
	cs := session(t, userAllow(t, true, "scripts", "tickets", "device-actions"), bearers(&seen))

	run := map[string]any{"action": "run", "id": 5, "reason": "x", "confirm": "FS01", "body": map[string]any{"type": "SCRIPT", "id": 171}}
	if _, isErr, text := call(t, cs, "ninjaone_script", run); isErr {
		t.Fatal(text)
	}
	if _, isErr, text := call(t, cs, "ninjaone_ticket", map[string]any{"action": "create", "reason": "x", "body": map[string]any{"subject": "s"}}); isErr {
		t.Fatal(text)
	}
	if _, isErr, text := call(t, cs, "ninjaone_api", map[string]any{"method": "POST", "path": "/v2/device/5/script/run", "reason": "x", "confirm": "FS01", "body": map[string]any{"type": "SCRIPT", "id": 171}}); isErr {
		t.Fatal(text)
	}
	if _, isErr, text := call(t, cs, "ninjaone_device_action", map[string]any{"action": "reboot", "id": 5, "reason": "x"}); isErr {
		t.Fatal(text)
	}

	want := map[string]string{
		"POST /v2/device/5/script/run": "Bearer usertok",
		"POST /v2/ticketing/ticket":    "Bearer usertok",
		"GET /v2/device/5":             "Bearer tok", // the confirm lookup
		"POST /v2/device/5/reboot":     "Bearer tok", // not flagged
	}
	for prefix, bearer := range want {
		found := false
		for _, s := range seen {
			if strings.HasPrefix(s, prefix) {
				found = true
				if !strings.HasSuffix(s, " "+bearer) {
					t.Errorf("%s: want %s", s, bearer)
				}
			}
		}
		if !found {
			t.Errorf("no request for %s in %v", prefix, seen)
		}
	}
}

func TestUserContextNotAuthorizedSendsNothing(t *testing.T) {
	var seen []string
	cs := session(t, userAllow(t, false, "scripts"), bearers(&seen))
	_, isErr, text := call(t, cs, "ninjaone_script", map[string]any{"action": "run", "id": 5, "reason": "x", "confirm": "FS01", "body": map[string]any{"type": "SCRIPT", "id": 171}})
	if !isErr || !strings.Contains(text, "action authorize") {
		t.Errorf("err=%v %s", isErr, text)
	}
	for _, s := range seen {
		if strings.HasPrefix(s, "POST") {
			t.Errorf("sent %s without an authorized user", s)
		}
	}
}
