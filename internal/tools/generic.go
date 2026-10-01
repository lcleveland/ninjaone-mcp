package tools

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// never marks a route no capability unlocks: tenant administration,
// security and billing stay in the NinjaOne console.
const never = "never"

// route classifies one non-GET method + path template.
type route struct {
	method, pattern string
	capability      string // "" for a read served over POST, never for blocked
	confirm         bool
	dispatch        bool
}

// genericRoutes are writes with no first-class action. Everything a
// first-class action covers comes from the tool table, so the two cannot
// drift apart.
var genericRoutes = []route{
	// documentation
	{"POST", "/v2/attachments/temp/upload", "documentation", false, false},
	{"POST", "/v2/checklist/archive", "documentation", false, false},
	{"POST", "/v2/checklist/restore", "documentation", false, false},
	{"DELETE", "/v2/checklist/template/{checklistTemplateId}", "documentation", false, false},
	{"POST", "/v2/checklist/templates", "documentation", false, false},
	{"PUT", "/v2/checklist/templates", "documentation", false, false},
	{"POST", "/v2/checklist/templates/delete", "documentation", false, false},
	{"POST", "/v2/document-templates/archive", "documentation", false, false},
	{"POST", "/v2/document-templates/restore", "documentation", false, false},
	{"DELETE", "/v2/document-templates/{documentTemplateId}", "documentation", false, false},
	{"POST", "/v2/knowledgebase/articles/upload", "documentation", false, false},
	{"POST", "/v2/knowledgebase/folders/archive", "documentation", false, false},
	{"POST", "/v2/knowledgebase/folders/delete", "documentation", false, false},
	{"PATCH", "/v2/knowledgebase/folders/move", "documentation", false, false},
	{"POST", "/v2/knowledgebase/folders/restore", "documentation", false, false},
	{"DELETE", "/v2/organization/checklist/{checklistId}", "documentation", false, false},
	{"POST", "/v2/organization/checklists", "documentation", false, false},
	{"PUT", "/v2/organization/checklists", "documentation", false, false},
	{"POST", "/v2/organization/checklists/delete", "documentation", false, false},
	{"POST", "/v2/organization/checklists/promote", "documentation", false, false},
	{"POST", "/v2/organization/checklists/promote-with-name", "documentation", false, false},
	{"POST", "/v2/organization/document/{clientDocumentId}/archive", "documentation", false, false},
	{"POST", "/v2/organization/document/{clientDocumentId}/restore", "documentation", false, false},
	{"POST", "/v2/organization/{organizationId}/checklists-from-templates", "documentation", false, false},
	{"POST", "/v2/organization/{organizationId}/document/{clientDocumentId}", "documentation", false, false},
	{"POST", "/v2/organization/{organizationId}/template/{documentTemplateId}/document", "documentation", false, false},
	{"POST", "/v2/related-items/entity/{entityType}/{entityId}/attachment", "documentation", false, false},
	{"POST", "/v2/related-items/entity/{entityType}/{entityId}/relation", "documentation", false, false},
	{"POST", "/v2/related-items/entity/{entityType}/{entityId}/relations", "documentation", false, false},
	{"POST", "/v2/related-items/entity/{entityType}/{entityId}/secure", "documentation", false, false},
	{"DELETE", "/v2/related-items/{entityType}/{entityId}", "documentation", false, false},
	{"DELETE", "/v2/related-items/{relatedItemId}", "documentation", false, false},
	{"POST", "/v2/tag", "documentation", false, false},
	{"POST", "/v2/tag/{assetType}", "documentation", false, false},
	{"PUT", "/v2/tag/{assetType}/{assetId}", "documentation", false, false},
	{"POST", "/v2/tag/delete", "documentation", false, false},
	{"POST", "/v2/tag/merge", "documentation", false, false},
	{"DELETE", "/v2/tag/{tagId}", "documentation", false, false},
	{"PUT", "/v2/tag/{tagId}", "documentation", false, false},

	// device-admin: inventory records that are not live devices
	{"DELETE", "/v2/itam/asset-relationship", "device-admin", false, false},
	{"POST", "/v2/itam/asset-relationship", "device-admin", false, false},
	{"POST", "/v2/itam/asset-relationship/types", "device-admin", false, false},
	{"POST", "/v2/itam/unmanaged-device", "device-admin", false, false},
	{"POST", "/v2/itam/unmanaged-device/decommissionList", "device-admin", false, false},
	{"DELETE", "/v2/itam/unmanaged-device/{nodeId}", "device-admin", false, false},
	{"PUT", "/v2/itam/unmanaged-device/{nodeId}", "device-admin", false, false},
	{"POST", "/v2/itam/unmanaged-device/{nodeId}/decommission", "device-admin", false, false},
	{"POST", "/v2/software-license", "device-admin", false, false},
	{"PUT", "/v2/software-license/assignments/last-usage", "device-admin", false, false},
	{"DELETE", "/v2/software-license/{licenseId}", "device-admin", false, false},
	{"PUT", "/v2/software-license/{licenseId}", "device-admin", false, false},
	{"POST", "/v2/software-license/upsert", "device-admin", false, false},
	{"POST", "/v2/staged-device", "device-admin", false, false},
}

