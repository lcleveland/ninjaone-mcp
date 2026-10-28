# ninjaone-mcp

An MCP server for the [NinjaOne](https://www.ninjaone.com/) RMM public API v2, written in Go. It serves over stdio or streamable HTTP and is packaged as a Nix flake with a NixOS module.

The server is read-only by default. Writes are turned on per **capability** (tickets, scripts, device actions, ...), every write needs a reason that goes to the audit log, and tenant administration is never exposed at all.

## Tools

| Group | Tools |
|---|---|
| core (always on) | `ninjaone_status` (auth, scopes and reachability probe), `ninjaone_api` (any `/v2/` path, capability-gated) |
| inventory | `ninjaone_organization`, `ninjaone_location`, `ninjaone_device` (list, list_detailed, search, get with console link), `ninjaone_device_detail` (disks, volumes, software, services, geolocation history, ...), `ninjaone_report` (fleet-wide `/v2/queries/*`), `ninjaone_lookup` (policies, device roles, groups), `ninjaone_tag` (asset tags; set, merge, delete with `--allow-documentation`), `ninjaone_software_license` (seats and assignments; writes with `--allow-device-admin`), `ninjaone_user`, `ninjaone_script` (automation library, scheduled tasks, per-device options; run with `--allow-scripts`), and with a capability enabled `ninjaone_device_action` |
| alerts | `ninjaone_alert`, `ninjaone_activity` (activity log and running jobs) |
| patching | `ninjaone_patch` (OS and software; pending and installs; per device or fleet) |
| docs | `ninjaone_custom_field`, `ninjaone_document`, `ninjaone_kb_article` |
| ticketing | `ninjaone_ticket` (get, boards, board_run, log entries, lookups) |
| backup | `ninjaone_backup` (jobs, integrity-check jobs, usage) |

Every tool takes an `action`. Lists:
- return a brief field set unless you pass `fields`;
- cap results at 200 items and 60 KiB, adding a `_truncation` note when they have to cut;
- return `next_cursor` when there is more. Pass it back as `cursor`. It hides NinjaOne's six different paging schemes.

Timestamps come back as RFC 3339 UTC. Device filters (`df`) support AND but not OR, and hide pending devices unless you ask for `status = PENDING`.

## Write capabilities

All are off by default. A disabled capability's actions are removed from the tool schemas and also refused by the handler.

| Flag | Unlocks |
|---|---|
| `--allow-tickets` | ticket create, update (needs the ticket's `version`), comment |
| `--allow-documentation` | documents, templates, KB articles, checklists, related items, attachments, tags |
| `--allow-custom-fields` | custom field values on devices, organizations, locations, end users |
| `--allow-device-maintenance` | maintenance mode, alert reset, patch scans |
| `--allow-device-actions` | reboot, Windows service control/configure, patch install, policy-override reset, backup integrity check/throttle |
| `--allow-scripts` | `script/run`: scripts and built-in actions, often as SYSTEM |
| `--allow-device-admin` | device edit/move, owner, approve/reject, decommission, organization and location create/edit, ITAM, licenses |

**Never exposed**, whatever the flags: custom-field definitions and global values, policies and policy mappings, node roles, tabs, users/roles/contacts, webhooks, installer generation, organization archive, billing and vulnerability imports.

Why capabilities rather than create/update/delete flags: every NinjaOne write needs the same `management` OAuth scope, so neither HTTP verbs nor scopes tell a ticket comment apart from running code as SYSTEM. See [ADR 0001](docs/adr/0001-capability-flags-not-verb-flags.md).

**Write safety:**
- Every write requires `reason`. It goes to NinjaOne where the API takes one (reboot reason, maintenance message) and always to the audit log, with `x-nj-request-id`.
- Device actions and scripts take exactly one device per call. Record writes (documents, KB articles) may batch up to `--max-bulk` (50).
- Forced reboot, decommission and script run need `confirm` equal to the device's display name. A mismatch fails before anything is sent.
- Device actions return a **dispatch**: queued to the agent but not confirmed as done. The reply says how to check the device's activities for the outcome.
- Writes are never retried automatically. NinjaOne actions have no idempotency key, so a retried reboot is a second reboot.
- `ninjaone_api` maps every write route in NinjaOne's spec to a capability or to "never" (a test enforces this), and refuses routes it cannot classify.

## Configuration

| Flag | Env | Default |
|---|---|---|
| `--region` (`app`, `us`, `us2`, `eu`, `ca`, `oc`, `fed`) | `NINJAONE_REGION` | one of region/base URL required |
| `--base-url` | `NINJAONE_BASE_URL` | https only, except to loopback |
| `--client-id` | `NINJAONE_CLIENT_ID` | required, or a file below |
| `--client-id-file` | `NINJAONE_CLIENT_ID_FILE` | the systemd credential `client-id`; wins over `--client-id` |
| `--client-secret-file` | `NINJAONE_CLIENT_SECRET_FILE` | see below |
| `--scopes` | | all scopes on the app |
| `--allow-<capability>` | | all off |
| `--max-bulk` | | 50 |
| `--tool-groups` | | all |
| `--stdio` / `--http` | | stdio |
| `--addr`, `--path` | | `127.0.0.1:8233`, `/mcp` |
| `--http-auth-token-file` | `NINJAONE_MCP_HTTP_AUTH_TOKEN_FILE` | none (a non-loopback listener requires one) |
| `--request-timeout`, `--log-level` | `NINJAONE_MCP_LOG_LEVEL` | `30s`, `info` |
| `--version` | | |

**Client secret.** The server looks for it in this order:
1. `--client-secret-file`
2. `NINJAONE_CLIENT_SECRET_FILE`
3. `NINJAONE_CLIENT_SECRET` (logs a warning)
4. the systemd credential `client-secret`

There is no flag that takes the secret directly.

### Getting an API client

1. In NinjaOne, go to **Administration → Apps → API → Client App IDs → Add**. Only system administrators can do this.
2. Set **Application platform** to **API Services (machine-to-machine)**.
3. Pick the scopes:
   - `monitoring` is read-only and enough for a read-only server;
   - `management` is needed for **every** write;
   - `control` is remote access, which no tool uses.
4. Under **Allowed grant types**, choose **Client credentials**.
5. Save, then copy the client ID and secret.

`ninjaone_status` shows the scopes the token was actually granted. A 404 from the token endpoint usually means the wrong region.

## nj: running scripts as a user

`script/run` needs a signed-in user, so an API Services client cannot make it (`403 user_context_required`). `nj` (`tools/nj.py`, one stdlib-only Python file, no dependencies) runs library scripts with a user's token instead. The run is audited as that user. The same file has read helpers (`analyze`, `grep`, `find`, `get`, `scripts`, `move`) that call the REST API directly.

```
nj auth                                      once: browser consent, stores a refresh token
nj scripts                                   library scripts: id, name, language
nj run PC-0001 <scriptId> [params]           dry run, prints the request
nj run PC-0001 <scriptId> [params] --apply   dispatch as SYSTEM
nj analyze PC-0001                           one-page device report
nj grep PC-0001 bluetooth [days]             rows from every dataset matching a regex
nj find 0001 | nj get /v2/queries/disks | nj move PC-0001 <locationId> [--apply]
nj selftest                                  offline check, touches no tenant
```

A device is an id, an exact name or a substring of the name. An ambiguous substring lists the matches and stops.

### Install

```nix
# flake inputs: ninjaone-mcp.url = "github:lcleveland/ninjaone-mcp";
environment.systemPackages = [ ninjaone-mcp.packages.${pkgs.stdenv.hostPlatform.system}.nj ];
```

Or without installing: `nix run github:lcleveland/ninjaone-mcp#nj -- scripts`. With the overlay it is `pkgs.nj`. Without Nix, run `python3 tools/nj.py`.

### Set up

`nj` uses two NinjaOne apps. The API Services app reads (device lookup, `scripts`, `analyze` and the rest, `run` included). The Web app only dispatches `run`, as the signed-in user.

1. **API Services app.** Create it as in [Getting an API client](#getting-an-api-client). `monitoring` is enough for `nj`. If you already run this server, reuse its client.
2. **Web app.** In **Administration → Apps → API → Client App IDs → Add**, pick the **Web** platform, the one that has a client secret and a Redirect URI (not API Services, not a native or single-page platform). Set:
   - Redirect URI `http://localhost:8765/callback`
   - Scopes **Monitoring**, **Management** and **Control**
   - Allowed grant types **Authorization code** and **Refresh token**

   Choose the platform and the grant types on the Add form: the Edit view does not show the platform, and shows only Refresh token under grant types, so a mistake there is hard to see afterwards. Save, then copy the client ID and secret. The app list then shows `offline_access` next to the three scopes. That comes from the Refresh token grant, and `nj auth` asks for it.
3. **Config.** The directory is `${XDG_CONFIG_HOME:-~/.config}/ninjaone`:
   ```sh
   d=${XDG_CONFIG_HOME:-$HOME/.config}/ninjaone
   mkdir -p "$d" && chmod 700 "$d"
   printf 'NINJAONE_REGION=us2\nNINJAONE_CLIENT_ID=<api services id>\n' > "$d/env"
   printf '%s' '<api services secret>' > "$d/client-secret"
   printf 'NINJAONE_WEB_CLIENT_ID=<web id>\nNINJAONE_WEB_CLIENT_SECRET=<web secret>\n' > "$d/webapp.env"
   chmod 400 "$d/client-secret" "$d/webapp.env"
   ```
   `NINJAONE_REGION` takes the same values as the server (`app`, `us`, `us2`, `eu`, `ca`, `oc`, `fed`), or set `NINJAONE_BASE_URL`. Use the region: a vanity admin domain may load the console but is not reliable for the OAuth token endpoint. The secret can instead be `NINJAONE_CLIENT_SECRET_FILE=<path>` in `env`, which is what the server uses. `NINJAONE_SCOPES` defaults to `monitoring`.
4. **Consent.** `nj auth` prints a URL and listens on `127.0.0.1:8765` for the redirect, so open the URL in a browser on the same machine (over SSH, forward that port). Sign in as the user who should own the runs and click Authorize. It prints `saved .../user-tokens.json`.
5. **Check.** `nj scripts` lists the library, and `nj run PC-0001 <scriptId>` prints the request without sending it.

Any key can also come from the environment, which wins over the files. The `NINJAONE_*` names match the server's, so one environment can serve both.

### Running and reading results

`run` is a dry run unless you pass `--apply`. The script runs as SYSTEM. A dispatch is queued to the agent and not confirmed, and an offline device holds it until the agent checks in. Read the outcome from the device's activities. With this server that is `ninjaone_activity`; the script output is at `data.message.params.output` in each activity. `nj get /v2/device/<id>/activities` also works but can be very slow.

### Notes

- Only library scripts run. The public API cannot create or edit scripts (`GET /v2/automation/scripts` is all there is), so import them in the console (Administration → Library → Automation).
- `user-tokens.json` (mode 0600) holds the user's refresh token, which can run scripts as SYSTEM on every device that user can reach. Treat it like a password.
- The refresh token rotates on every use, so keep one token file per machine, and keep it across reboots. If the home directory is ephemeral (impermanence), persist `~/.config/ninjaone`, or run `nj auth` again after each boot.
- A rejected refresh token (revoked app, user disabled) fails the run; run `nj auth` again.
- `nix flake check` builds `nj` and runs `nj selftest`.

## Nix

```nix
{
  inputs.ninjaone-mcp.url = "github:lcleveland/ninjaone-mcp";

  outputs = { nixpkgs, ninjaone-mcp, ... }: {
    nixosConfigurations.host = nixpkgs.lib.nixosSystem {
      modules = [
        ninjaone-mcp.nixosModules.default
        {
          services.ninjaone-mcp = {
            enable = true;
            region = "us2";
            clientId = "<client id>";
            # or, to keep the ID out of the Nix config:
            #   printf %s '<client id>' | sudo install -m 0400 /dev/stdin /persist/secrets/ninjaone-client-id
            # clientIdFile = "/persist/secrets/ninjaone-client-id";
            # sops-nix / agenix path, or a root-only file:
            #   printf %s '<secret>' | sudo install -m 0400 /dev/stdin /persist/secrets/ninjaone-client-secret
            clientSecretFile = "/persist/secrets/ninjaone-client-secret";
            allowTickets = true;
          };
        }
      ];
    };
  };
}
```

The module:
- passes secrets through systemd `LoadCredential`, so they never reach the Nix store, argv or the environment;
- runs the service as a hardened DynamicUser (`systemd-analyze security` scores 1.1);
- refuses a non-loopback listener without `bearerTokenFile`;
- warns when `allowScripts` or `allowDeviceAdmin` is on.

| Option | Default | Notes |
|---|---|---|
| `enable`, `package` | off, this flake's build | |
| `region` / `baseUrl` | | exactly one |
| `clientId` / `clientIdFile` | | exactly one |
| `clientSecretFile` | | the secret file is a runtime path string, never a Nix path |
| `scopes` | `[ ]` (all on the app) | `monitoring`, `management`, `control` |
| `allowTickets`, `allowDocumentation`, `allowCustomFields`, `allowDeviceMaintenance`, `allowDeviceActions`, `allowScripts`, `allowDeviceAdmin` | `false` | one per capability |
| `maxBulk`, `toolGroups` | 50, all | |
| `listenAddress`, `port`, `path` | `127.0.0.1`, 8233, `/mcp` | |
| `bearerTokenFile`, `openFirewall` | none, `false` | |
| `requestTimeout`, `logLevel` | `30s`, `info` | |
| `user`, `group` | DynamicUser | |
| `installCli` | `enable` | binary on PATH |
| `extraArgs`, `environment` | | never put a secret here |

To install only the CLI for stdio use on a workstation, set `services.ninjaone-mcp.installCli = true;` and leave `enable` off. `overlays.default` provides `pkgs.ninjaone-mcp`.

### Claude Code

Over HTTP, against the NixOS service:

```sh
claude mcp add --scope user --transport http ninjaone http://127.0.0.1:8233/mcp
```

Over stdio:

```sh
claude mcp add ninjaone -e NINJAONE_REGION=us2 -e NINJAONE_CLIENT_ID=<id> \
  -e NINJAONE_CLIENT_SECRET_FILE=$HOME/.config/ninjaone-mcp/client-secret -- ninjaone-mcp
```

## Checked against us2 (2026-10-02)

- `df` with `%20` for spaces works. `+` is still untested, but the client never sends it.
- The last page of an `after`-paged list is an empty `results` array.
- `/v2/queries/*` page by `cursor` (`{name, count, expires, offset}`). `ts` takes `after <date>` or `before <date>`. Anything else returns `500 InvalidFilterException`.
- The syntax of `of` on `/v2/organizations` is still unknown: `name = …`, `id = 1`, `org = 1` and `org in (1)` all return `500 InvalidFilterException`.
- A client-credentials API client with `monitoring` and `management` scopes can read everything checked, and sees organization 1 and about 480 devices.
- No throttling seen: 100, then 500 concurrent `GET /v2/organizations` all succeeded, the 500 in 0.85 s with none slower than 0.75 s, so nothing was retried. The real limit is higher, or applies elsewhere.
- `script/run` returns `403 user_context_required` for an API client, so the `scripts` capability cannot run scripts unless NinjaOne grants user context. [`nj`](#nj-running-scripts-as-a-user) is the workaround.

Still unverified: what a throttled response looks like (the client treats a 429 or an HTML page as throttling and retries GETs only), whether technician roles limit an API client, and ticket writes (this tenant does not use NinjaOne ticketing).

## Development

```sh
nix develop            # go, gopls, nixfmt, jq
go test ./...
nix flake check        # package build + Go tests + module eval checks + VM test
nix build .#checks.x86_64-linux.vm   # the VM test alone (about a minute with KVM)
```

The design decisions are recorded on [Map: Go NinjaOne MCP server](https://github.com/lcleveland/ninjaone-mcp/issues/1). Research notes are in [`docs/research/`](docs/research/).
