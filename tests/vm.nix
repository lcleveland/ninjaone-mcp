# VM test: the real binary, a stub NinjaOne, and the assertions only a booted
# machine can make: the unit comes up hardened, the client secret is
# delivered by systemd and readable by nothing else, it appears in neither
# argv nor the environment, and it still reaches the token endpoint. With
# user context: one sign-in through /oauth/callback, script runs sent with the
# user token, and the rotating refresh token surviving a restart.
#
# Not covered: real NinjaOne payload shapes, token expiry, or how a narrowly
# scoped API client behaves. Those need a live tenant.
{ pkgs, self }:

let
  stubPort = 9443;
  mcpPort = 8233;

  clientId = "vm-test-client";
  clientSecret = "s3cr3t-client-secret";
  accessToken = "issued-access-token";
  bearerToken = "bearer-abc";
  webClientId = "vm-web-client";
  webClientSecret = "w3b-s3cr3t";
  authCode = "vm-auth-code";
  redirect = "http://127.0.0.1:${toString mcpPort}/oauth/callback";

  # Client credentials at /ws/oauth/token, then any /v2/ GET with the issued
  # bearer. Records every secret and bearer it sees, so the test can prove the
  # credential made it out of systemd and onto the wire. For user context it
  # also takes the Web app's authorization_code and refresh_token grants,
  # rotating the refresh token (rt-1, rt-2, ...) and issuing user-access-<n>,
  # and accepts script/run only with a user token.
  stub = pkgs.writers.writePython3Bin "stub-ninjaone" { } ''
    import base64
    import http.server
    import json
    import urllib.parse

    CLIENT_ID = ${builtins.toJSON clientId}
    SECRET = ${builtins.toJSON clientSecret}
    TOKEN = ${builtins.toJSON accessToken}
    WEB_ID = ${builtins.toJSON webClientId}
    WEB_SECRET = ${builtins.toJSON webClientSecret}
    CODE = ${builtins.toJSON authCode}
    REDIRECT = ${builtins.toJSON redirect}
    issued = {"n": 0, "refresh": None}


    def record(path, value):
        with open(path, "a") as fh:
            fh.write(value + "\n")


    class Handler(http.server.BaseHTTPRequestHandler):
        def reply(self, status, payload):
            body = json.dumps(payload).encode()
            self.send_response(status)
            self.send_header("Content-Type", "application/json")
            self.send_header("Content-Length", str(len(body)))
            self.end_headers()
            self.wfile.write(body)

        def user_grant(self, form):
            def get(k):
                return form.get(k, [""])[0]
            record("/tmp/seen-secrets", get("client_secret"))
            if get("client_id") != WEB_ID or get("client_secret") != WEB_SECRET:
                return self.reply(401, {"error": "invalid_client"})
            grant = get("grant_type")
            record("/tmp/seen-grants", grant + " " + get("refresh_token"))
            if grant == "authorization_code":
                ok = get("code") == CODE and get("redirect_uri") == REDIRECT
            else:
                ok = issued["refresh"] is not None
                ok = ok and get("refresh_token") == issued["refresh"]
            if not ok:
                return self.reply(400, {"error": "invalid_grant"})
            issued["n"] += 1
            issued["refresh"] = "rt-%d" % issued["n"]
            self.reply(200, {"access_token": "user-access-%d" % issued["n"],
                             "refresh_token": issued["refresh"],
                             "token_type": "Bearer", "expires_in": 3600})

        def do_POST(self):
            if self.path == "/v2/device/5/script/run":
                bearer = self.headers.get("Authorization", "")
                record("/tmp/seen-user-bearers", bearer)
                if not bearer.startswith("Bearer user-access-"):
                    return self.reply(403, {"resultCode": "user_context_required"})
                return self.reply(200, {})
            if self.path != "/ws/oauth/token":
                return self.reply(404, {"resultCode": "NOT_FOUND"})
            n = int(self.headers.get("Content-Length", "0"))
            form = urllib.parse.parse_qs(self.rfile.read(n).decode())
            grant = form.get("grant_type", [""])[0]
            if grant in ("authorization_code", "refresh_token"):
                return self.user_grant(form)
            cid = form.get("client_id", [""])[0]
            secret = form.get("client_secret", [""])[0]
            auth = self.headers.get("Authorization", "")
            if auth.startswith("Basic "):
                raw = base64.b64decode(auth[6:]).decode()
                cid, _, secret = raw.partition(":")
            record("/tmp/seen-secrets", secret)
            if cid != CLIENT_ID or secret != SECRET:
                return self.reply(401, {"error": "invalid_client"})
            self.reply(200, {"access_token": TOKEN, "token_type": "Bearer",
                             "expires_in": 3600, "scope": "monitoring"})

        def do_GET(self):
            bearer = self.headers.get("Authorization", "")
            record("/tmp/seen-bearers", bearer)
            if bearer != "Bearer " + TOKEN:
                return self.reply(401, {"error": "not_authenticated"})
            if self.path == "/v2/device/5":
                return self.reply(200, {"id": 5, "displayName": "VM01"})
            self.reply(200, [])

        def log_message(self, *args):
            pass


    addr = ("127.0.0.1", ${toString stubPort})
    http.server.HTTPServer(addr, Handler).serve_forever()
  '';

  # initialize, then by mode: check (tools/list and ninjaone_status, so the
  # token exchange runs), authorize (print the sign-in URL) or run (a script
  # run, which must go out with the user token).
  # Streamable HTTP may answer as JSON or as an SSE frame; accept both.
  probe = pkgs.writers.writePython3Bin "mcp-probe" { } ''
    import json
    import sys
    import urllib.request

    URL = "http://127.0.0.1:${toString mcpPort}/mcp"
    BEARER = ${builtins.toJSON bearerToken}
    MODE = sys.argv[1] if len(sys.argv) > 1 else "check"


    def post(payload, session=None):
        req = urllib.request.Request(URL, data=json.dumps(payload).encode(),
                                     method="POST")
        req.add_header("Content-Type", "application/json")
        req.add_header("Accept", "application/json, text/event-stream")
        req.add_header("Authorization", "Bearer " + BEARER)
        if session:
            req.add_header("Mcp-Session-Id", session)
        resp = urllib.request.urlopen(req, timeout=20)
        raw = resp.read().decode()
        for line in raw.splitlines():
            if line.startswith("data:"):
                raw = line[5:].strip()
                break
        parsed = json.loads(raw) if raw.strip() else {}
        return resp.headers.get("Mcp-Session-Id"), parsed


    session, init = post({
        "jsonrpc": "2.0", "id": 1, "method": "initialize",
        "params": {"protocolVersion": "2025-06-18", "capabilities": {},
                   "clientInfo": {"name": "vm-test", "version": "0"}},
    })
    name = init.get("result", {}).get("serverInfo", {}).get("name")
    assert name == "ninjaone-mcp", f"unexpected serverInfo: {init}"
    post({"jsonrpc": "2.0", "method": "notifications/initialized"}, session)


    def call(name, arguments, want_error=False):
        _, called = post({
            "jsonrpc": "2.0", "id": 3, "method": "tools/call",
            "params": {"name": name, "arguments": arguments},
        }, session)
        result = called["result"]
        assert bool(result.get("isError")) == want_error, called
        return result.get("structuredContent") or {}


    if MODE == "authorize":
        # Prints the sign-in URL; the test plays the browser.
        print(call("ninjaone_status", {"action": "authorize"})["authorize_url"])
    elif MODE == "run":
        status = call("ninjaone_status", {})
        assert status["user_context"] == {"configured": True,
                                          "authorized": True}, status
        out = call("ninjaone_script", {
            "action": "run", "id": 5, "confirm": "VM01",
            "reason": "vm test", "body": {"type": "SCRIPT", "id": 171},
        })
        assert out.get("dispatched"), out
        print(f"ok: dispatched {out}")
    else:
        _, listed = post({"jsonrpc": "2.0", "id": 2, "method": "tools/list"},
                         session)
        tools = listed.get("result", {}).get("tools", [])
        assert len(tools) >= 10, f"expected the full tool surface, got {tools}"
        # Only the scripts capability is enabled: it alone may write.
        writable = {t["name"] for t in tools
                    if not t.get("annotations", {}).get("readOnlyHint")}
        assert writable == {"ninjaone_script", "ninjaone_api"}, writable

        status = call("ninjaone_status", {})
        assert status["authenticated"] and status["api_reachable"], status
        assert status["user_context"]["configured"], status
        print(f"ok: {len(tools)} tools, status {status}")
  '';