// neverPrefixes block whole areas: custom-field definitions and global
// values, policies and their mappings, node roles, tabs, users/roles/
// contacts, webhooks, installers, organization archive, billing and
// vulnerability imports.
var neverPrefixes = []string{
	"/v2/billing/", "/v2/contact", "/v2/custom-fields", "/v2/noderole", "/v2/organization/archive",
	"/v2/organization/restore", "/v2/organization/generate-installer", "/v2/organization/{id}/policies",
	"/v2/policies", "/v2/system/custom-fields", "/v2/tab", "/v2/user/", "/v2/vulnerability/", "/v2/webhook",
}

// routes builds the full table: first-class actions first (they carry
// confirm and dispatch), then the generic-only writes.
func routes() []route {
	var rs []route
	for _, t := range Tools() {
		for _, v := range t.Views {
			if v.method() == http.MethodGet {
				continue
			}
			rs = append(rs, route{v.method(), v.Path, v.Capability, v.Confirm, v.Dispatch})
		}
	}
	return append(rs, genericRoutes...)
}

// classify finds the route for a concrete method and path.
func classify(rs []route, method, path string) (route, bool) {
	for _, r := range rs {
		if r.method == method && matches(r.pattern, path) {
			return r, true
		}
	}
	for _, p := range neverPrefixes {
		if prefixMatch(p, path) {
			return route{method: method, pattern: p, capability: never}, true
		}
	}
	return route{}, false
}

// matches compares a template (/v2/device/{id}/reboot/FORCED) with a
// concrete path segment by segment; {x} matches any one segment.
func matches(pattern, path string) bool {
	ps, xs := strings.Split(pattern, "/"), strings.Split(path, "/")
	if len(ps) != len(xs) {
		return false
	}
	for i := range ps {
		if !(strings.HasPrefix(ps[i], "{") || ps[i] == xs[i]) {
			return false
		}
	}
	return true
}

// prefixMatch is matches on the first segments of path, with a partial last
// segment allowed (/v2/contact also blocks /v2/contacts).
func prefixMatch(prefix, path string) bool {
	ps, xs := strings.Split(strings.TrimSuffix(prefix, "/"), "/"), strings.Split(path, "/")
	if len(xs) < len(ps) {
		return false
	}
	last := len(ps) - 1
	return matches(strings.Join(ps[:last], "/"), strings.Join(xs[:last], "/")) &&
		(strings.HasPrefix(ps[last], "{") || strings.HasPrefix(xs[last], ps[last]))
}

type APIInput struct {
	Method  string         `json:"method" jsonschema:"HTTP method"`
	Path    string         `json:"path" jsonschema:"NinjaOne API path starting with /v2/, e.g. /v2/device/12/volumes"`
	Query   map[string]any `json:"query,omitempty" jsonschema:"query params, e.g. {\"df\": \"org = 3\", \"pageSize\": 50}"`
	Body    any            `json:"body,omitempty" jsonschema:"JSON body for POST/PUT/PATCH/DELETE"`
	Reason  string         `json:"reason,omitempty" jsonschema:"required for writes: why, recorded in the audit log"`
	Confirm string         `json:"confirm,omitempty" jsonschema:"forced reboot, decommission and script run: the device's display name, exactly"`
}

