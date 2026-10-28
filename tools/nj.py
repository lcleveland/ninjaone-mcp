#!/usr/bin/env python3
"""NinjaOne REST helper: running library scripts, plus per-device deep analysis.

ninjaone-mcp cannot run scripts: NinjaOne answers `script/run` with
403 user_context_required to a client-credentials (API Services) token. `run` here
uses a signed-in user's token instead (authorization code flow, refresh token kept
on disk), so the run is audited as that user.

  nj analyze PC-0001      consolidated per-device report
  nj find 0001             match devices by name
  nj get /v2/queries/disks raw GET, prints JSON
  nj grep PC-0001 bluetooth [days]   every dataset for one device, rows
                           matching a regex (days bounds patch history, default 30)
  nj move PC-0001 8 [--apply]   reassign device's locationId (dry-run unless --apply)
  nj auth                  one-time browser consent (Web app, user context)
  nj scripts               list library scripts (id, name, language)
  nj run PC-0001 <scriptId> [params] [--apply]   run script as system (dry-run unless --apply)
  nj selftest              offline self-check

Config lives in ${XDG_CONFIG_HOME:-~/.config}/ninjaone/. Every key can also come from
the environment, which wins over the files; the NINJAONE_* names match ninjaone-mcp.

  env          NINJAONE_REGION (app, us, us2, eu, ca, oc, fed) or NINJAONE_BASE_URL, and
               the API Services app, used by every command except `auth` (`run` uses
               it to look the device up): NINJAONE_CLIENT_ID, NINJAONE_CLIENT_SECRET
               or NINJAONE_CLIENT_SECRET_FILE (default client-secret), NINJAONE_SCOPES
               (default monitoring)
  webapp.env   the Web app, for `auth` and for dispatching `run`:
               NINJAONE_WEB_CLIENT_ID / NINJAONE_WEB_CLIENT_SECRET

`run` needs a user-context token: create a NinjaOne "Web Application" app (redirect
http://localhost:8765/callback, scopes monitoring management control, grants
authorization code + refresh token), put its ID and secret in webapp.env (0400), then
run `nj auth` once. The refresh token lands in user-tokens.json (0600) and rotates on
every use.
"""
import json, os, re, sys, time, urllib.request, urllib.parse, datetime

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
        kv = dict(l.rstrip("\n").split("=", 1)
                  for l in open(path) if "=" in l and not l.startswith("#"))
    except FileNotFoundError:
        kv = {}
    kv.update((k, v) for k, v in os.environ.items() if k.startswith("NINJAONE_"))
    return kv


def _need(conf, key, path):
    if not conf.get(key):
        raise SystemExit(f"{key} is not set: put it in {path} or the environment")
    return conf[key]


def _base(conf):
    if conf.get("NINJAONE_BASE_URL"):
        return conf["NINJAONE_BASE_URL"].rstrip("/")
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


def call(method, path, payload=None, _retry=True):
    """Returns (status, body_text). Never raises on an HTTP error status.

    NinjaOne keeps one live client_credentials token per app, so a token issued
    by a concurrent run invalidates ours mid-report. A 401 therefore means
    "re-authenticate", not "wrong secret" -- retry once before giving up.
    """
    global _tok
    data = json.dumps(payload).encode() if payload is not None else None
    req = urllib.request.Request(base() + path, data=data, method=method,
                                 headers={"Authorization": "Bearer " + token(),
                                          "Content-Type": "application/json"})
    try:
        r = urllib.request.urlopen(req, timeout=60)
        return r.status, r.read().decode()
    except TimeoutError:
        # the queries API stalls past the 60s read timeout now and then; one whole
        # triage sweep should not die because a single dataset hiccupped
        if _retry:
            return call(method, path, payload, _retry=False)
        raise
    except urllib.error.HTTPError as ex:
        if ex.code == 401 and _retry:
            _tok = None
            return call(method, path, payload, _retry=False)
        return ex.code, ex.read().decode()


