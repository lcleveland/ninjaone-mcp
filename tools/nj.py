#!/usr/bin/env python3
"""NinjaOne REST helper: run library scripts as a signed-in user.

ninjaone-mcp cannot run scripts: NinjaOne answers `script/run` with
403 user_context_required to a client-credentials (API Services) token. `run` here
uses a signed-in user's token instead (authorization code flow, refresh token kept
on disk), so the run is audited as that user.

  nj auth                  one-time browser consent (Web app, user context)
  nj scripts               list library scripts (id, name, language)
  nj run PC-0001 <scriptId> [params] [--apply]   run script as system (dry-run unless --apply)
  nj selftest              offline self-check

Config lives in ${XDG_CONFIG_HOME:-~/.config}/ninjaone/. Every key can also come from
the environment, which wins over the files; the NINJAONE_* names match ninjaone-mcp.

  env          NINJAONE_REGION (app, us, us2, eu, ca, oc, fed) or NINJAONE_BASE_URL, and
               the API Services app, used by `scripts` and by `run` to look the device
               up: NINJAONE_CLIENT_ID, NINJAONE_CLIENT_SECRET or
               NINJAONE_CLIENT_SECRET_FILE (default client-secret), NINJAONE_SCOPES
               (nj only, default monitoring)
  webapp.env   the Web app, for `auth` and for dispatching `run`:
               NINJAONE_WEB_CLIENT_ID / NINJAONE_WEB_CLIENT_SECRET

`run` needs a user-context token: create a NinjaOne "Web Application" app (redirect
http://127.0.0.1:8765/callback, scopes monitoring management control, grants
authorization code + refresh token), put its ID and secret in webapp.env (0400), then
run `nj auth` once. The refresh token lands in user-tokens.json (0600) and rotates on
every use.
"""
import fcntl, ipaddress, json, os, sys, urllib.error, urllib.request, urllib.parse

CONF = os.path.join(os.environ.get("XDG_CONFIG_HOME") or os.path.expanduser("~/.config"), "ninjaone")
ENVFILE = CONF + "/env"
# same table as ninjaone-mcp's internal/config
REGIONS = {r: f"https://{h}.ninjarmm.com" for r, h in
           [("app", "app"), ("us", "app"), ("us2", "us2"), ("eu", "eu"),
            ("ca", "ca"), ("oc", "oc"), ("fed", "fed")]}
_tok = None


def _conf(path):
    """KEY=VALUE file (missing is fine), overridden by NINJAONE_* from the environment."""
    try:
        kv = dict(l.strip().split("=", 1)
                  for l in open(path) if "=" in l and not l.startswith("#"))
    except FileNotFoundError:
        kv = {}
    kv.update((k, v) for k, v in os.environ.items() if k.startswith("NINJAONE_"))
    return kv


def _need(conf, key, path):
    if not conf.get(key):
        raise SystemExit(f"{key} is not set: put it in {path} or the environment")
    return conf[key]


def _loopback(host):
    if host == "localhost":
        return True
    try:
        return ipaddress.ip_address(host).is_loopback
    except ValueError:
        return False


def _base(conf):
    """Same rules as the server: exactly one of region and base URL, https unless loopback."""
    region, raw = conf.get("NINJAONE_REGION"), conf.get("NINJAONE_BASE_URL")
    if region and raw:
        raise SystemExit("NINJAONE_REGION and NINJAONE_BASE_URL are mutually exclusive")
    if raw:
        u = urllib.parse.urlparse(raw.rstrip("/"))
        # secrets and the refresh token go to this host, so never in the clear off-box
        if not u.hostname or u.scheme not in ("https", "http") or (
                u.scheme == "http" and not _loopback(u.hostname)):
            raise SystemExit(f"NINJAONE_BASE_URL must be https unless the host is loopback, got {raw!r}")
        return raw.rstrip("/")
    region = _need(conf, "NINJAONE_REGION", ENVFILE)
    if region not in REGIONS:
        raise SystemExit(f"NINJAONE_REGION {region!r}: expected one of {', '.join(REGIONS)}")
    return REGIONS[region]


_base_url = None


def base():
    global _base_url
    _base_url = _base_url or _base(_conf(ENVFILE))
    return _base_url


