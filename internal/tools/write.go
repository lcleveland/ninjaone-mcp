package tools

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"net/url"
	"strings"

	"github.com/lcleveland/ninjaone-mcp/internal/ninjaone"
)

// write runs a write view. Every write, from any tool, goes through here so
// the capability check, reason, bulk cap, confirmation and audit log cannot
// be skipped. Nothing here retries: NinjaOne actions take no idempotency key.
func (d Deps) write(ctx context.Context, tool string, v View, in Input) (any, error) {
	if !d.Config.Allow[v.Capability] {
		return nil, fmt.Errorf("the %s capability is disabled by the operator", v.Capability)
	}
	reason := strings.TrimSpace(in.Reason)
	if reason == "" {
		return nil, errors.New("reason is required for writes; say why, it is recorded in the audit log")
	}
	q, err := toValues(in.Query)
	if err != nil {
		return nil, err
	}
	path, err := fill(v.Path, in.ID, q)
	if err != nil {
		return nil, err
	}
	body, err := d.writeBody(v, in.Body, reason)
	if err != nil {
		return nil, err
	}

	if v.Confirm {
		if err := d.confirm(ctx, in); err != nil {
			return nil, err
		}
	}
	var since string
	if v.Dispatch {
		since = d.latestActivity(ctx, in.ID)
	}

	var send any = body
	if v.Multipart != "" {
		send = ninjaone.Multipart{Part: v.Multipart, Value: body}
	}
	resp, err := d.Client.Do(ctx, v.method(), path, q, send)
	audit := []any{"tool", tool, "action", v.Action, "capability", v.Capability, "method", v.method(), "path", path, "reason", reason}
	if err != nil {
		var ae *ninjaone.APIError
		if errors.As(err, &ae) {
			audit = append(audit, "status", ae.Status, "request_id", ae.RequestID)
		}
		d.Log.Warn("ninjaone write failed", append(audit, "error", err.Error())...)
		return nil, err
	}
	d.Log.Info("ninjaone write", append(audit, "status", resp.Status, "request_id", resp.RequestID)...)

	out := map[string]any{"status": resp.Status}
	if resp.RequestID != "" {
		out["request_id"] = resp.RequestID
	}
	if r, err := resp.Decode(); err == nil && r != nil {
		out["result"] = times(r)
	}
	if v.Dispatch {
		out["dispatched"] = true
		out["confirmed"] = false
		check := map[string]any{"tool": "ninjaone_activity", "action": "device", "id": in.ID}
		if since != "" {
			check["query"] = map[string]any{"newerThan": since}
		}
		out["check"] = check
		out["note"] = "NinjaOne accepted the action and queued it to the device's agent; it has not run yet. Check the device's activities for the outcome. Do not repeat the action unless the activities show it failed."
	}
	return out, nil
}

// writeBody validates the body against the view and forwards reason where
// NinjaOne takes one.
func (d Deps) writeBody(v View, body any, reason string) (any, error) {
	if !v.Body && body != nil {
		return nil, fmt.Errorf("action %s takes no body", v.Action)
	}
	switch t := body.(type) {
	case []any:
		if !v.Bulk {
			return nil, fmt.Errorf("action %s takes one record, not a list", v.Action)
		}
		if len(t) == 0 {
			return nil, errors.New("body is an empty list")
		}
		if len(t) > d.Config.MaxBulk {
			return nil, fmt.Errorf("batch of %d records exceeds the limit of %d; split it", len(t), d.Config.MaxBulk)
		}
		return t, nil
	case map[string]any:
		for _, k := range v.Require {
			if _, ok := t[k]; !ok {
				return nil, fmt.Errorf("body needs %s", k)
			}
		}
		if v.ReasonField != "" {
			t = maps.Clone(t)
			t[v.ReasonField] = reason
		}
		return t, nil
	case nil:
		if len(v.Require) > 0 {
			return nil, fmt.Errorf("body needs %s", strings.Join(v.Require, ", "))
		}
		if v.ReasonField != "" {
			return map[string]any{v.ReasonField: reason}, nil
		}
		return nil, nil
	}
	return nil, errors.New("body must be an object or a list of objects")
}

// confirm makes the model name the device it means to act on, so a wrong
// id fails before anything is sent.
func (d Deps) confirm(ctx context.Context, in Input) error {
	id := scalarString(in.ID)
	resp, err := d.Client.Do(ctx, http.MethodGet, "/v2/device/"+url.PathEscape(id), nil, nil)
	if err != nil {
		return fmt.Errorf("looking up device %s to confirm: %w", id, err)
	}
	v, _ := resp.Decode()
	m, _ := v.(map[string]any)
	name, _ := m["displayName"].(string)
	if name == "" {
		name, _ = m["systemName"].(string)
	}
	if name == "" || strings.TrimSpace(in.Confirm) != name {
		return fmt.Errorf("confirm must be the device's display name exactly; device %s is %q. Check this is the device you mean, then retry", id, name)
	}
	return nil
}

// latestActivity is the newest activity id on a device, so the model can
// look for what happened after its action. Best effort.
func (d Deps) latestActivity(ctx context.Context, id any) string {
	resp, err := d.Client.Do(ctx, http.MethodGet, "/v2/device/"+url.PathEscape(scalarString(id))+"/activities", url.Values{"pageSize": {"10"}}, nil)
	if err != nil {
		return ""
	}
	v, _ := resp.Decode()
	if acts := asList(v, "activities"); len(acts) > 0 {
		return idOf(acts[0])
	}
	return ""
}
