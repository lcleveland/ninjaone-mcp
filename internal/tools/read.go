package tools

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"
)

const (
	defaultLimit = 50
	maxItems     = 200
	maxBytes     = 60 << 10 // keeps a result inside a sensible slice of context
)

// fill substitutes {placeholders} in a view's path: {id} from id, the rest
// from query (consumed). Values are path-escaped.
func fill(path string, id any, query url.Values) (string, error) {
	var b strings.Builder
	for {
		i := strings.IndexByte(path, '{')
		if i < 0 {
			b.WriteString(path)
			return b.String(), nil
		}
		j := strings.IndexByte(path[i:], '}')
		name := path[i+1 : i+j]
		b.WriteString(path[:i])
		path = path[i+j+1:]
		var v string
		if name == "id" {
			v = scalarString(id)
			if v == "" {
				return "", errors.New("this action needs id")
			}
		} else {
			v = query.Get(name)
			if v == "" {
				return "", fmt.Errorf("this action needs query.%s", name)
			}
			query.Del(name)
		}
		b.WriteString(url.PathEscape(v))
	}
}

func scalarString(v any) string {
	switch t := v.(type) {
	case string:
		return strings.TrimSpace(t)
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64)
	case json.Number:
		return t.String()
	}
	return ""
}

// read runs a read view and shapes the reply for the model.
func (d Deps) read(ctx context.Context, tool string, v View, in Input) (any, error) {
	q, err := toValues(in.Query)
	if err != nil {
		return nil, err
	}
	if q.Has("showSecureValues") {
		return nil, errors.New("showSecureValues is never sent: it returns secret custom fields in plaintext")
	}
	path, err := fill(v.Path, in.ID, q)
	if err != nil {
		return nil, err
	}
	if in.DF != "" {
		if !v.DF {
			return nil, fmt.Errorf("action %s does not take a device filter (df)", v.Action)
		}
		q.Set("df", in.DF)
	}

	if v.Single {
		resp, err := d.Client.Do(ctx, v.method(), path, q, nil)
		if err != nil {
			return nil, err
		}
		out, err := resp.Decode()
		if err != nil {
			return nil, err
		}
		out = times(out)
		if v.DeepLink {
			out = d.deepLink(ctx, path, out)
		}
		if in.Fields != "" {
			out = project(out, strings.Split(in.Fields, ","))
		}
		return capBytes(out), nil
	}
	return d.list(ctx, tool+"."+v.Action, v, path, q, in)
}

