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

**Web app**:
The second NinjaOne application, platform Web, through which a user signs in so the server can act as them. Distinct from the API client.
_Avoid_: app (alone), OAuth app

**User context**:
Writes sent with a signed-in user's token instead of the API client's, because NinjaOne accepts them only from a user (script runs, ticket writes); audited in NinjaOne as that user.
_Avoid_: impersonation, user mode

**Sign-in**:
The one-time browser consent (`ninjaone_status` action authorize, then `/oauth/callback`) that gives the server a user's rotating refresh token.
_Avoid_: login, auth (alone)

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

**Dispatch**:
An action NinjaOne has accepted and queued to a device's agent but not yet confirmed as done; its outcome shows up later as activities.
_Avoid_: job, run, success
