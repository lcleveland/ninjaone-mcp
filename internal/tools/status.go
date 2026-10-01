package tools

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type StatusOutput struct {
	BaseURL       string   `json:"base_url"`
	Region        string   `json:"region,omitempty"`
	Authenticated bool     `json:"authenticated"`
	Scopes        []string `json:"scopes,omitempty"`
	TokenExpires  string   `json:"token_expires,omitempty"`
	APIReachable  bool     `json:"api_reachable"`
	Capabilities  []string `json:"write_capabilities"`
	Warnings      []string `json:"warnings,omitempty"`
	Detail        string   `json:"detail,omitempty"`
}

func registerStatus(s *mcp.Server, d Deps) {
	mcp.AddTool(s, &mcp.Tool{
		Name:  "ninjaone_status",
		Title: "NinjaOne connectivity check",
		Description: "Check that the NinjaOne API client can authenticate, report the granted OAuth scopes and " +
			"the write capabilities this server has enabled, and make one cheap authenticated read.\n\n" +
			"Call this first when another tool fails: it tells a wrong region apart from rejected " +
			"credentials apart from a token that works but lacks a scope.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true, OpenWorldHint: new(true)},
	}, func(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, StatusOutput, error) {
		out := StatusOutput{BaseURL: d.Client.BaseURL(), Region: d.Config.Region, Capabilities: d.Config.Enabled()}
		if out.Capabilities == nil {
			out.Capabilities = []string{}
		}
		tok, err := d.Client.Authenticate(ctx)
		if err != nil {
			out.Detail = err.Error()
			return nil, out, nil
		}
		out.Authenticated = true
		out.Scopes = strings.Fields(tok.Scope)
		out.TokenExpires = tok.Expires.UTC().Format(time.RFC3339)
		if len(out.Capabilities) > 0 && !strings.Contains(tok.Scope, "management") {
			out.Warnings = append(out.Warnings, "write capabilities are enabled but the token lacks the management scope every NinjaOne write needs")
		}

		if _, err := d.Client.Do(ctx, http.MethodGet, "/v2/organizations", url.Values{"pageSize": {"1"}}, nil); err != nil {
			out.Detail = err.Error()
			return nil, out, nil
		}
		out.APIReachable = true
		return nil, out, nil
	})
}
