package tools

import (
	"context"
	"errors"
	"fmt"
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

	UserContext  *UserContext `json:"user_context,omitempty"`
	AuthorizeURL string       `json:"authorize_url,omitempty"`
	Note         string       `json:"note,omitempty"`
}

// UserContext reports the signed-in user token that script runs and ticket
// writes use, when the operator configured a Web app.
type UserContext struct {
	Configured bool `json:"configured"`
	Authorized bool `json:"authorized"`
}

type StatusInput struct {
	Action string `json:"action,omitempty" jsonschema:"omit to check the connection; authorize: start a user sign-in for script runs and ticket writes"`
}

func registerStatus(s *mcp.Server, d Deps) {
	mcp.AddTool(s, &mcp.Tool{
		Name:  "ninjaone_status",
		Title: "NinjaOne connectivity check",
		Description: "Check that the NinjaOne API client can authenticate, report the granted OAuth scopes and " +
			"the write capabilities this server has enabled, and make one cheap authenticated read.\n\n" +
			"Call this first when another tool fails: it tells a wrong region apart from rejected " +
			"credentials apart from a token that works but lacks a scope.\n\n" +
			"action authorize returns a sign-in link for user context, which script runs and ticket writes need: " +
			"give it to the user to open in a browser on the server's machine and click Authorize. Once is enough; the server keeps the login.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true, OpenWorldHint: new(true)},
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in StatusInput) (*mcp.CallToolResult, StatusOutput, error) {
		out := StatusOutput{BaseURL: d.Client.BaseURL(), Region: d.Config.Region, Capabilities: d.Config.Enabled()}
		if us := d.Client.UserState(); us.Configured {
			out.UserContext = &UserContext{Configured: true, Authorized: us.Authorized}
		}
		if out.Capabilities == nil {
			out.Capabilities = []string{}
		}
		switch in.Action {
		case "":
		case "authorize":
			return d.authorize(out)
		default:
			return nil, out, fmt.Errorf("unknown action %q (want authorize, or omit it)", in.Action)
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

// authorize starts a user sign-in. The browser comes back to this server's
// own /oauth/callback, so it needs the HTTP transport.
func (d Deps) authorize(out StatusOutput) (*mcp.CallToolResult, StatusOutput, error) {
	switch {
	case out.UserContext == nil:
		return nil, out, errors.New("user context is not configured: the operator must set up a NinjaOne Web app (see the README section on running scripts)")
	case !d.Config.HTTP:
		return nil, out, errors.New("user sign-in needs the server to run with --http, so the browser can reach /oauth/callback")
	}
	u, err := d.Client.AuthorizeURL(d.Config.OAuthRedirectURL)
	if err != nil {
		return nil, out, err
	}
	out.AuthorizeURL = u
	out.Note = "Give this link to the user: open it in a browser on the server's machine, sign in as the NinjaOne user who should own script runs and ticket writes, and click Authorize. It works once, for 10 minutes. Then call ninjaone_status again: user_context.authorized turns true."
	return nil, out, nil
}
