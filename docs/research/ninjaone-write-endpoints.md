# NinjaOne API v2 write and action endpoints, and their blast radius

Research for #3 (part of #1, blocks #7). Researched 2026-10-01.

## Sources

- **[S1] NinjaOne OpenAPI spec**, `https://app.ninjarmm.com/apidocs/NinjaRMM-API-v2.json`, `info.version` = `2.0.9-draft`. This is the primary source for every endpoint, method, body and response below. 164 non-GET operations. (The interactive docs at `https://app.ninjarmm.com/apidocs-beta/` are built from the same spec.)
- **[S2] NinjaOne docs, "API OAuth Token Configuration"**, https://www.ninjaone.com/docs/application-programming-interface-api/oauth-token-configuration/ (scope definitions).
- **[S3] homotechsual/NinjaOne PowerShell module**, https://github.com/homotechsual/NinjaOne (community. Used only for its `ConfirmImpact` ratings and how it handles responses.)
- **[S4] tiredithumans/ninjaone-patch-toolkit PR #168**, https://github.com/tiredithumans/ninjaone-patch-toolkit/pull/168 (community. Shows how it correlates dispatches with `/activities`.)

Claims marked **(inference)** are my own reasoning from the HTTP semantics or field shapes, and are not documented. Check them against a sandbox tenant before relying on them.

## Key findings

1. **Scopes are coarse and not given per operation.** [S2] defines three scopes: *Monitoring* "Grants read-only access to monitoring data and organization structure"; *Management* "Allows modification of device and organization information, including creating new organizations, adding new devices, running scripts, and others"; *Control* "Activates remote access via API". The spec [S1] sets a single global `security` requirement of `["monitoring","management","control"]`. Only 4 operations set their own, all `management`: device decommission, `PATCH /v2/device/{id}`, set owner and remove owner. **So every write and action below needs `management`. None needs `control`**, because none of the documented endpoints starts a remote session. The scope cannot tell a ticket comment apart from a forced reboot or a script run as SYSTEM, so the scope alone gives no useful least-privilege. The MCP server's capability flags are the only fine-grained gate. An API client can also be given just `monitoring`, which makes it read-only.
2. **Device actions are fire-and-forget.** Reboot, service control, script run and patch scan or apply all return `default`/`204` with no response schema [S1] (for example, OS patch apply is "Submit a job to start a device OS patch apply", `204 No Content` or `400 Device is not applicable`). [S3] treats script run as a 204 with no body. [S4] reports that a dispatch can return a bare `uid` that does not reliably match any activity. **Do not rely on getting a job id back.** To track an action, poll `GET /v2/device/{id}/activities` (filters: `seriesUid`, `activityType`, `status`, `newerThan`) or `GET /v2/device/{id}/jobs` / `GET /v2/jobs` (active jobs) [S1]. The backup integrity check is the one action that returns a `jobUid` [S1].
3. **No action endpoint takes an idempotency key** [S1]. Retrying an action POST after a timeout can run the script twice or reboot twice. PATCH or PUT value writes are naturally idempotent (setting the same value twice gives the same state). Ticket update needs `version` (optimistic concurrency) [S1], which makes retries safe.
4. **The highest-risk operations are not the obvious ones.** These are org- or tenant-wide changes: `PUT /v2/organization/{id}/policies` ("Returns list of affected device IDs" [S1]), `PATCH /v2/device/{id}` with `policyId`/`organizationId` (moves a device or changes its policy), `PUT /v2/webhook` (redirects all activity notifications to any URL, with custom headers), custom field *definition* bulk delete, technician create and role-permission edits (privilege escalation), and `POST /v2/organization/generate-installer` (returns a URL that enrolls agents into an org).

## Endpoint inventory

Scope: **management for all rows** (see finding 1). Mode: **async** means the API queues work on the agent and returns before it finishes, so poll activities or jobs. **sync** means the API applies the record change during the request. Idempotency is **(inference)** unless a source is cited.

### Device actions (agent executes something on a live machine)