func (d Deps) list(ctx context.Context, key string, v View, path string, q url.Values, in Input) (map[string]any, error) {
	limit := in.Limit
	if limit <= 0 {
		limit = defaultLimit
	}
	limit = min(limit, maxItems)
	pos, err := decodeCursor(key, in.Cursor)
	if err != nil {
		return nil, err
	}

	var body any
	switch v.Paging {
	case After:
		q.Set("pageSize", strconv.Itoa(limit))
		if pos != "" {
			q.Set("after", pos)
		}
	case Cursor:
		q.Set("pageSize", strconv.Itoa(limit))
		if pos != "" {
			q.Set("cursor", pos)
		}
	case Activity:
		limit = max(limit, 10) // NinjaOne's minimum page
		q.Set("pageSize", strconv.Itoa(limit))
		if pos != "" {
			q.Set("olderThan", pos)
		}
	case Anchor:
		q.Set("pageSize", strconv.Itoa(limit))
		if pos != "" {
			q.Set("anchorId", pos)
		}
	case Board:
		m := map[string]any{}
		if b, ok := in.Body.(map[string]any); ok {
			for k, x := range b {
				m[k] = x
			}
		} else if in.Body != nil {
			return nil, errors.New("body must be an object of board run options (filters, sortBy, searchCriteria)")
		}
		m["pageSize"] = limit
		if pos != "" {
			n, _ := strconv.Atoi(pos)
			m["lastCursorId"] = n
		}
		body = m
	case NoPaging:
		if pos != "" {
			return nil, errors.New("this list is not paged; there is no next page")
		}
		if v.LimitParam != "" {
			q.Set(v.LimitParam, strconv.Itoa(limit))
		}
	}

	resp, err := d.Client.Do(ctx, v.method(), path, q, body)
	if err != nil {
		return nil, err
	}
	raw, err := resp.Decode()
	if err != nil {
		return nil, err
	}

	items, next := page(v, raw)
	of := len(items)
	if len(items) < limit {
		next = "" // a short page is the last page
	}
	unpagedCut := v.Paging == NoPaging && len(items) > limit
	if unpagedCut {
		items = items[:limit] // unpaged endpoints return everything; cut here
	}

	fields := v.Brief
	if in.Fields != "" {
		fields = strings.Split(in.Fields, ",")
	}
	for i, x := range items {
		x = times(x)
		if fields != nil {
			x = project(x, fields)
		}
		items[i] = x
	}

	out := map[string]any{}
	for len(items) > 1 && size(items) > maxBytes {
		items = items[:len(items)/2]
	}
	if len(items) < of {
		trunc := map[string]any{
			"returned": len(items),
			"of":       of,
			"note":     "result too large; narrow the device filter (df) or query, pass fields, or lower limit",
		}
		switch {
		case unpagedCut:
			trunc["note"] = "this list is not paged and was cut at limit; narrow the device filter (df) or query to see the rest"
		case (v.Paging == After || v.Paging == Anchor) && len(items) > 0:
			// Id-anchored: the next page resumes right after the last item kept.
			next = idOf(items[len(items)-1])
		case next != "":
			trunc["note"] = "result too large and the rest of this page was dropped; repeat with a lower limit or fields to see it"
		}
		out["_truncation"] = trunc
	}
	if next != "" {
		out["next_cursor"] = encodeCursor(key, next)
	}
	if items == nil {
		items = []any{}
	}
	out["results"] = items
	return out, nil
}

// page pulls the item list and the raw next position out of a reply.
func page(v View, raw any) ([]any, string) {
	items := asList(raw, v.Items)
	m, _ := raw.(map[string]any)
	switch v.Paging {
	case After, Anchor:
		if len(items) > 0 {
			return items, idOf(items[len(items)-1])
		}
	case Cursor:
		if c, ok := m["cursor"].(map[string]any); ok {
			name, _ := c["name"].(string)
			return items, name
		}
	case Activity:
		return items, scalarString(m["lastActivityId"])
	case Board:
		if md, ok := m["metadata"].(map[string]any); ok {
			return items, scalarString(md["lastCursorId"])
		}
	}
	return items, ""
}

func asList(raw any, key string) []any {
	if key != "" {
		m, _ := raw.(map[string]any)
		raw = m[key]
	}
	l, _ := raw.([]any)
	return slices.Clone(l)
}

func idOf(item any) string {
	m, _ := item.(map[string]any)
	return scalarString(m["id"])
}

// The cursor is opaque to the model and bound to the tool.action that issued it.
func encodeCursor(action, pos string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(action + "\x00" + pos))
}

func decodeCursor(action, c string) (string, error) {
	if c == "" {
		return "", nil
	}
	b, err := base64.RawURLEncoding.DecodeString(c)
	a, pos, ok := strings.Cut(string(b), "\x00")
	if err != nil || !ok || a != action {
		return "", fmt.Errorf("cursor does not belong to action %s; pass next_cursor from the previous %s call unchanged", action, action)
	}
	return pos, nil
}

// project keeps only the named top-level fields.
func project(v any, fields []string) any {
	m, ok := v.(map[string]any)
	if !ok {
		return v
	}
	out := make(map[string]any, len(fields))
	for _, f := range fields {
		f = strings.TrimSpace(f)
		if x, ok := m[f]; ok {
			out[f] = x
		}
	}
	return out
}

