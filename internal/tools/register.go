// Package tools defines the MCP tools and registers the enabled ones.
package tools

import (
	"log/slog"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/lcleveland/ninjaone-mcp/internal/config"
	"github.com/lcleveland/ninjaone-mcp/internal/ninjaone"
)

type Deps struct {
	Client *ninjaone.Client
	Config *config.Config
	Log    *slog.Logger
}

// Register adds every enabled tool and returns how many it added.
func Register(s *mcp.Server, d Deps) int {
	if d.Log == nil {
		d.Log = slog.New(slog.DiscardHandler)
	}
	registerStatus(s, d)
	must("ninjaone_api", registerGeneric(s, d))
	n := 2
	for _, t := range Tools() {
		if !d.Config.GroupOn(t.Group) || len(d.views(t)) == 0 {
			continue
		}
		must(t.Name, registerTool(s, d, t))
		n++
	}
	return n
}

// must panics on a schema error: the tool set is static, so only a
// programming error lands here, and it fails every test.
func must(name string, err error) {
	if err != nil {
		panic(name + ": " + err.Error())
	}
}