WEBENV = CONF + "/webapp.env"
USERTOK = CONF + "/user-tokens.json"
REDIRECT = "http://localhost:8765/callback"


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
    # refresh tokens rotate, so a torn write would lose the login: write aside, then rename
    os.makedirs(CONF, mode=0o700, exist_ok=True)
    tmp = USERTOK + ".tmp"
    fd = os.open(tmp, os.O_WRONLY | os.O_CREAT | os.O_TRUNC, 0o600)
    with os.fdopen(fd, "w") as f:
        json.dump(t, f)
    os.replace(tmp, USERTOK)


def auth():
    """Authorization Code flow: print the consent URL, catch the redirect, store the refresh token."""
    import http.server, secrets
    state, got = secrets.token_urlsafe(16), {}

    class H(http.server.BaseHTTPRequestHandler):
        def do_GET(self):
            got.update(urllib.parse.parse_qsl(urllib.parse.urlparse(self.path).query))
            self.send_response(200); self.end_headers()
            self.wfile.write(b"nj: authorized, you can close this tab")
        def log_message(self, *a): pass

    url = base() + "/ws/oauth/authorize?" + urllib.parse.urlencode({
        "response_type": "code", "client_id": _web()["NINJAONE_WEB_CLIENT_ID"],
        "redirect_uri": REDIRECT, "state": state,
        "scope": "monitoring management control offline_access"})
    print("Open in a browser and click Authorize:\n" + url)
    srv = http.server.HTTPServer(("127.0.0.1", 8765), H)
    srv.handle_request()
    if got.get("state") != state or "code" not in got:
        raise SystemExit(f"auth failed: {got}")
    _save(_token_req(grant_type="authorization_code", code=got["code"], redirect_uri=REDIRECT))
    print("saved", USERTOK)


def user_token():
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
        return (f"{r.status} dispatched to {name}: queued, not confirmed. Read the outcome from the "
                f"device's activities (output is at data.message.params.output)")
    except urllib.error.HTTPError as ex:
        return f"{ex.code} {ex.read().decode()[:300]}"


def get(path):
    s, b = call("GET", path)
    if s != 200:
        raise RuntimeError(f"GET {path} -> {s} {b[:200]}")
    return json.loads(b)


def query(name, device_id, page=1000, extra=""):
    """One /v2/queries/<name> scoped to a single device."""
    d = get(f"/v2/queries/{name}?pageSize={page}&df=id%3D{device_id}{extra}")
    return d.get("results", []) if isinstance(d, dict) else d


def devices():
    return get("/v2/devices?pageSize=1000")


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


def _ts(v):
    """Timestamps arrive as epoch seconds on most fields but milliseconds on
    logged-on-users.logonTime, so normalise rather than trusting the unit."""
    if not v:
        return "-"
    if v > 1e11:
        v = v / 1000
    try:
        return datetime.datetime.fromtimestamp(v).strftime("%Y-%m-%d %H:%M")
    except (ValueError, OverflowError, OSError):
        return f"?{v}"


def _gb(v):
    return f"{v / 1024**3:.1f}G" if v else "-"


def move(needle, location_id, apply=False):
    """Reassign a device's location. Unverified field name (NinjaOne's docs are a JS
    SPA, couldn't confirm the PATCH body schema ahead of time) -- dry-run by default,
    prints the exact request; --apply sends it and prints the raw response so a
    wrong field name shows up as an error instead of a silent no-op."""
    did = resolve(needle)
    body = {"locationId": int(location_id)}
    if not apply:
        return f"DRY RUN: PATCH /v2/device/{did} {json.dumps(body)}  (pass --apply to send)"
    s, b = call("PATCH", f"/v2/device/{did}", body)
    return f"PATCH /v2/device/{did} {json.dumps(body)} -> {s}\n{b}"


