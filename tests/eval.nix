# Module evaluation checks: no VM, just the generated unit.
{
  pkgs,
  self,
  lib,
}:
let
  evalModule =
    module:
    (lib.nixosSystem {
      inherit (pkgs.stdenv.hostPlatform) system;
      modules = [
        self.nixosModules.ninjaone-mcp
        {
          services.ninjaone-mcp.package = lib.mkForce (pkgs.writeShellScriptBin "ninjaone-mcp" "exit 0");
          boot.loader.grub.enable = false;
          fileSystems."/" = {
            device = "none";
            fsType = "tmpfs";
          };
          system.stateVersion = lib.trivial.release;
        }
        module
      ];
    }).config;

  failed = config: map (a: a.message) (builtins.filter (a: !a.assertion) config.assertions);

  base = extra: {
    services.ninjaone-mcp = {
      enable = true;
      region = "us2";
      clientId = "client-id-123";
      clientSecretFile = "/persist/secrets/ninjaone-client-secret";
    }
    // extra;
  };

  # Evaluates cleanly and the generated unit passes the grep checks.
  unitCheck =
    name: extra: checks:
    let
      config = evalModule (base extra);
      broken = failed config;
      svc = config.systemd.services.ninjaone-mcp;
    in
    assert broken == [ ] || throw "${name}: ${lib.concatStringsSep "; " broken}";
    pkgs.runCommand "ninjaone-mcp-${name}" { } ''
      cat > cmd <<'EOF'
      ${svc.serviceConfig.ExecStart}
      EOF
      cat > env <<'EOF'
      ${lib.concatStringsSep "\n" (lib.mapAttrsToList (k: v: "${k}=${v}") svc.environment)}
      EOF
      cat > creds <<'EOF'
      ${lib.concatStringsSep "\n" svc.serviceConfig.LoadCredential}
      EOF
      check() { grep -qF -- "$1" "$2" || { echo "missing from $2: $1"; cat "$2"; exit 1; }; }
      refute() { if grep -qF -- "$1" "$2"; then echo "unexpected in $2: $1"; cat "$2"; exit 1; fi; }
      ${checks}
      touch $out
    '';

  # Evaluation must trip an assertion containing `want`.
  mustFail =
    name: extra: want:
    let
      broken = failed (evalModule (base extra));
    in
    assert
      lib.any (lib.hasInfix want) broken
      || throw "${name}: expected assertion '${want}', got: ${toString broken}";
    pkgs.runCommand "ninjaone-mcp-${name}" { } "touch $out";

  warns =
    name: extra: want:
    let
      config = evalModule (base extra);
    in
    assert lib.any (lib.hasInfix want) config.warnings || throw "${name}: no warning '${want}'";
    pkgs.runCommand "ninjaone-mcp-${name}" { } "touch $out";
in
{
  module-eval = unitCheck "module-eval" { } ''
    check "--http" cmd
    check "--addr 127.0.0.1:8233" cmd
    check "--region us2" cmd
    check "--client-id client-id-123" cmd
    check "--tool-groups core,inventory,alerts,patching,docs,ticketing,backup" cmd
    check "client-secret:/persist/secrets/ninjaone-client-secret" creds
    refute "/persist/secrets/ninjaone-client-secret" cmd
    refute "ninjaone-client-secret" env
    refute "--allow-" cmd
    refute "--scopes" cmd
  '';

  module-full =
    unitCheck "module-full"
      {
        region = null;
        baseUrl = "https://us2.ninjarmm.com";
        allowTickets = true;
        allowCustomFields = true;
        allowDeviceActions = true;
        scopes = [
          "monitoring"
          "management"
        ];
        listenAddress = "0.0.0.0";
        bearerTokenFile = "/run/secrets/bearer";
        toolGroups = [ "inventory" ];
        maxBulk = 10;
      }
      ''
        check "--base-url https://us2.ninjarmm.com" cmd
        refute "--region" cmd
        check "--allow-tickets" cmd
        check "--allow-custom-fields" cmd
        check "--allow-device-actions" cmd
        refute "--allow-scripts" cmd
        refute "--allow-device-admin" cmd
        check "--scopes 'monitoring management'" cmd
        check "--addr 0.0.0.0:8233" cmd
        check "--max-bulk 10" cmd
        check "http-auth-token:/run/secrets/bearer" creds
        refute "/run/secrets/bearer" cmd
      '';

  module-scripts-warn = warns "scripts-warn" { allowScripts = true; } "allowScripts is true";
  module-admin-warn = warns "admin-warn" { allowDeviceAdmin = true; } "allowDeviceAdmin is true";

  module-no-secret = mustFail "no-secret" { clientSecretFile = null; } "clientSecretFile";
  module-secret-in-store = mustFail "secret-in-store" {
    clientSecretFile = "${builtins.storeDir}/abc-secret";
  } "world-readable";
  module-open-listener = mustFail "open-listener" {
    listenAddress = "0.0.0.0";
  } "without bearerTokenFile";
  module-region-and-url = mustFail "region-and-url" {
    baseUrl = "https://x.ninjarmm.com";
  } "exactly one of region and baseUrl";
  module-no-region = mustFail "no-region" { region = null; } "exactly one of region and baseUrl";
}
