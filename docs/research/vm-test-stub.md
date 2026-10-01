# Research: stubbing NinjaOne for the NixOS VM test

Ticket: #4 (part of #1). Question: how should the VM test fake NinjaOne so it can
assert that the unit is active, its `systemd-analyze security` score, that
credentials arrive via `LoadCredential=` and nobody else can read them, and that
the client secret is absent from `/proc/<pid>/cmdline` and `/proc/<pid>/environ`
but still reaches the token endpoint?

## Answer (short)

- Copy netskope-mcp's pattern: a ~50-line `pkgs.writers.writePython3Bin` stub
  (stdlib `http.server`) run as its own systemd unit in the same VM on
  `127.0.0.1`, plain HTTP.
- The stub emulates two things only: `POST /ws/oauth/token` (form-encoded
  client credentials -> `access_token` JSON) and "any GET with the right
  `Authorization: Bearer`" -> `200` with an empty JSON list.
- It appends every `client_secret` and every bearer it sees to files in `/tmp`,
  so the test can grep them. That is what proves the secret "reached the token
  endpoint".
- The module needs a `baseUrl` override (mutually exclusive with `region`),
  exactly like netskope-mcp's `baseUrl`/`tenant`. Don't use a test CA.

## How netskope-mcp does it (primary source)

Source: `/home/lcleveland/Documents/netskope-mcp/tests/module.nix` (261 lines),
wired up in `flake.nix` as `checks.module = import ./tests/module.nix { inherit pkgs self; }`.

- **The stub tenant** is `pkgs.writers.writePython3Bin "stub-tenant"`, about 30
  lines of stdlib `http.server`. It answers any GET, checks the
  `Netskope-API-Token` header against a constant interpolated from Nix
  (`builtins.toJSON apiToken`), returns `401` when the token is wrong and
  `{"status":"success","total":0,"data":[]}` when it's right. It **appends every
  token it sees to `/tmp/seen-tokens`**, and silences `log_message`.
- **It runs as a unit**: `systemd.services.stub-tenant` with
  `wantedBy = multi-user.target`, `before = netskope-mcp.service`,
  `Restart = on-failure`. It has no `PrivateTmp`, so the test script can read
  its `/tmp` file directly.
- **Secrets are seeded** with `systemd.tmpfiles.settings` as root-only `0400`
  files in `/run`, which is the same shape sops-nix and agenix produce. The
  module's `apiTokenFile` and `bearerTokenFile` point at those files.
- **Pointing at the stub** happens through the module's own option:
  `baseUrl = "http://127.0.0.1:9443"`. The module turns it into `--base-url` in
  ExecStart (`modules/netskope-mcp.nix` around lines 58-61), and an assertion
  makes `tenant` and `baseUrl` mutually exclusive (line ~320). So plain HTTP works
  with no CA.
- **An MCP probe**, a second `writePython3Bin` using stdlib `urllib`, runs
  initialize, then `tools/list`, then a read-only `tools/call`. It accepts either
  a JSON or an SSE reply. The `tools/call` step is what makes the token actually
  go out on the wire.
- **The test script's assertions**:
  - It calls `wait_for_unit` on both units and `wait_for_open_port` on the MCP
    port.
  - `/healthz` answers without the bearer, and `/mcp` returns `401` with no
    bearer or the wrong one.
  - `grep -qF <token> /tmp/seen-tokens` shows the credential reached the
    upstream.
  - For `/run/credentials/<unit>/` and each file in it,
    `stat -L -c %a` must end in `0`, i.e. "other" has no access. On top of that,
    `runuser -u nobody -- cat <cred>` must fail. Systemd grants the
    DynamicUser access through an ACL, so the test deliberately doesn't assert
    exact modes.
  - It gets `MainPID` from `systemctl show -p MainPID --value`, then
    `machine.fail("tr '\\0' '\\n' < /proc/{pid}/cmdline | grep -qF <secret>")`,
    and the same for `environ`, plus the env var name.
  - `systemd-analyze security <unit> --no-pager | tail -1` is parsed with the
    regex `level for \S+: ([0-9.]+)` and must come in under `3.0`. The real
    score is about 1.1. Watch out: the trailing `:-)` contains a colon.
  - `nsenter --mount --target <pid> -- test -w /etc` must fail
    (`ProtectSystem=strict`).
  - The unit is restarted and must come back healthy.

