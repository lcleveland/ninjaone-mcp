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
