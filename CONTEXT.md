# NinjaOne MCP

An MCP server that lets an MCP client read and, when permitted, act on one NinjaOne tenant.

## Language

**Tenant**:
The single NinjaOne account a server process talks to.
_Avoid_: instance, account

**Region**:
The NinjaOne hosting region that fixes the tenant's base URL (e.g. `us2`).
_Avoid_: host, datacenter

**API client**:
The NinjaOne API Services application whose client ID and secret the server authenticates as.
_Avoid_: app, service account, token

**Capability**:
A named class of write the operator opts into (e.g. device actions, scripts, custom fields); writes outside an enabled capability do not exist for the client.
_Avoid_: permission, verb flag

**Organization**:
A NinjaOne customer grouping that owns locations and devices.
_Avoid_: client, customer, company

**Device**:
A machine managed by NinjaOne.
_Avoid_: node, endpoint, agent

**Tool group**:
A named set of tools the operator can enable or disable together (e.g. inventory, patching, ticketing).
_Avoid_: module, feature, category

**Report**:
A fleet-wide NinjaOne query (`/v2/queries/*`) returning one row per device or item, filtered by a device filter.
_Avoid_: query (alone), search

**Device filter**:
NinjaOne's `df` expression selecting devices by org, location, role, class, status or group; AND only, no OR.
_Avoid_: search, query

**Board**:
A saved NinjaOne ticket view; listing tickets means running a board.
_Avoid_: queue, view
