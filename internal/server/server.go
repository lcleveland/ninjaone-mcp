// Package server builds the MCP server and serves it over stdio or HTTP.
package server

import (
	"context"
	"log/slog"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/lcleveland/ninjaone-mcp/internal/config"
	"github.com/lcleveland/ninjaone-mcp/internal/ninjaone"
	"github.com/lcleveland/ninjaone-mcp/internal/tools"
	"github.com/lcleveland/ninjaone-mcp/internal/version"
)

// New builds the server with the enabled tools and reports how many.
func New(cfg *config.Config, c *ninjaone.Client, log *slog.Logger) (*mcp.Server, int) {
	s := mcp.NewServer(&mcp.Implementation{Name: "ninjaone-mcp", Title: "NinjaOne", Version: version.Version},
		&mcp.ServerOptions{Logger: log, Instructions: instructions(cfg)})
	return s, tools.Register(s, tools.Deps{Client: c, Config: cfg, Log: log})
}

func instructions(cfg *config.Config) string {
	s := "Tools for the NinjaOne RMM tenant at " + cfg.BaseURL.String() + " (public API v2): organizations, locations, devices, alerts, activities, patching, custom fields, documentation, backup and tickets.\n\n" +
		"Call ninjaone_status first if anything fails: it separates a wrong region from rejected credentials from a missing scope.\n\n" +
		"Device filters (df) support AND but not OR (use `in (...)`), and hide pending devices unless you ask for `status = PENDING`. Freshservice, not NinjaOne, is the ticket system of record here.\n\n"
	on := cfg.Enabled()
	if len(on) == 0 {
		return s + "This server is read-only: every write capability is disabled by the operator. Do not suggest workarounds; ask the user to change the server configuration if a write is needed."
	}
	return s + "Enabled write capabilities: " + strings.Join(on, ", ") + ". Every write needs a reason, which is audit-logged. " +
		"Device actions (reboot, scripts, patch apply) are dispatched to the agent and are not confirmed when the call returns; check ninjaone_activity for the outcome, and never repeat an action just because it was not yet confirmed. " +
		"NinjaOne has no undo: confirm with the user before anything disruptive."
}

// ServeStdio runs until ctx is cancelled. Nothing else may write to stdout.
func ServeStdio(ctx context.Context, s *mcp.Server) error {
	return s.Run(ctx, &mcp.StdioTransport{})
}
