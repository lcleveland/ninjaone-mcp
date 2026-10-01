package tools

import (
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/lcleveland/ninjaone-mcp/internal/config"
)

func devices(from, n int) string {
	var items []string
	for i := from; i < from+n; i++ {
		items = append(items, fmt.Sprintf(`{"id":%d,"systemName":"pc%d","lastContact":1790870000.5,"secretSauce":"x"}`, i, i))
	}
	return "[" + strings.Join(items, ",") + "]"
}

func TestAfterPagingRoundTrip(t *testing.T) {
	var seen []string
	cs := session(t, nil, func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r.URL.RawQuery)
		if r.URL.Query().Get("after") == "" {
			jsonOK(w, devices(1, 3))
		} else {
			jsonOK(w, devices(4, 1))
		}
	})
	out, isErr, text := call(t, cs, "ninjaone_device", map[string]any{"action": "list", "limit": 3, "df": "org = 1 AND offline"})
	if isErr {
		t.Fatal(text)
	}
	res := out["results"].([]any)
	first := res[0].(map[string]any)
	if len(res) != 3 || first["secretSauce"] != nil || first["lastContact"] != "2026-10-01T15:53:20Z" {
		t.Fatalf("page 1 = %v", out)
	}
	cur, _ := out["next_cursor"].(string)
	if cur == "" {
		t.Fatal("no next_cursor on a full page")
	}
	if !strings.Contains(seen[0], "df=org%20%3D%201%20AND%20offline") || !strings.Contains(seen[0], "pageSize=3") {
		t.Errorf("query = %s", seen[0])
	}

	out, _, _ = call(t, cs, "ninjaone_device", map[string]any{"action": "list", "limit": 3, "cursor": cur})
	if !strings.Contains(seen[1], "after=3") || out["next_cursor"] != nil || len(out["results"].([]any)) != 1 {
		t.Errorf("page 2: query %s, out %v", seen[1], out)
	}

	// A cursor from another action is refused rather than misread.
	_, isErr, text = call(t, cs, "ninjaone_organization", map[string]any{"action": "list", "cursor": cur})
	if !isErr || !strings.Contains(text, "does not belong") {
		t.Errorf("foreign cursor: %v %s", isErr, text)
	}
}

func TestServerCursorSchemes(t *testing.T) {
	cases := []struct {
		tool, action string
		args         map[string]any
		reply        string
		wantParam    string
	}{
		{"ninjaone_report", "operating_systems", nil,
			`{"cursor":{"name":"c-123","offset":0,"count":2,"expires":1790870000},"results":[{"deviceId":1},{"deviceId":2}]}`, "cursor=c-123"},
	}
	for _, tc := range cases {
		var q []string
		cs := session(t, nil, func(w http.ResponseWriter, r *http.Request) {
			q = append(q, r.URL.RawQuery)
			jsonOK(w, tc.reply)
		})
		args := map[string]any{"action": tc.action, "limit": 2}
		out, isErr, text := call(t, cs, tc.tool, args)
		if isErr || out["next_cursor"] == nil {
			t.Fatalf("%s: %v %s", tc.action, out, text)
		}
		args["cursor"] = out["next_cursor"]
		call(t, cs, tc.tool, args)
		if !strings.Contains(q[1], tc.wantParam) {
			t.Errorf("%s: second query %s lacks %s", tc.action, q[1], tc.wantParam)
		}
	}
}

func TestUnpagedListIsCut(t *testing.T) {
	cs := session(t, nil, func(w http.ResponseWriter, r *http.Request) {
		jsonOK(w, devices(1, 30))
	})
	out, _, _ := call(t, cs, "ninjaone_lookup", map[string]any{"action": "policies", "limit": 10})
	tr, _ := out["_truncation"].(map[string]any)
	if len(out["results"].([]any)) != 10 || tr == nil || tr["of"] != 30.0 || out["next_cursor"] != nil {
		t.Errorf("out = %v", out)
	}
	if !strings.Contains(tr["note"].(string), "not paged") {
		t.Errorf("note = %v", tr["note"])
	}
}

func TestByteCapResumesAfterLastKept(t *testing.T) {
	big := strings.Repeat("x", 2000)
	cs := session(t, nil, func(w http.ResponseWriter, r *http.Request) {
		var items []string
		for i := 1; i <= 100; i++ {
			items = append(items, fmt.Sprintf(`{"id":%d,"name":"%s"}`, i, big))
		}
		jsonOK(w, "["+strings.Join(items, ",")+"]")
	})
	out, _, _ := call(t, cs, "ninjaone_location", map[string]any{"action": "list", "limit": 100})
	res := out["results"].([]any)
	if len(res) >= 100 || out["_truncation"] == nil {
		t.Fatalf("not truncated: %d", len(res))
	}
	last := res[len(res)-1].(map[string]any)["id"].(float64)
	pos, err := decodeCursor("ninjaone_location.list", out["next_cursor"].(string))
	if err != nil || pos != fmt.Sprint(last) {
		t.Errorf("next cursor %q, want resume after %v", pos, last)
	}
}

