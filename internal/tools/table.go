package tools

// Tool is one first-class MCP tool: a set of actions over NinjaOne endpoints.
// Adding an endpoint is adding a View; there is no per-endpoint handler code.
type Tool struct {
	Name        string // ninjaone_<thing>
	Group       string
	Title       string
	Description string // what it is; per-action help is appended
	Views       []View
}

// View is one action of a tool.
type View struct {
	Action string
	Help   string // one line: what this action does and its useful query params
	Method string // default GET
	// Path may hold {placeholders}: {id} comes from the id input, any other
	// from the query input (removed from the query once used).
	Path string

	// Reads.
	Paging     Paging
	Items      string   // key holding the list in an envelope response, e.g. "results"
	Single     bool     // the reply is one object: returned in full, never projected
	Brief      []string // fields a list keeps unless fields is given; nil keeps all
	DF         bool     // accepts a device filter
	LimitParam string   // unpaged endpoints with their own size param (devices/search)
	DeepLink   bool     // add the console URL from /v2/device/{id}/dashboard-url

	// Writes. A view with a Capability is a write, or (Method POST, no
	// Capability) a read that NinjaOne happens to serve over POST.
	Capability  string
	Body        bool     // takes the body input
	Multipart   string   // send body as this multipart JSON part instead of JSON
	ReasonField string   // also forward reason into this body field
	Require     []string // body keys that must be present
	Bulk        bool     // body may be an array, capped at --max-bulk
	Device      bool     // acts on one live device: list ids rejected
	Confirm     bool     // confirm must equal the device's display name
	Dispatch    bool     // async on the agent: report a dispatch, not success
	Destructive bool
}

func (v View) method() string {
	if v.Method == "" {
		return "GET"
	}
	return v.Method
}

func (v View) write() bool { return v.Capability != "" }

// Paging is how a NinjaOne list endpoint pages. The model never sees it:
// every list takes cursor and returns next_cursor.
type Paging int

const (
	NoPaging Paging = iota
	After           // pageSize + after=<last id>; bare array
	Cursor          // pageSize + cursor=<cursor.name>; {cursor, results}
	Activity        // pageSize + olderThan=<lastActivityId>; {activities, lastActivityId}
	Board           // POST {pageSize, lastCursorId}; {data, metadata.lastCursorId}
	Anchor          // pageSize + anchorId=<last id>; bare array
)

// Tools is the whole first-class tool table, in registration order.
func Tools() []Tool {
	var all []Tool
	for _, t := range [][]Tool{inventoryTools, readTools} {
		all = append(all, t...)
	}
	return all
}
