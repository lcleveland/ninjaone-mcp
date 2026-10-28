# nj: stdlib-only Python CLI. Runs library scripts with a signed-in user's token,
# which the ninjaone-mcp `scripts` capability cannot (403 user_context_required).
{
  lib,
  stdenvNoCC,
  python3,
}:

stdenvNoCC.mkDerivation {
  pname = "nj";
  version = "0.1.0";

  src = lib.fileset.toSource {
    root = ../tools;
    fileset = ../tools/nj.py;
  };

  buildInputs = [ python3 ]; # patchShebangs target
  dontBuild = true;

  installPhase = ''
    runHook preInstall
    install -Dm755 nj.py $out/bin/nj
    runHook postInstall
  '';

  # Offline: checks region/config parsing only, never touches a tenant.
  doInstallCheck = true;
  installCheckPhase = ''
    $out/bin/nj selftest
  '';

  meta = {
    description = "NinjaOne REST helper: run library scripts as a signed-in user, per-device analysis";
    homepage = "https://github.com/lcleveland/ninjaone-mcp";
    license = lib.licenses.mit;
    platforms = lib.platforms.linux;
    mainProgram = "nj";
  };
}