def token():
    global _tok
    if _tok:
        return _tok
    e = _conf(ENVFILE)
    secret = e.get("NINJAONE_CLIENT_SECRET")
    if not secret:
        path = e.get("NINJAONE_CLIENT_SECRET_FILE") or CONF + "/client-secret"
        try:
            secret = open(path).read().strip()
        except FileNotFoundError:
            raise SystemExit(f"no client secret: set NINJAONE_CLIENT_SECRET or create {path}")
    body = urllib.parse.urlencode({
        "grant_type": "client_credentials",
        "client_id": _need(e, "NINJAONE_CLIENT_ID", ENVFILE),
        "client_secret": secret,
        "scope": e.get("NINJAONE_SCOPES", "monitoring").replace(",", " "),
    }).encode()
    _tok = json.load(urllib.request.urlopen(base() + "/ws/oauth/token", body, timeout=30))["access_token"]
    return _tok


def get(path, _retry=True):
    """GET as the API Services app; exits on an HTTP error.

    NinjaOne keeps one live client_credentials token per app, so a token issued
    by a concurrent run invalidates ours. A 401 therefore means "re-authenticate",
    not "wrong secret" -- retry once before giving up. Timeouts retry once too;
    this is only ever a GET, so that is safe.
    """
    global _tok
    req = urllib.request.Request(base() + path, headers={"Authorization": "Bearer " + token()})
    try:
        return json.load(urllib.request.urlopen(req, timeout=60))
    except TimeoutError:
        if _retry:
            return get(path, _retry=False)
        raise
    except urllib.error.HTTPError as ex:
        if ex.code == 401 and _retry:
            _tok = None
            return get(path, _retry=False)
        raise SystemExit(f"GET {path} -> {ex.code} {ex.read().decode()[:200]}")


WEBENV = CONF + "/webapp.env"
USERTOK = CONF + "/user-tokens.json"
# 127.0.0.1, not localhost: the listener binds IPv4, and localhost may resolve to ::1 first
REDIRECT = "http://127.0.0.1:8765/callback"


def _web():
    e = _conf(WEBENV)
    for k in ("NINJAONE_WEB_CLIENT_ID", "NINJAONE_WEB_CLIENT_SECRET"):
        _need(e, k, WEBENV)
    return e


def _token_req(**form):
    e = _web()
    form.update(client_id=e["NINJAONE_WEB_CLIENT_ID"], client_secret=e["NINJAONE_WEB_CLIENT_SECRET"])
    return json.load(urllib.request.urlopen(
        base() + "/ws/oauth/token", urllib.parse.urlencode(form).encode(), timeout=30))


def _save(t):
    # refresh tokens rotate, so a torn write would lose the login: write aside, fsync, rename
    os.makedirs(CONF, mode=0o700, exist_ok=True)
    tmp = USERTOK + ".tmp"
    fd = os.open(tmp, os.O_WRONLY | os.O_CREAT | os.O_TRUNC, 0o600)
    with os.fdopen(fd, "w") as f:
        json.dump(t, f)
        f.flush()
        os.fsync(f.fileno())
    os.replace(tmp, USERTOK)


def auth():
    """Authorization Code flow: print the consent URL, catch the redirect, store the refresh token."""
    import http.server, secrets
    state, got = secrets.token_urlsafe(16), {}

    class H(http.server.BaseHTTPRequestHandler):
        def do_GET(self):
            u = urllib.parse.urlparse(self.path)
            if u.path != "/callback":  # e.g. the browser's /favicon.ico
                self.send_response(404); self.end_headers()
                return
            got.update(urllib.parse.parse_qsl(u.query))
            self.send_response(200); self.end_headers()
            self.wfile.write(b"nj: done, you can close this tab")
        def log_message(self, *a): pass

    url = base() + "/ws/oauth/authorize?" + urllib.parse.urlencode({
        "response_type": "code", "client_id": _web()["NINJAONE_WEB_CLIENT_ID"],
        "redirect_uri": REDIRECT, "state": state,
        "scope": "monitoring management control offline_access"})
    srv = http.server.HTTPServer(("127.0.0.1", 8765), H)
    print("Open in a browser and click Authorize:\n" + url)
    while not got:
        srv.handle_request()
    if got.get("state") != state or "code" not in got:
        raise SystemExit(f"auth failed: {got.get('error') or 'state mismatch or no code'}")
    _save(_token_req(grant_type="authorization_code", code=got["code"], redirect_uri=REDIRECT))
    print("saved", USERTOK)