netbox-mcp (`/home/lcleveland/Documents/netbox-mcp/tests/vm-netbox.nix`) goes
the other way: it runs a **real** `services.netbox` in the VM, with 2 GiB of
memory, migrations and a seeded token. Its own header says that takes about 7
minutes. NinjaOne is SaaS-only, so that option doesn't exist here. The stub
approach is the only one that applies, and it's also the fast one.

The test-driver API used above (`runNixOSTest`, `nodes.machine`, `testScript`,
`machine.succeed`/`fail`/`wait_for_unit`/`wait_for_open_port`, `subtest`) is
documented in the nixpkgs manual's NixOS tests chapter (NixOS manual,
"Writing Tests"). Both sibling repos use it the same way.

## What NinjaOne's token endpoint looks like (what the stub must emulate)

- **The request** is `POST {base}/ws/oauth/token` with
  `Content-Type: application/x-www-form-urlencoded`. Its fields are
  `grant_type=client_credentials`, `client_id`, `client_secret` and
  `scope=monitoring[ management][ control]`, space-separated.
  - Sources: NinjaOne Public API reference (app.ninjarmm.com/apidocs-beta,
    Authorization pages). The NinjaOne PowerShell module's
    `Connect-NinjaOne.ps1` posts the same form fields to `oauth/token` under
    `/ws/`, with that content type. NinjaOne's "API OAuth Token Configuration"
    doc lists the scopes as Monitoring, Management and Control, and
    "Client Credentials" as an allowed grant type.
- **The response** is the RFC 6749 §5.1 shape:
  `{"access_token": "...", "token_type": "bearer", "expires_in": 3600, "scope": "monitoring"}`.
  Client credentials issue no refresh token (RFC 6749 §4.4.3).
- **API calls** carry `Authorization: Bearer <access_token>` (RFC 6750 §2.1)
  against `{base}/v2/...` (or `/api/v2/...`). The stub doesn't care about the
  path. It matches any GET.
- **Client authentication**: RFC 6749 §2.3.1 allows the secret either in the
  form body or as HTTP Basic. Whichever one the server picks, the stub should
  **record both** places, so the test doesn't encode an implementation detail.

## The "secret reaches the token endpoint" assertion

This is the OAuth analogue of netskope's `/tmp/seen-tokens`, and it has two
links:

1. The token endpoint appends `client_secret` (from the form, or decoded from
   Basic) to `/tmp/seen-secrets`. It issues `access_token` **only** if
   `client_id` and `client_secret` both match the Nix-interpolated constants;
   otherwise it returns `401` with `{"error":"invalid_client"}`.
2. The API handler accepts only `Bearer <the issued token>` and appends the
   bearer to `/tmp/seen-bearers`.

The test then:
- runs `grep -qF <secret> /tmp/seen-secrets`, which proves
  LoadCredential -> `$CREDENTIALS_DIRECTORY` -> form body;
- runs `grep -qF <issued-token> /tmp/seen-bearers`, which proves the token
  exchange was actually used (the probe's `tools/call` succeeding implies it
  too);
- runs `machine.fail` grep for the secret **and** the client id/secret env var
  names in `/proc/<pid>/cmdline` and `/proc/<pid>/environ`, as netskope does.
  The client ID isn't secret, so it may appear in argv. Only the secret and the
  MCP bearer are checked.

The credential checks need `LoadCredential = [ "client-secret:…" ]` (plus
`http-token` for the MCP bearer). They then reuse netskope's loop over
`/run/credentials/ninjaone-mcp.service/*` unchanged: other-bit `0`, and
`runuser -u nobody cat` fails.

## TLS and base-URL override

The stub speaks plain `http://127.0.0.1:<port>`. There are two ways to make the
server talk to it:

- **Recommended: a `baseUrl` module option** that overrides `region`, mapped to
  a `--base-url` flag, with an assertion that exactly one of `region` and
  `baseUrl` is set. This mirrors netskope-mcp exactly. It costs nothing, needs
  no certificates, and it's also useful for NinjaOne instances that aren't in a
  known region table. The server derives the token URL as `{base}/ws/oauth/token`
  and API URLs as `{base}/v2/…` from that single value, so one override covers
  both.
  - Optional hardening for #1 to decide: refuse non-`https` base URLs unless
    the host is loopback. That way the override can't leak the secret in clear
    text on a real network.
