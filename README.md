# ninjaone-mcp

An MCP server for the [NinjaOne](https://www.ninjaone.com/) RMM public API v2, written in Go. It serves over stdio or streamable HTTP and is packaged as a Nix flake with a NixOS module.

The server is read-only by default. Writes are turned on per **capability** (tickets, scripts, device actions, ...), every write needs a reason that goes to the audit log, and tenant administration is never exposed at all.

## Tools

| Group | Tools |
|---|---|
| core (always on) | `ninjaone_status` (auth, scopes and reachability probe), `ninjaone_api` (any `/v2/` path, capability-gated) |
| inventory | `ninjaone_organization`, `ninjaone_location`, `ninjaone_device` (list, search, get with console link), `ninjaone_device_detail` (disks, volumes, software, services, ...), `ninjaone_report` (fleet-wide `/v2/queries/*`), `ninjaone_lookup` (policies, device roles, groups), `ninjaone_user`, and with a capability enabled `ninjaone_device_action` and `ninjaone_script` |
| alerts | `ninjaone_alert`, `ninjaone_activity` (activity log and running jobs) |
| patching | `ninjaone_patch` (OS and software; pending and installs; per device or fleet) |
| docs | `ninjaone_custom_field`, `ninjaone_document`, `ninjaone_kb_article` |
| ticketing | `ninjaone_ticket` (get, boards, board_run, log entries, lookups) |
| backup | `ninjaone_backup` (jobs, integrity-check jobs, usage) |

Every tool takes an `action`. Lists:
- return a brief field set unless you pass `fields`;
- cap results at 200 items and 60 KiB, adding a `_truncation` note when they have to cut;
- return `next_cursor` when there is more. Pass it back as `cursor`. It hides NinjaOne's five different paging schemes.

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
| `--client-id` | `NINJAONE_CLIENT_ID` | required |
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
| `clientId`, `clientSecretFile` | | the secret file is a runtime path string, never a Nix path |
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

## Not yet verified against a live tenant

The suite runs against fakes. These behaviours come from NinjaOne's docs and community clients, and still need checking on us2:
- whether `df` accepts `+` for spaces (the client always sends `%20`);
- how throttling actually looks. The client treats a 429 or an HTML page as throttling and retries GETs only;
- what `script/run` returns. The server never relies on it;
- whether technician roles limit a client-credentials API client, or whether it sees the whole tenant;
- whether ticket writes and script runs return `403 user_context_required` for an API client. If they do, the error says so.

## Development

```sh
nix develop            # go, gopls, nixfmt, jq
go test ./...
nix flake check        # package build + Go tests + module eval checks + VM test
nix build .#checks.x86_64-linux.vm   # the VM test alone (about a minute with KVM)
```

The design decisions are recorded on [Map: Go NinjaOne MCP server](https://github.com/lcleveland/ninjaone-mcp/issues/1). Research notes are in [`docs/research/`](docs/research/).