def user_token():
    os.makedirs(CONF, mode=0o700, exist_ok=True)
    # two runs refreshing the same rotating token would invalidate each other's login
    with open(USERTOK + ".lock", "w") as lock:
        fcntl.flock(lock, fcntl.LOCK_EX)
        try:
            t = json.load(open(USERTOK))
            n = _token_req(grant_type="refresh_token", refresh_token=t["refresh_token"])
        except (FileNotFoundError, KeyError, urllib.error.HTTPError) as ex:
            raise SystemExit(f"no usable user token ({ex!r}): run `nj auth`")
        n.setdefault("refresh_token", t["refresh_token"])  # keep the old one if not rotated
        _save(n)
    return n["access_token"]


def run(needle, script_id, params="", apply=False):
    did = resolve(needle)
    name = get(f"/v2/device/{did}")["systemName"]
    body = {"type": "SCRIPT", "id": int(script_id), "runAs": "system"}  # lowercase: "SYSTEM" is rejected
    if params:
        body["parameters"] = params
    if not apply:
        return f"dry-run: POST /v2/device/{did}/script/run {body} on {name}"
    req = urllib.request.Request(base() + f"/v2/device/{did}/script/run", json.dumps(body).encode(),
                                 {"Authorization": "Bearer " + user_token(),
                                  "Content-Type": "application/json"}, method="POST")
    try:
        r = urllib.request.urlopen(req, timeout=60)
    except urllib.error.HTTPError as ex:
        raise SystemExit(f"{ex.code} {ex.read().decode()[:300]}")
    return (f"{r.status} dispatched to {name}: queued, not confirmed. Read the outcome from the "
            f"device's activities (output is at data.message.params.output)")


def devices():
    out, after = [], ""
    while True:
        page = get(f"/v2/devices?pageSize=1000{after}")
        out += page
        if len(page) < 1000:
            return out
        after = f"&after={page[-1]['id']}"


def resolve(needle):
    """Device id from an id, an exact name, or a substring match."""
    if str(needle).isdigit():
        return int(needle)
    n = str(needle).lower()
    hits = [d for d in devices() if n in (d.get("systemName") or "").lower()]
    if not hits:
        raise SystemExit(f"no device matching {needle!r}")
    if len(hits) > 1:
        exact = [d for d in hits if (d.get("systemName") or "").lower() == n]
        if len(exact) != 1:
            raise SystemExit("ambiguous: " + ", ".join(
                f"{d['systemName']}({d['id']})" for d in hits[:15]))
        hits = exact
    return hits[0]["id"]


def selftest():
    assert _base({"NINJAONE_REGION": "us2"}) == "https://us2.ninjarmm.com"
    assert _base({"NINJAONE_REGION": "us"}) == "https://app.ninjarmm.com"
    assert _base({"NINJAONE_BASE_URL": "http://127.0.0.1:9/"}) == "http://127.0.0.1:9"
    assert _base({"NINJAONE_BASE_URL": "https://example.com"}) == "https://example.com"
    for bad in ({}, {"NINJAONE_REGION": "mars"}, {"NINJAONE_BASE_URL": "http://example.com"},
                {"NINJAONE_BASE_URL": "ftp://127.0.0.1"},
                {"NINJAONE_REGION": "us2", "NINJAONE_BASE_URL": "https://example.com"}):
        try:
            _base(bad)
        except SystemExit:
            continue
        raise AssertionError(f"accepted {bad}")
    print("selftest ok")


if __name__ == "__main__":
    args = [a for a in sys.argv[1:] if a != "--apply"]
    cmd, rest = (args[0], args[1:]) if args else ("", [])
    if cmd == "auth" and not rest:
        auth()
    elif cmd == "scripts" and not rest:
        for x in get("/v2/automation/scripts"):
            print(f"{x['id']:>5}  {x.get('name') or '-':<50} {x.get('language')}")
    elif cmd == "run" and len(rest) in (2, 3):
        print(run(*rest, apply="--apply" in sys.argv))
    elif cmd == "selftest" and not rest:
        selftest()
    else:
        raise SystemExit(__doc__)
