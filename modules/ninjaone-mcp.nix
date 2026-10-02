{
  config,
  lib,
  pkgs,
  ...
}:
let
  inherit (lib)
    mkIf
    mkOption
    mkEnableOption
    types
    optional
    literalExpression
    ;
  cfg = config.services.ninjaone-mcp;
  groups = [
    "core"
    "inventory"
    "alerts"
    "patching"
    "docs"
    "ticketing"
    "backup"
  ];
  # option name -> --allow-<capability>; see docs/adr/0001.
  capabilities = {
    allowTickets = "tickets";
    allowDocumentation = "documentation";
    allowCustomFields = "custom-fields";
    allowDeviceMaintenance = "device-maintenance";
    allowDeviceActions = "device-actions";
    allowScripts = "scripts";
    allowDeviceAdmin = "device-admin";
  };
  capabilityHelp = {
    allowTickets = "create, update and comment on NinjaOne tickets";
    allowDocumentation = "write documents, templates, knowledge base articles, checklists, related items and tags";
    allowCustomFields = "set custom field values on devices, organizations, locations and end users";
    allowDeviceMaintenance = "maintenance mode, alert reset and patch scans";
    allowDeviceActions = "reboots, Windows service control, patch installs, policy override reset and backup actions";
    allowScripts = "run scripts and built-in actions on devices, often as SYSTEM";
    allowDeviceAdmin = "edit, move, approve and decommission devices, and create or edit organizations and locations";
  };
  isLoopback = a: a == "::1" || a == "localhost" || lib.hasPrefix "127." a;
  inStore = p: p != null && lib.hasPrefix builtins.storeDir p;
  staticUser = cfg.user != null;

  args = [
    "--http"
    "--addr"
    (
      if lib.hasInfix ":" cfg.listenAddress then
        "[${cfg.listenAddress}]:${toString cfg.port}"
      else
        "${cfg.listenAddress}:${toString cfg.port}"
    )
    "--path"
    cfg.path
    "--tool-groups"
    (lib.concatStringsSep "," cfg.toolGroups)
    "--max-bulk"
    (toString cfg.maxBulk)
    "--request-timeout"
    cfg.requestTimeout
    "--log-level"
    cfg.logLevel
  ]
  ++ (
    if cfg.baseUrl != null then
      [
        "--base-url"
        cfg.baseUrl
      ]
    else
      [
        "--region"
        cfg.region
      ]
  )
  ++ lib.optionals (cfg.clientId != null) [
    "--client-id"
    cfg.clientId
  ]
  ++ lib.optionals (cfg.scopes != [ ]) [
    "--scopes"
    (lib.concatStringsSep " " cfg.scopes)
  ]
  ++ lib.concatMap (o: optional cfg.${o} "--allow-${capabilities.${o}}") (lib.attrNames capabilities)
  ++ cfg.extraArgs;
