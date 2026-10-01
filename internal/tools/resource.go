package tools

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Input is shared by every first-class tool.
type Input struct {
	Action  string         `json:"action" jsonschema:"what to do"`
	ID      any            `json:"id,omitempty" jsonschema:"the object id (or uid) the action needs"`
	DF      string         `json:"df,omitempty" jsonschema:"device filter, e.g. \"org = 12 AND class in (WINDOWS_SERVER,WINDOWS_WORKSTATION) AND offline\". AND only, no OR; pending devices are hidden unless status = PENDING"`
	Query   map[string]any `json:"query,omitempty" jsonschema:"extra query params (and path values the action names), e.g. {\"status\": \"FAILED\"}"`
	Fields  string         `json:"fields,omitempty" jsonschema:"comma-separated top-level fields to return instead of the default brief set"`
	Cursor  string         `json:"cursor,omitempty" jsonschema:"next_cursor from the previous call of the same action, to get the next page"`
	Limit   int            `json:"limit,omitempty" jsonschema:"list actions: max items (default 50, max 200)"`
	Body    any            `json:"body,omitempty" jsonschema:"JSON body for actions that take one"`
	Reason  string         `json:"reason,omitempty" jsonschema:"required for writes: why, recorded in the audit log"`
	Confirm string         `json:"confirm,omitempty" jsonschema:"forced reboot, decommission and script run: the device's display name, exactly"`
}

// views filters a tool's actions to those the operator enabled.
func (d Deps) views(t Tool) []View {
	var vs []View
	for _, v := range t.Views {
		if v.write() && !d.Config.Allow[v.Capability] {
			continue
		}
		vs = append(vs, v)
	}
	return vs
}

// registerTool enforces the capability flags three times: the action enum
// omits disabled actions, the description never mentions them, and the
// handler refuses them. Only the last is load-bearing; the first two stop the
// model trying.
func registerTool(s *mcp.Server, d Deps, t Tool) error {
	views := d.views(t)
	if len(views) == 0 {
		return nil
	}
	schema, err := jsonschema.For[Input](nil)
	if err != nil {
		return err
	}
	schema.Properties["id"].Types = []string{"integer", "string"}
	actions := make([]any, len(views))
	for i, v := range views {
		actions[i] = v.Action
	}
	schema.Properties["action"].Enum = actions
	schema.Required = []string{"action"}

	writes := slices.ContainsFunc(views, View.write)
	if !writes {
		delete(schema.Properties, "reason")
		delete(schema.Properties, "confirm")
	}
	if !slices.ContainsFunc(views, func(v View) bool { return v.Body || v.Paging == Board }) {
		delete(schema.Properties, "body")
	}
	if !slices.ContainsFunc(views, func(v View) bool { return v.DF }) {
		delete(schema.Properties, "df")
	}
	if !slices.ContainsFunc(views, func(v View) bool { return v.Confirm }) {
		delete(schema.Properties, "confirm")
	}

	mcp.AddTool(s, &mcp.Tool{
		Name:        t.Name,
		Title:       t.Title,
		Description: t.Description + help(views, d.Config.MaxBulk),
		InputSchema: schema,
		Annotations: &mcp.ToolAnnotations{
			ReadOnlyHint:    !writes,
			DestructiveHint: new(slices.ContainsFunc(views, func(v View) bool { return v.Destructive })),
			OpenWorldHint:   new(true),
		},
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in Input) (*mcp.CallToolResult, any, error) {
		i := slices.IndexFunc(views, func(v View) bool { return v.Action == in.Action })
		if i < 0 {
			return nil, nil, fmt.Errorf("action %q is not available on %s (enabled: %s)", in.Action, t.Name, strings.Join(names(views), ", "))
		}
		v := views[i]
		if v.write() {
			out, err := d.write(ctx, t.Name, v, in)
			return nil, out, err
		}
		out, err := d.read(ctx, t.Name, v, in)
		return nil, out, err
	})
	return nil
}

func help(views []View, maxBulk int) string {
	var b strings.Builder
	b.WriteString("\n\nActions:")
	for _, v := range views {
		b.WriteString("\n- " + v.Action + ": " + v.Help)
	}
	b.WriteString("\n\nLists return brief items (pass fields for others) and next_cursor when there is more; pass it back as cursor. Timestamps are RFC 3339 UTC.")
	if slices.ContainsFunc(views, View.write) {
		fmt.Fprintf(&b, " Writes require reason (audit-logged) and are never retried automatically.")
		if slices.ContainsFunc(views, func(v View) bool { return v.Bulk }) {
			fmt.Fprintf(&b, " Batch writes take at most %d records.", maxBulk)
		}
		if slices.ContainsFunc(views, func(v View) bool { return v.Dispatch }) {
			b.WriteString(" Device actions are dispatched to the agent and return before they run: check ninjaone_activity for the outcome, and never repeat an action just because it is not yet confirmed.")
		}
	}
	return b.String()
}

func names(vs []View) []string {
	s := make([]string, len(vs))
	for i, v := range vs {
		s[i] = v.Action
	}
	return s
}