| Endpoint | What it does | Mode | Idempotent | Blast radius |
|---|---|---|---|---|
| `POST /v2/device/{id}/reboot/{mode}` (`NORMAL`\|`FORCED`, body `reason`) | Restart | async, no job id | No: each call reboots again | **High.** Outage. `FORCED` loses unsaved user work. Rebooting a server or DC in bulk takes down a site. |
| `POST /v2/device/{id}/windows-service/{serviceId}/control` (`START`/`PAUSE`/`STOP`/`RESTART`) | Service control | async | START/STOP converge. RESTART does not. | **High** for core services (stopping the NinjaOne agent, AV, or DNS can cut the device off). |
| `POST /v2/device/{id}/windows-service/{serviceId}/configure` (`startType`, `userName`) | Change startup type or account | async | Yes (sets state) | **High**, and it persists. `DISABLED` survives a reboot, and changing the service account can break the service. |
| `POST /v2/device/{id}/script/run` (`type` SCRIPT\|ACTION, `id`/`uid`, `parameters`, `runAs`) | Run a library script or built-in action | async, response undocumented [S1][S3][S4] | No | **Critical.** This is code execution on the endpoint, often as SYSTEM or root (`runAs`). The real risk depends on what the script does, which the server cannot see. `GET /v2/device/{id}/scripting/options` lists what can run. |
| `POST /v2/device/{id}/patch/os/scan`, `.../patch/software/scan` | Trigger a patch scan | async, 204 | Yes in practice | **Low.** Read-like, but it uses agent CPU and network. |
| `POST /v2/device/{id}/patch/os/apply`, `.../patch/software/apply` | Install the patches the policy has approved | async, 204 / 400 not applicable | Mostly (later calls find nothing new to install) | **High.** Can reboot depending on policy. A bad patch rolled out in bulk causes an outage across many devices. Note: there is **no patch approve/reject endpoint** in v2 [S1]. Approval is set in policy in the UI. |
| `PUT /v2/device/{id}/maintenance` (`disabledFeatures` ALERTS/PATCHING/AVSCANS/TASKS, `start`, `end`, `reasonMessage`) | Schedule maintenance mode | sync | Yes (replaces the window) | **Medium.** Silently turns off alerting, patching or AV for the window. A long window across many devices blinds monitoring. |
| `DELETE /v2/device/{id}/maintenance` | Cancel maintenance | sync | Yes | Low |
| `DELETE /v2/device/{id}/policy/overrides` | "Submit request to remove device policy overrides" | async | Yes | **Medium.** Policy settings set per device are lost and cannot be undone through the API. |
| `POST /v2/alert/{uid}/reset`, `DELETE /v2/alert/{uid}` | Reset an alert or condition | sync | Yes | **Low to medium.** Hides an active problem. If done in bulk it is noise suppression. |
| `POST /v2/backup/integrity-check-jobs` (`deviceId`, `planUid`) | Start a backup integrity check | async, **returns `jobUid`** | No (new job each time) | Low. Uses I/O. |
| `POST /v2/backup/bandwidth-throttle` | Set backup throttle on a device | sync | Yes | Low to medium. Can stall backups. |

Backup has no restore, delete or plan-edit endpoints in v2 [S1].

### Custom field values (data on existing entities)

| Endpoint | Mode | Idempotent | Blast radius |
|---|---|---|---|
| `PATCH /v2/device/{id}/custom-fields` (204) | sync | Yes | **Medium.** Policy conditions and scripts often key on custom fields, so a write can trigger automation (inference). |
| `PATCH /v2/organization/{id}/custom-fields` | sync | Yes | Medium, applies org-wide |
| `PATCH /v2/organization/{id}/location/{locationId}/custom-fields` | sync | Yes | Medium |
| `PATCH /v2/user/end-user/{id}/custom-fields` | sync | Yes | Low to medium |
| `PATCH /v2/system/custom-fields` (global values) | sync | Yes | **High.** Tenant-wide. |

### Custom field definitions and tenant schema (admin)

`POST/PUT/DELETE /v2/custom-fields/bulk` ("All operations succeed or all fail" [S1]), `POST /v2/custom-fields`, `PUT/DELETE /v2/custom-fields/field-name/{fieldName}`, custom tabs (`/v2/tab*`), node roles (`/v2/noderole*`), policies (`POST /v2/policies`), policy conditions (`/v2/policies/{policy_id}/condition/*`). All sync. **Delete is Critical**: deleting a definition takes its values with it on every entity (inference). [S3] rates bulk custom-field and delete `ConfirmImpact='High'`.

### Org, location and device records

| Endpoint | Mode | Idempotent | Blast radius |
|---|---|---|---|
| `POST /v2/organizations`, `POST /v2/organization/{id}/locations` | sync | **No** (duplicates) | Low |
| `PATCH /v2/organization/{id}` (incl. `nodeApprovalMode` AUTOMATIC/MANUAL/REJECT) | sync | Yes | Medium. `AUTOMATIC` lets any installer enroll devices. |
| `PATCH /v2/organization/{id}/locations/{locationId}` | sync | Yes | Low |
| `PUT /v2/organization/{id}/policies` (node role to policy) | sync, returns affected device ids | Yes | **Critical.** Changes the policy (patching, monitoring, automation) for every device of that role in the org. |
| `PATCH /v2/device/{id}` (`displayName`, `userData`, `nodeRoleId`, `policyId`, `organizationId`, `locationId`, `warranty`) | sync | Yes | Low for names. **High** for `policyId`/`organizationId` (moves the device between customers or policies). |
| `POST /v2/device/{id}/owner/{ownerUid}`, `DELETE /v2/device/{id}/owner` | sync | Yes | Low |
| `POST /v2/devices/approval/{mode}` (APPROVE/REJECT, list of devices) | sync | Yes | Medium. Approving lets in unknown agents. Rejecting is bulk by design. |
| `POST /v2/device/{id}/decommission` | sync (200) | Yes | **Critical.** The device stops being managed. Undoing it means reinstalling the agent (inference). [S3] `High`. |
| `POST /v2/organization/generate-installer` | sync, returns installer URL | No | **High (security).** The URL enrolls agents into the org. Treat it as a secret. |
| `POST /v2/staged-device`, `/v2/itam/unmanaged-device*` (create/update/delete/decommission) | sync | Create: no | Low to medium (ITAM records only) |

