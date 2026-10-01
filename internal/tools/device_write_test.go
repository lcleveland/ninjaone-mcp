package tools

import (
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"testing"
)

// fakeDevice serves device 5 ("FS01"), its activities, and records writes.
func fakeDevice(t *testing.T, writes *[]string, bodies *[]map[string]any) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v2/device/5":
			jsonOK(w, `{"id":5,"displayName":"FS01","systemName":"fs01.corp"}`)
		case r.Method == http.MethodGet && r.URL.Path == "/v2/device/5/activities":
			jsonOK(w, `{"lastActivityId":100,"activities":[{"id":444},{"id":300}]}`)
		case r.Method != http.MethodGet:
			*writes = append(*writes, r.Method+" "+r.URL.Path)
			var b map[string]any
			json.NewDecoder(r.Body).Decode(&b)
			*bodies = append(*bodies, b)
			w.WriteHeader(http.StatusNoContent)
		default:
			jsonOK(w, `[]`)
		}
	}
}

func TestConfirmMismatchSendsNothing(t *testing.T) {
	var writes []string
	var bodies []map[string]any
	cs := session(t, allow("device-actions", "scripts", "device-admin"), fakeDevice(t, &writes, &bodies))
	for _, c := range []struct{ tool, action string }{
		{"ninjaone_device_action", "reboot_forced"},
		{"ninjaone_device", "decommission"},
		{"ninjaone_script", "run"},
	} {
		args := map[string]any{"action": c.action, "id": 5, "reason": "x", "confirm": "fs01"}
		if c.action == "run" {
			args["body"] = map[string]any{"type": "SCRIPT", "id": 12}
		}
		_, isErr, text := call(t, cs, c.tool, args)
		if !isErr || !strings.Contains(text, `"FS01"`) {
			t.Errorf("%s with wrong confirm: %v %s", c.action, isErr, text)
		}
	}
	if len(writes) != 0 {
		t.Fatalf("writes sent despite failed confirm: %v", writes)
	}

	out, isErr, text := call(t, cs, "ninjaone_device_action", map[string]any{"action": "reboot_forced", "id": 5, "reason": "hung", "confirm": "FS01"})
	if isErr || len(writes) != 1 || writes[0] != "POST /v2/device/5/reboot/FORCED" {
		t.Fatalf("confirmed reboot: %v %s %v", out, text, writes)
	}
	if bodies[0]["reason"] != "hung" {
		t.Errorf("reason not forwarded: %v", bodies[0])
	}
	check, _ := out["check"].(map[string]any)
	q, _ := check["query"].(map[string]any)
	if out["dispatched"] != true || out["confirmed"] != false || check["tool"] != "ninjaone_activity" || q["newerThan"] != "444" {
		t.Errorf("dispatch report: %v", out)
	}
}

func TestEachCapabilityGatesOnlyItsOwn(t *testing.T) {
	cs := session(t, allow("device-maintenance"), nil)
	acts := actionsOf(t, cs, "ninjaone_device_action")
	if !slices.Contains(acts, "maintenance_set") || slices.Contains(acts, "reboot") {
		t.Errorf("device-maintenance only: %v", acts)
	}
	if a := actionsOf(t, cs, "ninjaone_alert"); !slices.Contains(a, "reset") {
		t.Errorf("alert reset missing: %v", a)
	}
	if a := actionsOf(t, cs, "ninjaone_script"); slices.Contains(a, "run") {
		t.Errorf("scripts leaked: %v", a)
	}
	if a := actionsOf(t, cs, "ninjaone_device"); slices.Contains(a, "decommission") {
		t.Errorf("device-admin leaked: %v", a)
	}

	cs = session(t, nil, nil)
	if a := actionsOf(t, cs, "ninjaone_script"); !slices.Equal(a, []string{"options"}) {
		t.Errorf("read-only script tool: %v", a)
	}
	if toolNames(t, cs)["ninjaone_device_action"] != nil {
		t.Error("device action tool registered with no capability")
	}
}

func TestMaintenanceForwardsReasonAndRejectsLists(t *testing.T) {
	var writes []string
	var bodies []map[string]any
	cs := session(t, allow("device-maintenance"), fakeDevice(t, &writes, &bodies))
	_, isErr, text := call(t, cs, "ninjaone_device_action", map[string]any{"action": "maintenance_set", "id": 5, "reason": "patch window",
		"body": map[string]any{"disabledFeatures": []any{"ALERTS"}, "end": 1790900000}})
	if isErr || bodies[0]["reasonMessage"] != "patch window" {
		t.Errorf("maintenance: %s %v", text, bodies)
	}
	_, isErr, text = call(t, cs, "ninjaone_device_action", map[string]any{"action": "maintenance_set", "id": 5, "reason": "x",
		"body": []any{map[string]any{"end": 1}}})
	if !isErr || !strings.Contains(text, "not a list") {
		t.Errorf("list body: %s", text)
	}
}

func TestUserContextRequiredHint(t *testing.T) {
	cs := session(t, allow("scripts"), func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			fakeDevice(t, new([]string), new([]map[string]any))(w, r)
			return
		}
		w.WriteHeader(http.StatusForbidden)
		jsonOK(w, `{"resultCode":"user_context_required"}`)
	})
	_, isErr, text := call(t, cs, "ninjaone_script", map[string]any{"action": "run", "id": 5, "reason": "x", "confirm": "FS01", "body": map[string]any{"type": "ACTION", "uid": "u"}})
	if !isErr || !strings.Contains(text, "signed-in NinjaOne user") {
		t.Errorf("text = %s", text)
	}
}