// times rewrites epoch timestamps to RFC 3339 UTC. NinjaOne sends most as
// epoch seconds (floats) and custom-field dates as epoch milliseconds; the
// key name and the magnitude together tell them apart from other numbers.
func times(v any) any {
	switch t := v.(type) {
	case map[string]any:
		for k, x := range t {
			if f, ok := x.(float64); ok && timeKey(k) {
				if s, ok := epoch(f); ok {
					t[k] = s
					continue
				}
			}
			t[k] = times(x)
		}
	case []any:
		for i, x := range t {
			t[i] = times(x)
		}
	}
	return v
}

func timeKey(k string) bool {
	k = strings.ToLower(k)
	for _, s := range []string{"time", "date", "at", "created", "updated", "contact", "update", "expires", "timestamp", "boot"} {
		if strings.HasSuffix(k, s) {
			return true
		}
	}
	return false
}

func epoch(f float64) (string, bool) {
	switch {
	case f >= 1e9 && f < 1e10: // seconds, 2001..2286
		return time.Unix(0, int64(f*1e9)).UTC().Format(time.RFC3339), true
	case f >= 1e12 && f < 1e13: // milliseconds
		return time.UnixMilli(int64(f)).UTC().Format(time.RFC3339), true
	}
	return "", false
}

// capBytes trims an oversized single object's largest list, so one huge get
// (a device's software) cannot flood the context.
func capBytes(v any) any {
	if size(v) <= maxBytes {
		return v
	}
	switch t := v.(type) {
	case []any:
		total := len(t)
		for len(t) > 1 && size(t) > maxBytes {
			t = t[:len(t)/2]
		}
		return map[string]any{"results": t, "_truncation": map[string]any{"returned": len(t), "of": total,
			"note": "result too large; pass fields or narrow the query"}}
	case map[string]any:
		return map[string]any{"_truncation": map[string]any{"note": "object too large to return (" + strconv.Itoa(size(v)) + " bytes); pass fields to pick the parts you need"},
			"fields": sortedKeys(t)}
	}
	return v
}

func sortedKeys(m map[string]any) []string {
	ks := make([]string, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	slices.Sort(ks)
	return ks
}

func size(v any) int {
	b, _ := json.Marshal(v)
	return len(b)
}

// deepLink adds the console URL to a device get. Best effort: the endpoint
// may need the management scope.
func (d Deps) deepLink(ctx context.Context, devicePath string, out any) any {
	m, ok := out.(map[string]any)
	if !ok {
		return out
	}
	resp, err := d.Client.Do(ctx, http.MethodGet, devicePath+"/dashboard-url", nil, nil)
	if err != nil {
		return out
	}
	if v, _ := resp.Decode(); v != nil {
		if u, ok := v.(map[string]any)["url"].(string); ok {
			m["_dashboard_url"] = u
		}
	}
	return m
}

// ponytail: time query params (installedAfter, startTime, ...) pass through in
// each endpoint's own format, which the action help names; add RFC 3339
// conversion per param if models keep getting them wrong.
//
// toValues turns {"status": "FAILED", "type": ["A","B"], "pageSize": 10} into
// query params.
func toValues(m map[string]any) (url.Values, error) {
	q := url.Values{}
	for k, v := range m {
		switch t := v.(type) {
		case []any:
			for _, x := range t {
				s, err := scalar(k, x)
				if err != nil {
					return nil, err
				}
				q.Add(k, s)
			}
		default:
			s, err := scalar(k, t)
			if err != nil {
				return nil, err
			}
			q.Add(k, s)
		}
	}
	return q, nil
}

func scalar(k string, v any) (string, error) {
	switch t := v.(type) {
	case string:
		return t, nil
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64), nil
	case bool:
		return strconv.FormatBool(t), nil
	}
	return "", fmt.Errorf("query %q: values must be strings, numbers, booleans or arrays of them", k)
}