func registerGeneric(s *mcp.Server, d Deps) error {
	rs := routes()
	methods := []string{http.MethodGet, http.MethodPost}
	if len(d.Config.Enabled()) > 0 {
		methods = append(methods, http.MethodPut, http.MethodPatch, http.MethodDelete)
	}
	schema, err := jsonschema.For[APIInput](nil)
	if err != nil {
		return err
	}
	schema.Properties["method"].Enum = make([]any, len(methods))
	for i, m := range methods {
		schema.Properties["method"].Enum[i] = m
	}
	schema.Required = []string{"method", "path"}

	desc := "Call any NinjaOne API v2 endpoint without a dedicated tool. Prefer the ninjaone_* tools when one fits: they page, trim and convert timestamps; this returns raw JSON (timestamps in epoch seconds), capped in size.\n\n" +
		"GET is always allowed. POST is allowed for reads NinjaOne serves over POST (ticket board runs). "
	if on := d.Config.Enabled(); len(on) > 0 {
		desc += "Writes are allowed only on routes belonging to an enabled capability (" + strings.Join(on, ", ") + ") and require reason; tenant administration (policies, users, roles, webhooks, installers, billing, custom-field definitions) is never allowed."
	} else {
		desc += "Writes are disabled by the operator."
	}

	mcp.AddTool(s, &mcp.Tool{
		Name:        "ninjaone_api",
		Title:       "NinjaOne API (generic)",
		Description: desc,
		InputSchema: schema,
		Annotations: &mcp.ToolAnnotations{
			ReadOnlyHint:    len(d.Config.Enabled()) == 0,
			DestructiveHint: new(len(d.Config.Enabled()) > 0),
			OpenWorldHint:   new(true),
		},
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in APIInput) (*mcp.CallToolResult, any, error) {
		out, err := d.generic(ctx, rs, in)
		return nil, out, err
	})
	return nil
}

func (d Deps) generic(ctx context.Context, rs []route, in APIInput) (any, error) {
	method := strings.ToUpper(in.Method)
	path, err := apiPath(in.Path)
	if err != nil {
		return nil, err
	}
	q, err := toValues(in.Query)
	if err != nil {
		return nil, err
	}
	q.Del("showSecureValues") // never: it returns secret fields in plaintext

	if method == http.MethodGet {
		if in.Body != nil {
			return nil, errors.New("GET takes no body")
		}
		return d.raw(ctx, method, path, q, nil)
	}
	r, ok := classify(rs, method, path)
	switch {
	case !ok:
		return nil, fmt.Errorf("%s %s is not a known NinjaOne write route; this server refuses writes it cannot classify", method, path)
	case r.capability == never:
		return nil, fmt.Errorf("%s %s is tenant administration, which this server never exposes; do it in the NinjaOne console", method, path)
	case r.capability == "":
		return d.raw(ctx, method, path, q, in.Body) // a read over POST
	case !d.Config.Allow[r.capability]:
		return nil, fmt.Errorf("%s %s needs the %s capability, which the operator has disabled", method, path, r.capability)
	}

	// The same write path as the first-class tools.
	v := View{Action: method + " " + r.pattern, Method: method, Path: path, Capability: r.capability,
		Body: true, Bulk: true, Confirm: r.confirm, Dispatch: r.dispatch}
	wi := Input{Body: in.Body, Reason: in.Reason, Confirm: in.Confirm, Query: in.Query}
	if r.confirm || r.dispatch {
		// These routes are all /v2/device/{id}/...: the id is the 3rd segment.
		wi.ID = strings.Split(path, "/")[3]
	}
	delete(wi.Query, "showSecureValues")
	return d.write(ctx, "ninjaone_api", v, wi)
}

func (d Deps) raw(ctx context.Context, method, path string, q map[string][]string, body any) (any, error) {
	resp, err := d.Client.Do(ctx, method, path, q, body)
	if err != nil {
		return nil, err
	}
	v, err := resp.Decode()
	if err != nil {
		return nil, err
	}
	v = capBytes(v)
	if l, ok := v.([]any); ok {
		v = map[string]any{"results": l} // structured output must be an object
	}
	return v, nil
}

// apiPath confines the generic tool to the API: a relative path under /v2/
// with no traversal, encoding tricks, query or fragment.
func apiPath(p string) (string, error) {
	bad := errors.New("path must be a NinjaOne API path like /v2/devices (no host, query string, '..' or encoded characters)")
	if !strings.HasPrefix(p, "/v2/") || strings.ContainsAny(p, "%?#\\ \t\r\n") || strings.Contains(p, "//") {
		return "", bad
	}
	if slices.ContainsFunc(strings.Split(p, "/"), func(s string) bool { return s == "." || s == ".." }) {
		return "", bad
	}
	return strings.TrimSuffix(p, "/"), nil
}