func TestFieldsAndTimes(t *testing.T) {
	cs := session(t, nil, func(w http.ResponseWriter, r *http.Request) {
		jsonOK(w, `{"id":7,"systemName":"srv","created":1790870000,"warrantyEndDate":1790870000000,"memory":1790870000}`)
	})
	out, _, _ := call(t, cs, "ninjaone_device", map[string]any{"action": "get", "id": 7})
	if out["created"] != "2026-10-01T15:53:20Z" || out["warrantyEndDate"] != "2026-10-01T15:53:20Z" || out["memory"] != 1790870000.0 {
		t.Errorf("times: %v", out)
	}
	out, _, _ = call(t, cs, "ninjaone_device", map[string]any{"action": "get", "id": 7, "fields": "id,systemName"})
	if len(out) != 2 || out["systemName"] != "srv" {
		t.Errorf("fields: %v", out)
	}
}

func TestDFAndSecureValuesGuards(t *testing.T) {
	cs := session(t, nil, nil)
	_, isErr, text := call(t, cs, "ninjaone_location", map[string]any{"action": "list", "df": "org = 1"})
	if !isErr || !strings.Contains(text, "df") {
		t.Errorf("df on location: %s", text)
	}
	_, isErr, text = call(t, cs, "ninjaone_report", map[string]any{"action": "software", "query": map[string]any{"showSecureValues": true}})
	if !isErr || !strings.Contains(text, "never sent") {
		t.Errorf("showSecureValues: %s", text)
	}
}

func TestPathPlaceholders(t *testing.T) {
	var path string
	cs := session(t, nil, func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.EscapedPath()
		jsonOK(w, "[]")
	})
	call(t, cs, "ninjaone_organization", map[string]any{"action": "locations", "id": 12})
	if path != "/v2/organization/12/locations" {
		t.Errorf("path = %s", path)
	}
	_, isErr, text := call(t, cs, "ninjaone_organization", map[string]any{"action": "locations"})
	if !isErr || !strings.Contains(text, "needs id") {
		t.Errorf("missing id: %s", text)
	}
	call(t, cs, "ninjaone_device_detail", map[string]any{"action": "disks", "id": "../../x"})
	if path != "/v2/device/..%2F..%2Fx/disks" {
		t.Errorf("escaped path = %s", path)
	}
}

func TestToolGroups(t *testing.T) {
	cs := session(t, &config.Config{Groups: []string{"inventory"}}, nil)
	names := toolNames(t, cs)
	if names["ninjaone_status"] == nil || names["ninjaone_device"] == nil {
		t.Errorf("missing tools: %v", names)
	}
	for n, tl := range names {
		if !tl.Annotations.ReadOnlyHint {
			t.Errorf("%s is not read-only with no capabilities", n)
		}
	}
	cs = session(t, &config.Config{Groups: []string{"alerts"}}, nil)
	if toolNames(t, cs)["ninjaone_device"] != nil {
		t.Error("inventory registered with only alerts enabled")
	}
}

// The table is the whole tool surface; check it is well formed.
func TestTableWellFormed(t *testing.T) {
	seen := map[string]bool{}
	for _, tl := range Tools() {
		if seen[tl.Name] || !strings.HasPrefix(tl.Name, "ninjaone_") || !slices.Contains(config.Groups, tl.Group) {
			t.Errorf("tool %s: duplicate, bad name or unknown group %q", tl.Name, tl.Group)
		}
		seen[tl.Name] = true
		acts := map[string]bool{}
		for _, v := range tl.Views {
			if acts[v.Action] || v.Help == "" || !strings.HasPrefix(v.Path, "/v2/") {
				t.Errorf("%s.%s: duplicate action, no help, or bad path %q", tl.Name, v.Action, v.Path)
			}
			acts[v.Action] = true
			if (v.Paging == After || v.Paging == Anchor) && v.Brief != nil && !slices.Contains(v.Brief, "id") {
				t.Errorf("%s.%s: id-paged brief must keep id", tl.Name, v.Action)
			}
			if v.Capability != "" && !slices.Contains(config.Capabilities, v.Capability) {
				t.Errorf("%s.%s: unknown capability %q", tl.Name, v.Action, v.Capability)
			}
		}
	}
}