in
pkgs.testers.runNixOSTest {
  name = "ninjaone-mcp-vm";

  nodes.machine =
    { ... }:
    {
      imports = [ self.nixosModules.ninjaone-mcp ];

      environment.systemPackages = [
        pkgs.curl
        probe
      ];

      systemd.services.stub-ninjaone = {
        description = "Stub NinjaOne";
        wantedBy = [ "multi-user.target" ];
        before = [ "ninjaone-mcp.service" ];
        serviceConfig = {
          ExecStart = pkgs.lib.getExe stub;
          Restart = "on-failure";
        };
      };

      # Root-only 0400 files, the shape sops-nix and agenix produce.
      systemd.tmpfiles.settings."10-ninjaone-mcp" = {
        "/run/ninjaone-client-secret".f = {
          user = "root";
          group = "root";
          mode = "0400";
          argument = clientSecret;
        };
        "/run/ninjaone-client-id".f = {
          user = "root";
          group = "root";
          mode = "0400";
          argument = clientId;
        };
        "/run/ninjaone-mcp-bearer".f = {
          user = "root";
          group = "root";
          mode = "0400";
          argument = bearerToken;
        };
        "/run/ninjaone-web-client-id".f = {
          user = "root";
          group = "root";
          mode = "0400";
          argument = webClientId;
        };
        "/run/ninjaone-web-client-secret".f = {
          user = "root";
          group = "root";
          mode = "0400";
          argument = webClientSecret;
        };
      };

      services.ninjaone-mcp = {
        enable = true;
        baseUrl = "http://127.0.0.1:${toString stubPort}";
        clientIdFile = "/run/ninjaone-client-id";
        clientSecretFile = "/run/ninjaone-client-secret";
        bearerTokenFile = "/run/ninjaone-mcp-bearer";
        port = mcpPort;
        # User context: script runs go out with the signed-in user's token.
        webClientIdFile = "/run/ninjaone-web-client-id";
        webClientSecretFile = "/run/ninjaone-web-client-secret";
        allowScripts = true;
      };
    };

  testScript = ''
    import re

    machine.wait_for_unit("stub-ninjaone.service")
    machine.wait_for_unit("ninjaone-mcp.service")
    machine.wait_for_open_port(${toString mcpPort})

    with subtest("health is reachable without the bearer token"):
        out = machine.succeed("curl -fsS http://127.0.0.1:${toString mcpPort}/healthz")
        assert '"status":"ok"' in out, out

    with subtest("the MCP endpoint refuses unauthenticated callers"):
        for hdr in ("", "-H 'Authorization: Bearer wrong'"):
            machine.succeed(
                f"test \"$(curl -s -o /dev/null -w %{{http_code}} -X POST {hdr} "
                "-H 'Content-Type: application/json' -d '{}' "
                "http://127.0.0.1:${toString mcpPort}/mcp)\" = 401"
            )

    with subtest("a full MCP session works, only scripts can write, and authenticates upstream"):
        print(machine.succeed("mcp-probe"))

    with subtest("the secret reached the token endpoint and the token was used"):
        # LoadCredential -> $CREDENTIALS_DIRECTORY -> token request form.
        machine.succeed("grep -qxF ${clientSecret} /tmp/seen-secrets")
        machine.succeed("grep -qxF 'Bearer ${accessToken}' /tmp/seen-bearers")

    with subtest("the credentials are not readable by anything else"):
        creds = "/run/credentials/ninjaone-mcp.service"
        for path in (creds, f"{creds}/client-secret", f"{creds}/http-auth-token", f"{creds}/web-client-secret"):
            mode = machine.succeed(f"stat -L -c %a {path}").strip()
            assert mode[-1] == "0", f"{path} is mode {mode}: readable by other"
        machine.fail(f"runuser -u nobody -- cat {creds}/client-secret")
        machine.fail(f"runuser -u nobody -- cat {creds}/http-auth-token")
        machine.succeed("test \"$(stat -c %a /run/ninjaone-client-secret)\" = 400")

    with subtest("the secrets are in neither argv nor the environment"):
        pid = machine.succeed("systemctl show -p MainPID --value ninjaone-mcp.service").strip()
        for f in ("cmdline", "environ"):
            machine.fail(f"tr '\\0' '\\n' < /proc/{pid}/{f} | grep -qF ${clientSecret}")
            machine.fail(f"tr '\\0' '\\n' < /proc/{pid}/{f} | grep -qF ${bearerToken}")
            machine.fail(f"tr '\\0' '\\n' < /proc/{pid}/{f} | grep -qF ${webClientSecret}")
        machine.fail(f"tr '\\0' '\\n' < /proc/{pid}/environ | grep -q NINJAONE_CLIENT_SECRET")

    with subtest("a user signs in once through /oauth/callback, without the bearer"):
        from urllib.parse import urlparse, parse_qs
        url = machine.succeed("mcp-probe authorize").strip().splitlines()[-1]
        q = parse_qs(urlparse(url).query)
        assert q["redirect_uri"] == ["${redirect}"], url
        state = q["state"][0]
        # The test plays the browser NinjaOne redirects back.
        page = machine.succeed(f"curl -fsS '${redirect}?code=${authCode}&state={state}'")
        assert "Authorized" in page, page
        machine.fail(f"curl -fsS '${redirect}?code=${authCode}&state={state}'")
        tok = "/var/lib/private/ninjaone-mcp/user-token.json"
        machine.succeed(f"test \"$(stat -c %a {tok})\" = 600")
        machine.fail(f"runuser -u nobody -- cat {tok}")

    with subtest("a script run goes out with the user token"):
        print(machine.succeed("mcp-probe run"))
        machine.succeed("grep -qxF 'Bearer user-access-1' /tmp/seen-user-bearers")

    with subtest("the unit is actually hardened"):
        out = machine.succeed("systemd-analyze security ninjaone-mcp.service --no-pager | tail -1")
        print(out)
        # "Overall exposure level for ninjaone-mcp.service: 1.1 OK :-)"
        found = re.search(r"level for \S+: ([0-9.]+)", out)
        assert found is not None, f"could not read an exposure score from: {out}"
        assert float(found.group(1)) < 3.0, f"unit exposure score regressed: {out}"
        machine.fail(f"nsenter --mount --target {pid} -- test -w /etc")

    with subtest("it survives a restart"):
        machine.succeed("systemctl restart ninjaone-mcp.service")
        machine.wait_for_open_port(${toString mcpPort})
        machine.succeed("curl -fsS http://127.0.0.1:${toString mcpPort}/healthz")
        # The refresh token survived in the state directory and rotates:
        # rt-1 is refreshed into user-access-2.
        print(machine.succeed("mcp-probe run"))
        machine.succeed("grep -qxF 'refresh_token rt-1' /tmp/seen-grants")
        machine.succeed("grep -qxF 'Bearer user-access-2' /tmp/seen-user-bearers")
  '';
}