- **Rejected: a test CA.** This would mean generating a cert at build time,
  adding it to `security.pki.certificateFiles`, and serving TLS from the stub
  via `ssl.wrap_socket`. The Go server would also have to resolve a fake
  `*.ninjarmm.com` (an `/etc/hosts` entry via `networking.hosts`). That's more
  moving parts, and it would test TLS plumbing from Go's stdlib rather than
  this module.

The netskope sandbox stays compatible with all of this. `RestrictAddressFamilies`
allows `AF_INET`, and `SocketBindDeny = any` only stops **binding**, not
connecting. If the ninjaone module later adds `IPAddressDeny=any` and
`IPAddressAllow=`, it must allow `localhost` in the test, or the test must
override it.

## Recommended stub (about 50 lines of Python, stdlib only)

```nix
stubNinja = pkgs.writers.writePython3Bin "stub-ninjaone" { } ''
  import base64
  import http.server
  import json
  import urllib.parse

  CLIENT_ID = ${builtins.toJSON clientId}
  SECRET = ${builtins.toJSON clientSecret}
  ISSUED = "stub-access-token"


  def log(path, value):
      with open(path, "a") as fh:
          fh.write(value + "\n")


  class Handler(http.server.BaseHTTPRequestHandler):
      def reply(self, code, obj):
          body = json.dumps(obj).encode()
          self.send_response(code)
          self.send_header("Content-Type", "application/json")
          self.send_header("Content-Length", str(len(body)))
          self.end_headers()
          self.wfile.write(body)

      def do_POST(self):
          if self.path != "/ws/oauth/token":
              return self.reply(404, {})
          n = int(self.headers.get("Content-Length", 0))
          form = urllib.parse.parse_qs(self.rfile.read(n).decode())
          cid = form.get("client_id", [""])[0]
          sec = form.get("client_secret", [""])[0]
          auth = self.headers.get("Authorization", "")
          if auth.startswith("Basic "):
              raw = base64.b64decode(auth[6:]).decode()
              cid, _, sec = raw.partition(":")
          log("/tmp/seen-secrets", sec)
          ok = form.get("grant_type") == ["client_credentials"]
          if not ok or (cid, sec) != (CLIENT_ID, SECRET):
              return self.reply(401, {"error": "invalid_client"})
          self.reply(200, {"access_token": ISSUED, "token_type": "bearer",
                           "expires_in": 3600,
                           "scope": form.get("scope", [""])[0]})

      def do_GET(self):
          bearer = self.headers.get("Authorization", "")
          log("/tmp/seen-bearers", bearer)
          if bearer != "Bearer " + ISSUED:
              return self.reply(401, {"error": "invalid_token"})
          self.reply(200, [])

      def log_message(self, *args):
          pass


  http.server.HTTPServer(("127.0.0.1", ${toString port}), Handler).serve_forever()
'';
```

Everything else carries over from `netskope-mcp/tests/module.nix` with names
swapped: the unit wiring, the tmpfiles-seeded `0400` secrets, the MCP probe,
and the assertion subtests. Expect the whole `tests/module.nix` to be
**about 260 lines**, the same as netskope's.

Deliberately not covered:
- **Token expiry and refresh.** You could add it later by issuing
  `expires_in: 1` and asserting that a second POST appears in
  `/tmp/seen-secrets`, if the server grows refresh logic worth guarding.
- **Real payload shapes and scope enforcement.** Those need a real tenant; as
  in netskope, they belong in the README's evidence section.

## Sources

- `/home/lcleveland/Documents/netskope-mcp/tests/module.nix`, `modules/netskope-mcp.nix`, `flake.nix`
- `/home/lcleveland/Documents/netbox-mcp/tests/vm-netbox.nix`
- NinjaOne Public API reference, Authorization: https://app.ninjarmm.com/apidocs-beta/authorization/flows/authorization-code-flow (and sibling client-credentials and refresh-token pages)
- NinjaOne, API OAuth Token Configuration: https://www.ninjaone.com/docs/application-programming-interface-api/oauth-token-configuration/
- NinjaOne PowerShell module, `Connect-NinjaOne.ps1`: https://www.powershellgallery.com/packages/NinjaOne/1.2.2/Content/Public/Connect-NinjaOne.ps1
- RFC 6749 §2.3.1, §4.4, §5.1, §5.2: https://www.rfc-editor.org/rfc/rfc6749
- RFC 6750 §2.1: https://www.rfc-editor.org/rfc/rfc6750
- NixOS manual, "Writing Tests" (NixOS test driver): https://nixos.org/manual/nixos/stable/#sec-writing-nixos-tests