### Tickets

`POST /v2/ticketing/ticket` (create, **not idempotent**), `PUT /v2/ticketing/ticket/{ticketId}` (needs `version`, which acts as an optimistic lock [S1]), `POST /v2/ticketing/ticket/{ticketId}/comment` (**not idempotent**, and can email the requester (inference)). All sync. Blast radius **low to medium**: mostly noise, but public comments reach customers. Note that `POST /v2/ticketing/trigger/board/{boardId}/run` is a **read** that uses POST ("Returns list of tickets matching the board"), so classify it as read-only.

### Documentation and knowledge base

Org documents (`/v2/organization/document*`, `/v2/organization/documents*`), document templates (`/v2/document-templates*`), KB articles and folders (`/v2/knowledgebase/*`), checklists and checklist templates (`/v2/checklist*`, `/v2/organization/checklist*`), related items (`/v2/related-items/*`, including `.../secure`, which creates a secure relation), attachments (`POST /v2/attachments/temp/upload`). All sync. Most follow an archive, restore, delete lifecycle, so archive can be undone and delete is limited to archived items (for example, "Delete an archived organization document" [S1]). Blast radius **low to medium**. Bulk overwrite of documents is the main risk, and PATCH has no version field.

### Tags, ITAM, licenses

Asset tags (`/v2/tag*`: create, update, delete, merge, batch-tag, set-tags), asset relationships, software licenses (including `upsert`). Sync. **Low.** `POST /v2/tag/merge` and `PUT /v2/tag/{assetType}/{assetId}` (replaces the full tag set) cannot be undone (inference).

### Users, roles, contacts (identity and access)

Technicians (`POST /v2/user/technicians`, `PATCH/DELETE /v2/user/technician/{id}`), end users (incl. `PATCH .../device-access`), role membership (`PATCH /v2/user/role/{roleId}/add-members|remove-members`), role org permissions (`PATCH /v2/user/role/{roleId}/permissions/organizations`), contacts (`/v2/contact*`). Sync. **Critical (security)**: privilege escalation or lockout. An MCP client should not be able to do this.

### Billing

Accounts, agreements, invoices (create, approve, archive, export), products, ticket products, time entries (`/v2/billing/*`). Sync. **Medium to high (financial)**: invoice approval reaches customers. [S3] rates billing deletes `High`.

### Integrations

`PUT /v2/webhook`, `DELETE /v2/webhook`. Sync. **Critical (security)**: sends all tenant activity, including any custom `headers`, to any URL, or quietly breaks existing integrations. `POST /v2/vulnerability/scan-groups/{id}/upload` (CSV import): medium.

## Candidate capability grouping (suggestion only)

*This is a proposal for #7. It is not a decision.* The aim is that each flag is something an operator can reason about as one risk ("can the client do X?"), and that the most dangerous classes need their own explicit opt-in. Read-only remains the default. POST endpoints that are really reads (board run) stay in read-only.

| Capability (suggested) | Covers | Why grouped |
|---|---|---|
| `tickets` | ticket create, update, comment | Low risk, everyday PSA use |
| `documentation` | org docs, templates, KB, checklists, related items, attachments, tags | Content only, archive/restore exists |
| `custom-fields` | **value** PATCHes on device, org, location, end-user (not global) | Data writes, idempotent, but can trigger automation |
| `device-maintenance` | maintenance schedule/cancel, alert reset, patch **scan** | Changes monitoring and alerting, no code runs |
| `device-actions` | reboot, service control/configure, patch **apply**, policy-override reset, backup integrity check/throttle | Disruptive to a live machine, but bounded to known operations |
| `scripts` | `script/run` (scripts and built-in actions) | Arbitrary code as SYSTEM. Must never be bundled with anything else. |
| `device-admin` | device PATCH, owner, approval, decommission, staged/unmanaged devices, ITAM/licenses, org/location create/update | Changes inventory and management |
| *not exposed (recommend)* | custom-field definitions and global values, policies and policy mappings, node roles, tabs, users/roles/contacts, webhook, generate-installer, billing | Tenant admin, security or financial. Leave to the console unless a real need comes up. |

Server-side guards to consider on top of the flags (also suggestions): accept only one device id per action call (no fan-out tool); require a `reason` on reboot and maintenance (the API accepts one); return a pointer to `/v2/device/{id}/activities` instead of claiming success; never retry action POSTs automatically.

## Open questions (need a live tenant)

- What does `script/run` actually return (204 with no body, or a `uid`), and does that uid match `seriesUid` in activities? [S3] and [S4] disagree.
- Does an API client with only `management` and no `monitoring` scope also get reads? Does any endpoint actually enforce `control`?
- Does a technician's role restrict an API client (client credentials grant) at all, or does the client get the whole tenant?
