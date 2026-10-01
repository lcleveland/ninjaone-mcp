package tools

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestActivityPaging(t *testing.T) {
	var q []string
	cs := session(t, nil, func(w http.ResponseWriter, r *http.Request) {
		q = append(q, r.URL.RawQuery)
		jsonOK(w, `{"lastActivityId":901,"activities":[`+strings.Repeat(`{"id":1,"activityTime":1790870000,"data":{"big":1}},`, 9)+`{"id":2}]}`)
	})
	out, isErr, text := call(t, cs, "ninjaone_activity", map[string]any{"action": "device", "id": 5, "limit": 3})
	if isErr {
		t.Fatal(text)
	}
	first := out["results"].([]any)[0].(map[string]any)
	if first["data"] != nil || first["activityTime"] != "2026-10-01T15:53:20Z" {
		t.Errorf("brief/times: %v", first)
	}
	if !strings.Contains(q[0], "pageSize=10") { // NinjaOne's minimum
		t.Errorf("query = %s", q[0])
	}
	call(t, cs, "ninjaone_activity", map[string]any{"action": "device", "id": 5, "cursor": out["next_cursor"]})
	if !strings.Contains(q[1], "olderThan=901") {
		t.Errorf("second query = %s", q[1])
	}
}

func TestBoardRunIsAReadAndPages(t *testing.T) {
	var bodies []map[string]any
	cs := session(t, nil, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v2/ticketing/trigger/board/3/run" {
			t.Errorf("%s %s", r.Method, r.URL.Path)
		}
		var b map[string]any
		json.NewDecoder(r.Body).Decode(&b)
		bodies = append(bodies, b)
		jsonOK(w, `{"data":[{"id":1},{"id":2}],"metadata":{"lastCursorId":77}}`)
	})
	if tl := toolNames(t, cs)["ninjaone_ticket"]; tl == nil || !tl.Annotations.ReadOnlyHint {
		t.Fatal("ninjaone_ticket should be registered read-only with no capabilities")
	}
	args := map[string]any{"action": "board_run", "id": 3, "limit": 2, "body": map[string]any{"sortBy": []any{map[string]any{"field": "createTime"}}}}
	out, isErr, text := call(t, cs, "ninjaone_ticket", args)
	if isErr || out["next_cursor"] == nil {
		t.Fatalf("%v %s", out, text)
	}
	args["cursor"] = out["next_cursor"]
	call(t, cs, "ninjaone_ticket", args)
	if bodies[0]["pageSize"] != 2.0 || bodies[0]["sortBy"] == nil || bodies[1]["lastCursorId"] != 77.0 {
		t.Errorf("bodies = %v", bodies)
	}
}

func TestAnchorPaging(t *testing.T) {
	var q []string
	cs := session(t, nil, func(w http.ResponseWriter, r *http.Request) {
		q = append(q, r.URL.RawQuery)
		jsonOK(w, `[{"id":10},{"id":11}]`)
	})
	out, _, _ := call(t, cs, "ninjaone_ticket", map[string]any{"action": "log_entries", "id": 1, "limit": 2})
	call(t, cs, "ninjaone_ticket", map[string]any{"action": "log_entries", "id": 1, "limit": 2, "cursor": out["next_cursor"]})
	if !strings.Contains(q[1], "anchorId=11") {
		t.Errorf("query = %s", q[1])
	}
}

func TestSecureValuesNeverSentFromAnyRead(t *testing.T) {
	cs := session(t, nil, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Has("showSecureValues") {
			t.Errorf("showSecureValues sent to %s", r.URL.Path)
		}
		io.WriteString(w, `{"cursor":{"name":"c"},"results":[]}`)
	})
	for _, v := range []string{"fleet", "fleet_scoped"} {
		_, isErr, _ := call(t, cs, "ninjaone_custom_field", map[string]any{"action": v, "query": map[string]any{"showSecureValues": "true"}})
		if !isErr {
			t.Errorf("%s accepted showSecureValues", v)
		}
	}
}

func TestAllGroupsRegisterReadOnly(t *testing.T) {
	names := toolNames(t, session(t, nil, nil))
	for _, n := range []string{"ninjaone_alert", "ninjaone_activity", "ninjaone_patch", "ninjaone_custom_field", "ninjaone_document", "ninjaone_kb_article", "ninjaone_ticket", "ninjaone_backup"} {
		if tl := names[n]; tl == nil || !tl.Annotations.ReadOnlyHint {
			t.Errorf("%s missing or not read-only", n)
		}
	}
}