def analyze(needle):
    did = resolve(needle)
    dev = get(f"/v2/device/{did}")
    out = [f"=== {dev.get('systemName')} (id {did}) ===",
           f"dns          {dev.get('dnsName')}",
           f"class        {dev.get('nodeClass')}   offline={dev.get('offline')}",
           f"last contact {_ts(dev.get('lastContact'))}   created {_ts(dev.get('created'))}"]

    for cs in query("computer-systems", did):
        out += [f"hardware     {cs.get('manufacturer')} {cs.get('model')}  sn={cs.get('serialNumber')}",
                f"memory       {_gb(cs.get('totalPhysicalMemory'))}   chassis={cs.get('chassisType')}"
                f"   domain={cs.get('domain')}"]
    for p in query("processors", did):
        out.append(f"cpu          {p.get('name')} ({p.get('numCores')}c/{p.get('numLogicalCores')}t)")
    for o in query("operating-systems", did):
        out += [f"os           {o.get('name')} {o.get('releaseId')} build {o.get('buildNumber')}",
                f"boot         {_ts(o.get('lastBootTime'))}   needsReboot={o.get('needsReboot')}"]

    for h in query("device-health", did):
        out += ["", f"health       {h.get('healthStatus')}   alerts={h.get('alertCount')}"
                    f"   activeJobs={h.get('activeJobCount')}",
                f"patches      OS pending={h.get('pendingOSPatchesCount')}"
                f" failed={h.get('failedOSPatchesCount')} /"
                f" SW pending={h.get('pendingSoftwarePatchesCount')}"
                f" failed={h.get('failedSoftwarePatchesCount')}",
                f"threats      active={h.get('activeThreatsCount')}"
                f" quarantined={h.get('quarantinedThreatsCount')}"
                f" blocked={h.get('blockedThreatsCount')}"]
        if h.get("pendingRebootReason"):
            out.append(f"rebootReason {h['pendingRebootReason']}")

    for a in query("antivirus-status", did):
        out.append(f"av           {a.get('productName')} state={a.get('productState')}"
                   f" defs={a.get('definitionStatus')}")

    vols = query("volumes", did)
    if vols:
        out.append("")
        for v in vols:
            cap, free = v.get("capacity") or 0, v.get("freeSpace") or 0
            pct = f"{100 * (cap - free) / cap:.0f}%" if cap else "-"
            flag = "  <-- LOW" if cap and free / cap < 0.10 else ""
            out.append(f"volume {v.get('driveLetter') or v.get('name'):<6} {v.get('fileSystem'):<6}"
                       f" {_gb(cap):>7} total {_gb(free):>7} free  {pct} used{flag}")

    nics = [n for n in query("network-interfaces", did) if n.get("ipAddress")]
    if nics:
        out.append("")
        for n in nics:
            # adapterName is a raw GUID; interfaceName is the human-readable one
            name = (n.get("interfaceName") or n.get("adapterName") or "")[:30]
            ips = n.get("ipAddress") or []
            v4 = [i for i in ips if ":" not in i] or ips
            out.append(f"nic    {name:<30} {', '.join(v4):<16}"
                       f" gw={n.get('defaultGateway')} status={n.get('status')}")

    users = query("logged-on-users", did)
    if users:
        out.append("")
        out.append("users        " + ", ".join(
            f"{u.get('userName')}@{_ts(u.get('logonTime'))}" for u in users))

    # auto-start services that are not running: the usual "why is this box broken" signal
    svcs = [s for s in query("windows-services", did)
            if s.get("startType") == "AUTO_START" and s.get("state") != "RUNNING"]
    if svcs:
        out.append("")
        out.append(f"stopped auto-start services ({len(svcs)}):")
        out += [f"  {s.get('name'):<28} {s.get('state')}" for s in svcs[:20]]

    pend = [p for p in query("os-patches", did) if p.get("status") != "INSTALLED"]
    if pend:
        out.append("")
        out.append(f"pending OS patches ({len(pend)}):")
        out += [f"  [{p.get('severity')}] {p.get('name')}" for p in pend[:15]]

    sw = query("software", did)
    out += ["", f"software     {len(sw)} packages installed"]
    return "\n".join(out)


