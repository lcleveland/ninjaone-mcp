package tools

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/lcleveland/ninjaone-mcp/internal/config"
)

func allow(caps ...string) *config.Config {
	c := &config.Config{Allow: map[string]bool{}}
	for _, n := range caps {
		c.Allow[n] = true
	}
	return c
}

func actionsOf(t *testing.T, cs sessionT, tool string) []string {
	t.Helper()
	tl := toolNames(t, cs)[tool]
	if tl == nil {
		t.Fatalf("%s not registered", tool)
	}
	b, _ := json.Marshal(tl.InputSchema)
	var s struct {
		Properties map[string]struct {
			Enum []string `json:"enum"`
		} `json:"properties"`
	}
	json.Unmarshal(b, &s)
	return s.Properties["action"].Enum
}

func TestCapabilityGatesSchema(t *testing.T) {
	off := session(t, nil, nil)
	if a := actionsOf(t, off, "ninjaone_ticket"); slices.Contains(a, "create") || slices.Contains(a, "comment") {
		t.Errorf("tickets off but schema has %v", a)
	}
	on := session(t, allow("tickets"), nil)
	a := actionsOf(t, on, "ninjaone_ticket")
	if !slices.Contains(a, "create") || !slices.Contains(a, "update") || !slices.Contains(a, "comment") {
		t.Errorf("tickets on but schema has %v", a)
	}
	if toolNames(t, on)["ninjaone_ticket"].Annotations.ReadOnlyHint {
		t.Error("tool with writes is still marked read-only")
	}
	// Only its own capability: custom field writes stay hidden.
	if a := actionsOf(t, on, "ninjaone_custom_field"); slices.Contains(a, "set_device") {
		t.Errorf("custom-fields leaked: %v", a)
	}
	// Called anyway, a hidden action is refused.
	_, isErr, text := call(t, off, "ninjaone_ticket", map[string]any{"action": "create", "body": map[string]any{}, "reason": "x"})
	if !isErr || !(strings.Contains(text, "not available") || strings.Contains(text, "enum")) {
		t.Errorf("hidden action: %v %s", isErr, text)
	}
}

func TestWriteHandlerChecksCapabilityItself(t *testing.T) {
	d := Deps{Config: allow(), Log: slog.New(slog.DiscardHandler)}
	v := View{Action: "x", Capability: "scripts", Method: "POST", Path: "/v2/x"}
	if _, err := d.write(context.Background(), "t", v, Input{Reason: "r"}); err == nil || !strings.Contains(err.Error(), "disabled") {
		t.Errorf("err = %v", err)
	}
}

func TestReasonBulkRequireAndAudit(t *testing.T) {
	var log strings.Builder
	var got []string
	cs := sessionLog(t, allow("tickets", "documentation", "custom-fields"), slog.New(slog.NewTextHandler(&log, nil)),
		func(w http.ResponseWriter, r *http.Request) {
			got = append(got, r.Method+" "+r.URL.Path)
			w.Header().Set("x-nj-request-id", "req-77")
			w.WriteHeader(http.StatusNoContent)
		})

	_, isErr, text := call(t, cs, "ninjaone_custom_field", map[string]any{"action": "set_device", "id": 4, "body": map[string]any{"owner": "ops"}})
	if !isErr || !strings.Contains(text, "reason is required") {
		t.Errorf("no reason: %s", text)
	}

	out, isErr, text := call(t, cs, "ninjaone_custom_field", map[string]any{"action": "set_device", "id": 4, "body": map[string]any{"owner": "ops"}, "reason": "ticket 123"})
	if isErr || out["request_id"] != "req-77" || got[len(got)-1] != "PATCH /v2/device/4/custom-fields" {
		t.Fatalf("set_device: %v %s %v", out, text, got)
	}
	if l := log.String(); !strings.Contains(l, "ninjaone write") || !strings.Contains(l, "request_id=req-77") || !strings.Contains(l, `reason="ticket 123"`) {
		t.Errorf("audit log: %s", l)
	}

	ids := make([]any, 51)
	for i := range ids {
		ids[i] = i
	}
	_, isErr, text = call(t, cs, "ninjaone_document", map[string]any{"action": "archive", "body": ids, "reason": "cleanup"})
	if !isErr || !strings.Contains(text, "exceeds the limit of 50") {
		t.Errorf("bulk cap: %s", text)
	}
	_, isErr, text = call(t, cs, "ninjaone_custom_field", map[string]any{"action": "set_device", "id": 4, "body": []any{map[string]any{}}, "reason": "x"})
	if !isErr || !strings.Contains(text, "not a list") {
		t.Errorf("list to single-record write: %s", text)
	}

	_, isErr, text = call(t, cs, "ninjaone_ticket", map[string]any{"action": "update", "id": 9, "body": map[string]any{"status": "OPEN"}, "reason": "x"})
	if !isErr || !strings.Contains(text, "needs version") {
		t.Errorf("update without version: %s", text)
	}
}

func TestCommentIsMultipart(t *testing.T) {
	var ct, part string
	cs := session(t, allow("tickets"), func(w http.ResponseWriter, r *http.Request) {
		ct = r.Header.Get("Content-Type")
		if err := r.ParseMultipartForm(1 << 20); err == nil {
			part = r.MultipartForm.Value["comment"][0]
		}
		w.WriteHeader(http.StatusNoContent)
	})
	_, isErr, text := call(t, cs, "ninjaone_ticket", map[string]any{"action": "comment", "id": 9, "body": map[string]any{"public": false, "body": "checked"}, "reason": "triage"})
	if isErr || !strings.HasPrefix(ct, "multipart/form-data") || !strings.Contains(part, `"body":"checked"`) {
		t.Errorf("comment: %s ct=%s part=%s", text, ct, part)
	}
}

// Global custom field values and definitions are never writable.
func TestNeverExposedWritesAbsent(t *testing.T) {
	all := allow(config.Capabilities...)
	d := Deps{Config: all}
	for _, tl := range Tools() {
		for _, v := range d.views(tl) {
			if !v.write() {
				continue
			}
			for _, bad := range []string{"/v2/system/custom-fields", "/v2/custom-fields", "/v2/webhook", "/v2/user/technician", "/v2/user/role", "/v2/billing", "/v2/policies", "generate-installer"} {
				if strings.HasPrefix(v.Path, bad) || strings.Contains(v.Path, bad) {
					t.Errorf("%s.%s writes never-exposed %s", tl.Name, v.Action, v.Path)
				}
			}
		}
	}
	// The read of global values stays available.
	cs := session(t, all, func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, `{}`) })
	if a := actionsOf(t, cs, "ninjaone_custom_field"); !slices.Contains(a, "global") || slices.Contains(a, "set_global") {
		t.Errorf("actions = %v", a)
	}
}
