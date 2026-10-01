# NinjaOne public API v2: facts for the Go MCP client

Research for issue #2. Gathered 2026-10-01.

**Sources, in order of trust:**

1. **Primary.** The live OpenAPI spec that each instance serves at `https://<host>/apidocs/NinjaRMM-API-v2.json`. It is OpenAPI 3.0.1, `info.version` `2.0.9-draft`, 254 paths. Fetched from <https://app.ninjarmm.com/apidocs/NinjaRMM-API-v2.json> and the other hosts.
2. **Primary.** The docs articles behind the beta reference UI. The SPA loads them as Markdown from `https://app.ninjarmm.com/apidocs-beta/articles/<slug>.md`:
   - [authorization/overview](https://app.ninjarmm.com/apidocs-beta/authorization/overview)
   - [client-credentials-flow](https://app.ninjarmm.com/apidocs-beta/authorization/flows/client-credentials-flow)
   - [refresh-tokens](https://app.ninjarmm.com/apidocs-beta/authorization/refresh-tokens)
   - [machine-to-machine-apps](https://app.ninjarmm.com/apidocs-beta/authorization/create-applications/machine-to-machine-apps)
   - [device-filters](https://app.ninjarmm.com/apidocs-beta/core-resources/articles/devices/device-filters)
   - [custom-fields-values](https://app.ninjarmm.com/apidocs-beta/core-resources/articles/customFields/customFieldsValues/custom-fields-values)
   - [webhooks/overview](https://app.ninjarmm.com/apidocs-beta/core-resources/articles/webhooks/overview)
3. **Probes.** Unauthenticated requests I made against `us2.ninjarmm.com` on 2026-10-01. I used no credentials, so I only observed error shapes.
4. **Community.** The [homotechsual/NinjaOne](https://github.com/homotechsual/NinjaOne) PowerShell module, which is the most mature client. Also [Lungshot/NinjaOneMCP](https://github.com/Lungshot/NinjaOneMCP). The tables mark claims that come only from these sources as **[community]**.

## 1. Hosts and regions

API base is `https://<host>`, and every resource path starts with `/v2/...`. The OAuth endpoints live under `/ws` (see §2). The spec has **no `servers` block**, so the client has to supply the host itself.

| Key | Host | Evidence |
|---|---|---|
| `app` / `us` | `https://app.ninjarmm.com` | Webhook doc hostname table (NA); spec host |
| `us2` | `https://us2.ninjarmm.com` | Our tenant; spec served; probed |
| `eu` | `https://eu.ninjarmm.com` | Webhook doc table (EMEA) |
| `ca` | `https://ca.ninjarmm.com` | Spec served; homotechsual instance map |
| `oc` | `https://oc.ninjarmm.com` | Webhook doc table (APAC) |
| `fed` | `https://fed.ninjarmm.com` | FedRAMP. Spec served, but with 250 paths, and its app version lags. **[community]** drift report |

Notes:

- `jp.ninjarmm.com`, `api.ninjarmm.com` and `app.ninjaone.com` all served an identical spec on 2026-10-01. None of them is documented as an API host.
- NinjaOne's own docs list only app, eu and oc, in the webhook doc. Every other host in the table comes from probes or the community.
- Recommendation: take either a region key (`app|us|us2|eu|ca|oc|fed`) **or** a full base URL as an override. A tenant only exists on one host, so a wrong host fails authentication (see §2 quirk: it fails with a 404).
- **Instances drift.** The [homotechsual drift report](https://github.com/homotechsual/NinjaOne/blob/main/docs/NinjaOne/development/instance-endpoint-drift.mdx) (2026-04-28) found:
  - app, eu and oc were on app version 13.0.20.
  - ca and us2 were on 13.0.10.
  - fed was on 11.0.70 and lacked about 100 endpoints. Among them: `/v2/custom-fields`, `/v2/user/*`, `/v2/billing/*`, `/v2/tab/*` and `/v2/noderole/list`.
  - An MCP tool should therefore treat a 404 on a valid-looking path as "not on this instance", not as a bug.

## 2. Auth: OAuth2 client credentials

Source: the client-credentials-flow and refresh-tokens articles.

**Token endpoint:** `POST https://<host>/ws/oauth/token` (the docs give `baseUrl: "/ws"` and `url: "/oauth/token"`).

- Content type is `application/x-www-form-urlencoded`.
- Form fields:
  - `grant_type=client_credentials`
  - `client_id`
  - `client_secret`
  - `scope`: a space-separated list. If you omit it, you get **all scopes configured on the app**.
- Response body:

  ```json
  {"access_token":"...","expires_in":3600,"scope":"monitoring","token_type":"Bearer"}
  ```
- Use it as `Authorization: Bearer <token>`.

**Lifetime and refresh:**

- `expires_in` is 3600 s in every doc example. Read the actual value; do not hardcode 3600.
- Client credentials returns **no refresh token**. A refresh token needs the `offline_access` scope through `/ws/oauth/authorize`, which only exists in the authorization-code flow.
- So the client should re-POST client credentials shortly before `expires_in` runs out (for example 60 s early) and also once after any 401.
- `golang.org/x/oauth2/clientcredentials` does all of this. Set `TokenURL` to `<host>/ws/oauth/token`, set `AuthStyle: oauth2.AuthStyleInParams` (the docs show credentials in the body), and pass `Scopes`.

**Scopes.** You choose them per app under Administration > Apps > API > Client App IDs, using the "API Services (machine-to-machine)" platform.

| Scope | Grants |
|---|---|
| `monitoring` | Read-only access to monitoring data and organization structure |
| `management` | Changes to devices and orgs: creating orgs, adding devices, running scripts, and so on |
| `control` | Remote access through the API |

- A read-only MCP needs only `monitoring`.
- The spec declares `security: [{oauth2: [monitoring, management, control]}]` globally. Only one operation states its own scope (`GET /v2/device/{id}/last-logged-on-user` requires `monitoring`). In short, **per-endpoint scope requirements are not documented**.
- Only system administrators can create API apps ([NinjaOne docs](https://www.ninjaone.com/docs/application-programming-interface-api/oauth-token-configuration/)).
- **[community]** Some endpoints return `403 user_context_required` to a client-credentials token. Those reported are ticket writes and running device scripts. Reads work with client credentials alone, per [Lungshot/NinjaOneMCP](https://github.com/Lungshot/NinjaOneMCP).

**Auth error quirks.** All of these were probed against us2:

| Request | Status | Body |
|---|---|---|
| Unknown `client_id` at `/ws/oauth/token` | **404**, not 401 | `{"resultCode":"Client app not exist","incidentId":"WEB_MGMT_SERVICE-..."}` |
| No `Authorization` header | **400** | `{"error":"missing_header","error_description":"Missing required header","error_code":2}` |
| Malformed bearer value | **400** | `{"error":"invalid_header","error_description":"Invalid 'Authorization' header","error_code":1}` |
| Well-formed but invalid or expired token | 401 | `{"error":"not_authenticated","error_description":"Invalid credentials","error_code":10}` |

Two consequences for the client:

- A 404 from the token endpoint usually means **wrong region**. Report it that way.
- Re-authenticate on 401 only. A 400 from the auth layer is a client bug and retrying will not help.

## 3. Response and error shapes

- **Success bodies are bare JSON.** There is no envelope: list endpoints return a JSON array, and detail endpoints return an object. The exceptions are the paginated families in §4.
- **Spec quality.** 116 operations document only a `default` response, with no `200`. Schemas are inlined rather than defined as `$ref` components. Code generators produce poor Go from this spec, so hand-write the structs you need, or decode into `map[string]any`.
- **Errors come in three families.** Decode errors leniently into a struct that has all of these fields:
  - Auth layer: `{error, error_description, error_code}` (see §2).
  - App layer: `{resultCode, errorMessage?, errorDetails?, incidentId}`. `resultCode` is either a snake_case code or `"FAILURE"`. Surface `incidentId` to the user, because NinjaOne support asks for it.
  - Billing/PSA endpoints: `{errorCode, message}`.
- **Status codes do not always mean what they say.** Some billing endpoints document **500** for a permission failure (`permission_denied`, `organization_access_violation`). Treat a 500 that carries a known `resultCode` as a 403.
- **Timestamps are epoch *seconds* as a JSON number with a fraction** (`format: double`). Fields include `created`, `lastContact`, `lastUpdate`, `activityTime`, alert `createTime`, and `cursor.expires`. Decode them as `float64`.
  - Custom-field `DATE` values are **epoch milliseconds** (custom-fields-values article).
  - Query parameters vary: activities `before`/`after` take date strings, `startTime`/`endTime` take ISO-8601, geolocation takes epoch seconds, and billing takes `yyyy-MM-dd`.
- **IDs are `int32`.** Device `uid` and org/location UIDs are UUID strings.
- **Localization.** `/v2/alerts`, `/v2/activities` and `/v2/jobs` take `lang` and `tz`, which localize message text.
- **Response headers.** The probed responses included `x-nj-request-id`, which is worth logging. The server header was `Grizzly`.

## 4. Pagination: there is no single model

| Family | Request | Response | Next page |
|---|---|---|---|
| Entity lists: `/v2/devices`, `/v2/devices-detailed`, `/v2/organizations`, `/v2/organizations-detailed`, `/v2/locations`, `/v2/organization/{id}/devices` | `pageSize`, `after` (int: **last ID from the previous page**) | Bare array | `after = last.id`. Stop when the page is shorter than `pageSize` or empty (inferred; not documented) |
| Query reports: `/v2/queries/*`, `/v2/backup/jobs`, `/v2/backup/integrity-check-jobs`, geolocation history | `cursor` (cursor **name**), `pageSize` | `{cursor:{name,offset,count,expires}, results:[...]}` | Pass `cursor=<cursor.name>`. Cursors expire (`expires` is in epoch seconds) |
| Activities: `/v2/activities`, `/v2/device/{id}/activities` | `pageSize` (10–1000, default 200), `olderThan` / `newerThan` (activity ID) | `{lastActivityId, activities:[...]}` | `olderThan = lastActivityId`. Results are in reverse chronological order. **The spec descriptions of `olderThan` and `newerThan` are swapped**; trust the parameter names |
| Custom field definitions: `/v2/custom-fields` | `cursorName`, `pageSize` (5–500, default 50) | `{page, pageSize, count, results}` | Not present on fed |
| Ticket boards: `POST /v2/ticketing/trigger/board/{boardId}/run` | JSON body `{pageSize, lastCursorId, sortBy, filters, searchCriteria, includeColumns}` | `{data:[...], metadata:{lastCursorId, columns, ...}}` | Send back `metadata.lastCursorId` |
| Ticket log entries | `anchorId`, `pageSize` (default 500), `type[]`, `createTime` | Bare array | `anchorId` |
| ITAM and software licenses | `after` / `before` / `pageSize` | Varies | — |

Notes:

- Query-report `pageSize` limits are large: up to 10000 (default 1000) on custom-fields reports, and a default of 10000 on backup jobs.
- **Many lists are not paginated at all** and can be large: `/v2/alerts`, `/v2/users`, `/v2/groups`, `/v2/policies`, `/v2/organization/{id}/locations`, `/v2/device/{id}/software`, ticketing lookups and others. An MCP tool must cap or summarize these before returning them to the model.

## 5. Rate limits

- **No published limits for v2.** The spec mentions 429 on exactly one endpoint (`/v2/device/{id}/geolocation/history`: "Rate limit exceeded").
- **[community]** The homotechsual module says: *"NinjaOne signals rate limiting by returning an HTML page (not a 429)"* ([source](https://github.com/homotechsual/NinjaOne/blob/main/Source/Public/PSModule/Invoke/Invoke-NinjaOneRequest.ps1)).
  - The module retries only GET requests whose body is HTML and matches `rate limit|too many requests|request limit exceeded`.
  - It retries up to 5 times with exponential backoff starting at 2 s.
- **Client rule:** treat these as rate limited and retry GETs with backoff, honoring `Retry-After` if present:
  - a 429;
  - any non-JSON (HTML) body on an API path.
- Never auto-retry a mutation.
- The legacy v0 API (v0.1.2 PDF) allowed about 10 list calls per 10 minutes. That limit is historical and does not apply to v2.

## 6. Device filter (`df`)

Source: the device-filters article. The older [v2.0.5 Device Filter Syntax PDF](https://resources.ninjarmm.com/API/Ninja+RMM+Public+API+v2.0.5+Device+Filter+Syntax.pdf) is linked from the spec's `info`.

| Key | Operators | Value |
|---|---|---|
| `org` / `organization` | `=`/`eq`, `!=`/`neq`/`<>`, `in`, `nin`/`notin`/`!in` | int, or `(1,2)` |
| `loc` / `location` | same | int, or list |
| `role` | same | int, or list |
| `id` | same | int, or list |
| `class` | same | `nodeClass` enum (`WINDOWS_SERVER`, `MAC`, `NMS_SWITCH`, ...), or list |
| `status` | `=`, `!=` | `PENDING` or `APPROVED`. The **default is `APPROVED`**, so pending devices are hidden unless you ask for them |
| `online` / `offline` | Bare keyword | — |
| `created` | `=`/`eq`, `<`/`lt`/`before`, `>`/`gt`/`after` | `yyyyMMdd`, `yyyy-MM-dd`, or `yyyy-MM-dd'T'HH:mm:ss.SSS'Z'` |
| `group` | No operator: `group 563` | Saved-search ID |

Syntax rules:

- Combine clauses with `AND`, `and` or `&&`.
- **OR is not supported.** Use `in (...)` for alternatives within one key, or make multiple calls.
- The value must be URL-encoded. The docs' examples encode spaces as `%20`. Go's `url.Values.Encode` emits `+`, and whether the server accepts `+` is **unverified**. Until a live test settles it, use `strings.ReplaceAll(url.QueryEscape(df), "+", "%20")`.
- The doc has typos, so do not copy its examples blindly:
  - The `id eq` example says `role eq 1`.
  - The encoded `created` range example does not match its raw form.
  - The `status` encoded example is malformed.

`df` is accepted by:

- `/v2/devices` and `/v2/devices-detailed`
- `/v2/alerts`, `/v2/activities` and `/v2/jobs`
- most `/v2/queries/*` endpoints
- `/v2/backup/jobs`

Other filters:

| Filter | Used on | Values |
|---|---|---|
| `of` | `/v2/organizations` | Organization filter. **Its syntax is undocumented** |
| `ts` | Several `/v2/queries/*` endpoints | Monitoring-timestamp filter. **Its syntax is undocumented** |
| `sf` (status) | `/v2/backup/jobs` | e.g. `status = RUNNING` or `status in (RUNNING,PROCESSING)`; values PROCESSING, RUNNING, COMPLETED, CANCELED, FAILED **[community]** |
| `ptf` (plan type) | `/v2/backup/jobs` | `planType in (IMAGE,FILE_FOLDER)` **[community]** |
| `stf` (start time) | `/v2/backup/jobs` | `startTime after <ISO UTC>` or `startTime between (<a>,<b>)` **[community]** |
| `ddf` | `/v2/backup/jobs` | Deleted-device filter |
| `include` | `/v2/backup/jobs` | `active` (default), `deleted` or `all` |

Source for the community filter syntax: [homotechsual filters docs](https://github.com/homotechsual/NinjaOne/tree/main/docs/NinjaOne/filters).

`GET /v2/devices/search?q=&limit=` does free-text search by name, logged-on user or IP. It is the friendliest lookup for an LLM.

## 7. Read endpoints by area

All of these are `GET` unless marked otherwise. `{id}` is an int.

**Organizations and locations**

- Lists, paged by `after`:
  - `/v2/organizations` (supports `of`) and `/v2/organizations-detailed`
  - `/v2/locations` (all orgs)
- Single organization:
  - `/v2/organization/{id}`
  - `/v2/organization/{id}/locations` (not paged)
  - `/v2/organization/{id}/devices` (paged)
  - `/v2/organization/{id}/end-users`
- Policies, roles and groups:
  - `/v2/policies`
  - `/v2/roles`: device roles, which is what `df role=` refers to
  - `/v2/groups` and `/v2/group/{id}/device-ids`: saved searches, which is what `df group` refers to

**Devices**

- List, search and detail: `/v2/devices`, `/v2/devices-detailed`, `/v2/devices/search`, `/v2/device/{id}`.
- **Spec gap.** The `/v2/device/{id}` schema lists only the base node fields plus `ipAddresses`, `macAddresses`, `publicIP`, `notes` and `deviceType`. Its OS and hardware data are not described. Check what the live response contains before relying on it.
- Hardware, one device:
  - `/v2/device/{id}/processors`
  - `/disks`
  - `/volumes` (`include` param)
  - `/network-interfaces`
- Hardware, fleet-wide via `/v2/queries/*` (`df` + cursor):
  - `computer-systems` (make, model, serial)
  - `processors`, `disks`, `volumes`
  - `raid-controllers`, `raid-drives`
  - `network-interfaces`
- OS: `/v2/queries/operating-systems`. There is no per-device OS endpoint, so filter the query with `df=id=N`.
- Software:
  - Per device: `/v2/device/{id}/software`
  - Fleet: `/v2/queries/software` (`installedBefore`/`installedAfter`)
  - Supported third-party catalog: `/v2/software-products`
- Other per-device reads:
  - `/v2/device/{id}/last-logged-on-user` and `/v2/queries/logged-on-users`
  - `/v2/device/{id}/windows-services` and `/v2/queries/windows-services`
  - `/v2/device/{id}/jobs`
  - `/v2/device/{id}/policy/overrides`
  - `/v2/queries/device-health` (`health` filter)
  - `/v2/queries/antivirus-status` and `/v2/queries/antivirus-threats`
- `/v2/device/{id}/dashboard-url` builds a deep link into the console, useful in tool output. It is tagged `management`, so it may need that scope.

**Alerts and activities**

- `/v2/alerts`: active triggered conditions; `sourceType` enum, `df`; not paged.
- `/v2/device/{id}/alerts`
- `/v2/activities`:
  - `class`: SYSTEM, DEVICE, USER or ALL
  - `type` enum: ACTIONSET, CONDITION, PATCH_MANAGEMENT, ...
  - `status`, `user`, `seriesUid` (ties activities to an alert), `df`, `sourceConfigUid`
- `/v2/device/{id}/activities`

**Patching**

| Scope | Pending, failed or rejected patches | Install history |
|---|---|---|
| Per device | `/v2/device/{id}/os-patches`, `/v2/device/{id}/software-patches` | `/v2/device/{id}/os-patch-installs`, `/v2/device/{id}/software-patch-installs` |
| Fleet | `/v2/queries/os-patches`, `/v2/queries/software-patches` | `/v2/queries/os-patch-installs`, `/v2/queries/software-patch-installs` |

Filters: `status`, `type`, `severity` (OS), `impact`/`productIdentifier` (software), and `installedBefore`/`installedAfter`.

**Custom fields**

- Values per entity:
  - `/v2/device/{id}/custom-fields`: returns a **map keyed by field name**; `withInheritance`
  - `/v2/organization/{id}/custom-fields`
  - `/v2/organization/{id}/location/{locationId}/custom-fields`
  - `/v2/user/end-user/{id}/custom-fields`
  - `/v2/system/custom-fields` (global)
- Fleet values: `/v2/queries/custom-fields`, `custom-fields-detailed`, `scoped-custom-fields` and `scoped-custom-fields-detailed`. They take `fields` (CSV), `updatedAfter` and `scopes`.
- Definitions:
  - `/v2/device-custom-fields`
  - `/v2/custom-fields` (paged) and `/v2/custom-fields/field-name/{fieldName}`
- **Never send `showSecureValues=true`.** It returns secure fields (for example `TEXT_ENCRYPTED`) as plaintext. The MCP should never set it.
- Value types and their encodings are listed in the custom-fields-values article. Watch for `DATE`, which is in ms, and dropdown values, which are option IDs given as strings.

**Documentation and knowledge base**

- Documents:
  - `/v2/organization/documents` (filters: `organizationIds`, `templateIds`, `templateName`, `documentName`, `groupBy`)
  - `/v2/organization/{organizationId}/documents`
  - `/v2/document-templates` and `/v2/document-templates/{id}`
- Knowledge base:
  - `/v2/knowledgebase/organization/articles` and `/v2/knowledgebase/global/articles`
  - `/v2/knowledgebase/article/{id}` and `.../download`
  - `/v2/knowledgebase/folder`
- Checklists: `/v2/checklist/templates`, `/v2/organization/checklists`.
- `/v2/related-items/...` links entities and attachments.
- Attachment bytes come from `.../signed-urls` endpoints. These return pre-signed, time-limited links, so do not cache them.

**Ticketing**

- Single ticket: `/v2/ticketing/ticket/{ticketId}` and `/v2/ticketing/ticket/{ticketId}/log-entry`.
- **There is no "list tickets" GET.** Instead:
  1. List boards with `/v2/ticketing/trigger/boards`.
  2. Run a board with `POST /v2/ticketing/trigger/board/{boardId}/run`.
- A read-only MCP has to allow that one POST.
- Lookups: `/v2/ticketing/statuses`, `/attributes`, `/ticket-form`, `/ticket-form/{id}`, `/contact/contacts`, `/app-user-contact`.

**Backup**

- `/v2/backup/jobs` and `/v2/backup/integrity-check-jobs`: cursor-paged, with `df`/`ddf`/`sf`/`ptf`/`stf`/`include`.
- `/v2/queries/backup/usage` (`includeDeletedDevices`)
- `/v2/organization/{id}/locations/backup/usage` and `/v2/organization/{id}/locations/{locationId}/backup/usage`

**Users**

- `/v2/users` (`userType`, `includeRoles`)
- `/v2/user/technicians` and `/v2/user/end-users`
- `/v2/user/roles`
- `/v2/contacts`

**Also present** (out of scope for this ticket): billing (`/v2/billing/*`), vulnerability scan groups, ITAM, software licenses, asset tags, custom tabs, webhooks and ServiceNow.

## 8. Quirks that will bite an MCP tool (checklist)

1. **Region.** The client must be told the host. A wrong host gives **404 "Client app not exist"** at the token endpoint. fed has fewer endpoints, and instances run different app versions.
2. **Token.** It lasts about 1 h and there is no refresh token. Re-request on expiry or on a 401.
3. **Auth errors.** A missing or garbled header returns **400**, not 401. Errors come in three JSON shapes, so decode leniently and surface `incidentId`.
4. **Rate limiting** can show up as an **HTML page with status 200 or another non-429 code** **[community]**. Check `Content-Type` before decoding JSON, and back off on GET only.
5. **Pagination.** There are at least five schemes. Some large lists have no pagination at all. Always cap output size.
6. **`df`.** It supports AND but not OR. The default status is `APPROVED`, so pending devices are hidden. Watch the `%20` versus `+` encoding.
7. **Times.** Response timestamps are epoch seconds as floats. Custom-field dates are epoch ms. Query parameters use mixed date formats.
8. **Ticket lists** require a POST that runs a board.
9. **`showSecureValues`** leaks secrets. Never send it.
10. **Signed URLs** expire.
11. **Write scopes.** Some writes need user context and fail with `403 user_context_required` for a client-credentials token **[community]**. Reads are fine with a `monitoring`-only client-credentials app.
12. **Spec quality.** The spec has mostly `default` responses, inline schemas and some swapped descriptions (`olderThan`/`newerThan`). Hand-write the structs; do not codegen.

## Open questions (need a live tenant)

- Whether `df` accepts `+` for spaces.
- What the real 429 or HTML rate-limit behavior is on us2, including any headers and the actual limits.
- Whether the last page of the `after`-paged lists is just a short page, or something else.
- The full live field set of `/v2/device/{id}` and `/v2/devices-detailed`, beyond what the spec lists.
- The syntax of `of` and `ts`.
