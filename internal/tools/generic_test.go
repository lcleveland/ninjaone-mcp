package tools

import (
	"bufio"
	"net/http"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/lcleveland/ninjaone-mcp/internal/config"
)

// Every write in NinjaOne's spec is either routed to a capability (or a
// POST read) or explicitly never exposed. A new spec route lands here first.
func TestEverySpecWriteIsClassified(t *testing.T) {
	f, err := os.Open("testdata/spec-writes.txt")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	rs := routes()
	sample := strings.NewReplacer("{mode}", "NORMAL")
	approval := strings.NewReplacer("{mode}", "APPROVE")
	s := bufio.NewScanner(f)
	n := 0
	for s.Scan() {
		line := s.Text()
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		n++
		method, tmpl, _ := strings.Cut(line, " ")
		r := sample
		if strings.Contains(tmpl, "approval") {
			r = approval
		}
		if _, ok := classify(rs, method, r.Replace(tmpl)); !ok {
			t.Errorf("unclassified: %s", line)
		}
	}
	if n != 164 {
		t.Errorf("read %d spec writes, want 164", n)
	}
}

func TestGenericNeverExposedEvenWithEverything(t *testing.T) {
	var hits []string
	cs := session(t, allow(config.Capabilities...), func(w http.ResponseWriter, r *http.Request) {
		hits = append(hits, r.Method+" "+r.URL.Path)
		w.WriteHeader(http.StatusNoContent)
	})
	for _, c := range [][2]string{
		{"PUT", "/v2/webhook"}, {"POST", "/v2/organization/generate-installer"}, {"POST", "/v2/user/technicians"},
		{"PATCH", "/v2/user/role/3/add-members"}, {"PUT", "/v2/organization/4/policies"}, {"POST", "/v2/billing/invoices/approve"},
		{"DELETE", "/v2/custom-fields/bulk"}, {"PATCH", "/v2/system/custom-fields"}, {"POST", "/v2/contacts"},
	} {
		_, isErr, text := call(t, cs, "ninjaone_api", map[string]any{"method": c[0], "path": c[1], "reason": "x", "body": map[string]any{}})
		if !isErr || !strings.Contains(text, "never exposes") {
			t.Errorf("%s %s: %v %s", c[0], c[1], isErr, text)
		}
	}
	_, isErr, text := call(t, cs, "ninjaone_api", map[string]any{"method": "POST", "path": "/v2/device/1/reboot/SOFT", "reason": "x"})
	if !isErr || !strings.Contains(text, "not a known") {
		t.Errorf("unknown route: %s", text)
	}
	if len(hits) != 0 {
		t.Errorf("requests sent: %v", hits)
	}
}

func TestGenericGatesAndSharesWritePath(t *testing.T) {
	var hits []string
	h := func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/v2/device/5" {
			jsonOK(w, `{"id":5,"displayName":"FS01"}`)
			return
		}
		if r.URL.Query().Has("showSecureValues") {
			t.Error("showSecureValues sent")
		}
		hits = append(hits, r.Method+" "+r.URL.Path)
		if r.Method == http.MethodGet {
			jsonOK(w, `[{"id":1}]`)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
	off := session(t, nil, h)
	out, isErr, _ := call(t, off, "ninjaone_api", map[string]any{"method": "GET", "path": "/v2/device/5/volumes", "query": map[string]any{"showSecureValues": true}})
	if isErr || len(out["results"].([]any)) != 1 {
		t.Errorf("GET: %v", out)
	}
	if _, isErr, _ := call(t, off, "ninjaone_api", map[string]any{"method": "POST", "path": "/v2/ticketing/trigger/board/1/run", "body": map[string]any{}}); isErr {
		t.Error("board run refused as a write")
	}
	_, isErr, text := call(t, off, "ninjaone_api", map[string]any{"method": "POST", "path": "/v2/tag", "reason": "x", "body": map[string]any{}})
	if !isErr || !strings.Contains(text, "documentation capability") {
		t.Errorf("disabled capability: %s", text)
	}
	if m := toolNames(t, off)["ninjaone_api"]; !m.Annotations.ReadOnlyHint {
		t.Error("generic tool not read-only with no capabilities")
	}

	on := session(t, allow("documentation", "device-actions"), h)
	if _, isErr, text := call(t, on, "ninjaone_api", map[string]any{"method": "POST", "path": "/v2/tag", "body": map[string]any{}}); !isErr || !strings.Contains(text, "reason") {
		t.Errorf("no reason: %s", text)
	}
	if _, isErr, text := call(t, on, "ninjaone_api", map[string]any{"method": "POST", "path": "/v2/tag", "reason": "tagging", "body": map[string]any{"name": "x"}}); isErr {
		t.Errorf("allowed write: %s", text)
	}
	_, isErr, text = call(t, on, "ninjaone_api", map[string]any{"method": "POST", "path": "/v2/device/5/reboot/FORCED", "reason": "x", "confirm": "nope"})
	if !isErr || !strings.Contains(text, "FS01") {
		t.Errorf("forced reboot without confirm: %s", text)
	}
	if slices.Contains(hits, "POST /v2/device/5/reboot/FORCED") {
		t.Error("forced reboot sent without confirm")
	}
}

func TestAPIPath(t *testing.T) {
	for _, p := range []string{"/api/x", "/ws/oauth/token", "/v2/../ws/oauth/token", "/v2/a%2Fb", "/v2/x?y=1", "https://evil/v2/x", "/v2//x", "/v2/x#f", `/v2\x`} {
		if _, err := apiPath(p); err == nil {
			t.Errorf("%q accepted", p)
		}
	}
	if p, err := apiPath("/v2/devices/"); err != nil || p != "/v2/devices" {
		t.Errorf("good path: %q %v", p, err)
	}
}