in
{
  options.services.ninjaone-mcp = {
    enable = mkEnableOption "the NinjaOne MCP server (streamable HTTP)";

    package = mkOption {
      type = types.package;
      default = pkgs.callPackage ../pkgs/ninjaone-mcp.nix { };
      defaultText = literalExpression "pkgs.ninjaone-mcp";
      description = "The ninjaone-mcp package.";
    };

    region = mkOption {
      type = types.nullOr (
        types.enum [
          "app"
          "us"
          "us2"
          "eu"
          "ca"
          "oc"
          "fed"
        ]
      );
      default = null;
      example = "us2";
      description = "NinjaOne region the tenant lives on. Set exactly one of region and baseUrl.";
    };
    baseUrl = mkOption {
      type = types.nullOr types.str;
      default = null;
      example = "https://us2.ninjarmm.com";
      description = "NinjaOne base URL, instead of region. Must be https unless the host is loopback.";
    };

    clientId = mkOption {
      type = types.nullOr types.str;
      default = null;
      description = "Client ID of the NinjaOne API Services (machine-to-machine) application. Not secret. Set exactly one of clientId and clientIdFile.";
    };
    clientIdFile = mkOption {
      type = types.nullOr types.str;
      default = null;
      example = "/persist/secrets/ninjaone-client-id";
      description = "Runtime path to a file holding the client ID, instead of clientId. Passed via systemd `LoadCredential`.";
    };
    clientSecretFile = mkOption {
      type = types.nullOr types.str;
      default = null;
      example = "/persist/secrets/ninjaone-client-secret";
      description = ''
        Runtime path to a file holding the API Services client secret. Passed via
        systemd `LoadCredential`, so it never enters the Nix store, argv or the
        environment. Use sops-nix, agenix or a root-owned 0400 file.
      '';
    };
    scopes = mkOption {
      type = types.listOf (
        types.enum [
          "monitoring"
          "management"
          "control"
        ]
      );
      default = [ ];
      description = "OAuth scopes to request. Empty requests every scope configured on the app. Every write needs management.";
    };

    maxBulk = mkOption {
      type = types.ints.positive;
      default = 50;
      description = "Maximum records in one batched write.";
    };
    toolGroups = mkOption {
      type = types.listOf (types.enum groups);
      default = groups;
      description = "Tool groups to register. `core` is always on.";
    };

    listenAddress = mkOption {
      type = types.str;
      default = "127.0.0.1";
      description = "Address to listen on. A non-loopback address requires bearerTokenFile.";
    };
    port = mkOption {
      type = types.port;
      default = 8233;
      description = "TCP port for the MCP endpoint.";
    };
    path = mkOption {
      type = types.str;
      default = "/mcp";
      description = "URL path of the MCP endpoint.";
    };
    bearerTokenFile = mkOption {
      type = types.nullOr types.str;
      default = null;
      example = "/run/secrets/ninjaone-mcp-bearer";
      description = ''
        Runtime path to a shared secret HTTP clients must send as
        `Authorization: Bearer <token>`. Required for a non-loopback listener.
        `/healthz` stays open.
      '';
    };
    openFirewall = mkOption {
      type = types.bool;
      default = false;
      description = "Open port in the firewall.";
    };

    requestTimeout = mkOption {
      type = types.str;
      default = "30s";
      description = "Timeout for a single NinjaOne request, as a Go duration.";
    };
    logLevel = mkOption {
      type = types.enum [
        "debug"
        "info"
        "warn"
        "error"
      ];
      default = "info";
      description = "Log verbosity. Writes are always audit-logged at info.";
    };

    user = mkOption {
      type = types.nullOr types.str;
      default = null;
      description = "Run as this user instead of a systemd DynamicUser.";
    };
    group = mkOption {
      type = types.nullOr types.str;
      default = cfg.user;
      defaultText = literalExpression "config.services.ninjaone-mcp.user";
      description = "Group to run as when user is set.";
    };

    installCli = mkOption {
      type = types.bool;
      default = cfg.enable;
      defaultText = literalExpression "config.services.ninjaone-mcp.enable";
      description = ''
        Put the binary on the system PATH. Set this with enable = false on a
        workstation that only spawns the server over stdio from an MCP client.
      '';
    };
    extraArgs = mkOption {
      type = types.listOf types.str;
      default = [ ];
      description = "Extra command-line arguments. Never put a secret here.";
    };
    environment = mkOption {
      type = types.attrsOf types.str;
      default = { };
      description = "Extra environment variables. Never put a secret here.";
    };
  }
  // lib.mapAttrs (
    o: help:
    mkOption {
      type = types.bool;
      default = false;
      description = "Enable the `${capabilities.${o}}` write capability: ${help}.";
    }
  ) capabilityHelp;

  config = lib.mkMerge [
    (mkIf cfg.installCli { environment.systemPackages = [ cfg.package ]; })

    (mkIf cfg.enable {
      assertions = [
        {
          assertion = (cfg.region == null) != (cfg.baseUrl == null);
          message = "services.ninjaone-mcp: set exactly one of region and baseUrl.";
        }
        {
          assertion = (cfg.clientId == null) != (cfg.clientIdFile == null);
          message = "services.ninjaone-mcp: set exactly one of clientId and clientIdFile.";
        }
        {
          assertion = cfg.clientIdFile == null || lib.hasPrefix "/" cfg.clientIdFile;
          message = "services.ninjaone-mcp.clientIdFile must be an absolute path.";
        }
        {
          assertion = cfg.clientSecretFile != null && lib.hasPrefix "/" cfg.clientSecretFile;
          message = "services.ninjaone-mcp.clientSecretFile must be an absolute runtime path to the API client secret.";
        }
        {
          assertion = !(inStore cfg.clientSecretFile) && !(inStore cfg.bearerTokenFile);
          message = "services.ninjaone-mcp: secret files must not live in ${builtins.storeDir}, which is world-readable. Use sops-nix, agenix or a root-owned 0400 file.";
        }
        {
          assertion = isLoopback cfg.listenAddress || cfg.bearerTokenFile != null;
          message = "services.ninjaone-mcp.listenAddress is ${cfg.listenAddress} (not loopback) without bearerTokenFile; the server refuses to start unauthenticated on a network address.";
        }
        {
          assertion = cfg.bearerTokenFile == null || lib.hasPrefix "/" cfg.bearerTokenFile;
          message = "services.ninjaone-mcp.bearerTokenFile must be an absolute path.";
        }
        {
          assertion = !staticUser || cfg.group != null;
          message = "services.ninjaone-mcp.group must be set when user is set.";
        }
        {
          assertion = lib.hasPrefix "/" cfg.path;
          message = "services.ninjaone-mcp.path must begin with a slash.";
        }
      ];

      warnings =
        optional cfg.allowScripts "services.ninjaone-mcp.allowScripts is true: a model can run scripts on managed devices, often as SYSTEM."
        ++ optional cfg.allowDeviceAdmin "services.ninjaone-mcp.allowDeviceAdmin is true: a model can move, reject and decommission devices and edit organizations."
        ++
          optional (cfg.openFirewall && isLoopback cfg.listenAddress)
            "services.ninjaone-mcp.openFirewall has no effect while listenAddress is loopback (${cfg.listenAddress}).";

      users = mkIf staticUser {
        users.${cfg.user} = {
          isSystemUser = true;
          group = cfg.group;
        };
        groups.${cfg.group} = { };
      };

      systemd.services.ninjaone-mcp = {
        description = "NinjaOne MCP server";
        documentation = [ "https://github.com/lcleveland/ninjaone-mcp" ];
        wantedBy = [ "multi-user.target" ];
        after = [ "network-online.target" ];
        wants = [ "network-online.target" ];
        environment = cfg.environment;
        serviceConfig = {
          Type = "exec";
          ExecStart = "${lib.getExe cfg.package} ${lib.escapeShellArgs args}";
          Restart = "on-failure";
          RestartSec = 5;
          LoadCredential = [
            "client-secret:${cfg.clientSecretFile}"
          ]
          ++ optional (cfg.clientIdFile != null) "client-id:${cfg.clientIdFile}"
          ++ optional (cfg.bearerTokenFile != null) "http-auth-token:${cfg.bearerTokenFile}";

          User = mkIf staticUser cfg.user;
          Group = mkIf staticUser cfg.group;
          DynamicUser = !staticUser;

          AmbientCapabilities = [ "" ];
          CapabilityBoundingSet = [ "" ];
          DevicePolicy = "closed";
          LockPersonality = true;
          MemoryDenyWriteExecute = true;
          NoNewPrivileges = true;
          PrivateDevices = true;
          PrivateTmp = true;
          PrivateUsers = true;
          ProcSubset = "pid";
          ProtectClock = true;
          ProtectControlGroups = true;
          ProtectHome = true;
          ProtectHostname = true;
          ProtectKernelLogs = true;
          ProtectKernelModules = true;
          ProtectKernelTunables = true;
          ProtectProc = "invisible";
          ProtectSystem = "strict";
          RemoveIPC = true;
          # AF_NETLINK: Go's pure resolver reads interface addresses.
          RestrictAddressFamilies = [
            "AF_INET"
            "AF_INET6"
            "AF_NETLINK"
          ];
          RestrictNamespaces = true;
          RestrictRealtime = true;
          RestrictSUIDSGID = true;
          SystemCallArchitectures = "native";
          SystemCallFilter = [
            "@system-service"
            "~@privileged"
            "~@resources"
          ];
          UMask = "0077";
          SocketBindDeny = "any";
          SocketBindAllow = "tcp:${toString cfg.port}";
        };
      };

      networking.firewall.allowedTCPPorts = mkIf cfg.openFirewall [ cfg.port ];
    })
  ];
}
