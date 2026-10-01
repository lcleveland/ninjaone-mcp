package tools

import (
	"net/http"
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