DATASETS = ["computer-systems", "operating-systems", "processors", "volumes", "disks",
            "network-interfaces", "logged-on-users", "windows-services", "os-patches",
            "os-patch-installs", "software", "software-patches",
            "software-patch-installs", "device-health", "antivirus-status",
            "antivirus-threats", "raid-controllers", "raid-drives",
            "custom-fields-detailed"]


def grep(needle, pattern, days=30):
    """Rows from every dataset for one device whose JSON matches pattern.

    Symptom-driven triage: match on the symptom's vocabulary ("bluetooth", "print",
    "vpn") and let the data say which layer is broken, instead of one hand-written
    report per symptom. Returns the matching rows so callers can post-process.
    Row timestamps are inventory scan times, not live state.
    """
    did = resolve(needle)
    rx = re.compile(pattern, re.I)
    hits = []
    since = int(time.time()) - days * 86400
    for name in DATASETS:
        # os-patch-installs is unindexed without a time bound: unfiltered it hits the
        # 60s read timeout, installedAfter returns in 0.4s. days also bounds how far
        # back "recently changed" reaches, so tune it to when the symptom started.
        extra = f"&installedAfter={since}" if name == "os-patch-installs" else ""
        for row in query(name, did, extra=extra):
            if rx.search(json.dumps(row)):
                hits.append((name, row))
    s, b = call("GET", f"/v2/device/{did}/activities?pageSize=500")
    if s == 200:
        for a in json.loads(b).get("activities", []):
            if rx.search(json.dumps(a)):
                hits.append(("activities", a))
    return hits


def selftest():
    assert _base({"NINJAONE_REGION": "us2"}) == "https://us2.ninjarmm.com"
    assert _base({"NINJAONE_REGION": "us"}) == "https://app.ninjarmm.com"
    assert _base({"NINJAONE_BASE_URL": "http://127.0.0.1:9/"}) == "http://127.0.0.1:9"
    for bad in ({}, {"NINJAONE_REGION": "mars"}):
        try:
            _base(bad)
        except SystemExit:
            continue
        raise AssertionError(f"accepted {bad}")
    assert _ts(1_700_000_000) == _ts(1_700_000_000_000), "seconds and ms must agree"
    print("selftest ok")


if __name__ == "__main__":
    cmd = sys.argv[1] if len(sys.argv) > 1 else "selftest"
    if cmd == "analyze":
        print(analyze(sys.argv[2]))
    elif cmd == "find":
        n = sys.argv[2].lower()
        for d in devices():
            if n in (d.get("systemName") or "").lower():
                print(f"{d['id']:>6}  {d['systemName']:<20} {d.get('nodeClass')}"
                      f"  offline={d.get('offline')}")
    elif cmd == "grep":
        for name, row in grep(sys.argv[2], sys.argv[3],
                              int(sys.argv[4]) if len(sys.argv) > 4 else 30):
            print(f"{name:<24} {json.dumps(row, sort_keys=True)}")
    elif cmd == "get":
        s, b = call("GET", sys.argv[2])
        print(s, json.dumps(json.loads(b), indent=2) if b.startswith(("{", "[")) else b)
    elif cmd == "move":
        print(move(sys.argv[2], sys.argv[3], apply="--apply" in sys.argv))
    elif cmd == "auth":
        auth()
    elif cmd == "scripts":
        for x in get("/v2/automation/scripts"):
            print(f"{x['id']:>5}  {x['name']:<50} {x.get('language')}")
    elif cmd == "run":
        print(run(sys.argv[2], sys.argv[3],
                  next((a for a in sys.argv[4:] if a != "--apply"), ""), apply="--apply" in sys.argv))
    elif cmd == "selftest":
        selftest()
    else:
        raise SystemExit(__doc__)
