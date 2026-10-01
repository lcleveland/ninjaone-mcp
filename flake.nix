{
  description = "MCP server for the NinjaOne public API v2, packaged with a NixOS module";

  inputs.nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";

  outputs =
    { self, nixpkgs }:
    let
      inherit (nixpkgs) lib;
      forAllSystems = lib.genAttrs [
        "x86_64-linux"
        "aarch64-linux"
      ];
      pkgsFor = system: nixpkgs.legacyPackages.${system};
    in
    {
      packages = forAllSystems (system: rec {
        ninjaone-mcp = (pkgsFor system).callPackage ./pkgs/ninjaone-mcp.nix { };
        default = ninjaone-mcp;
      });

      checks = forAllSystems (system: {
        # Runs the Go test suite in checkPhase.
        package = self.packages.${system}.ninjaone-mcp;
      });

      formatter = forAllSystems (system: (pkgsFor system).nixfmt-tree);

      devShells = forAllSystems (
        system:
        let
          pkgs = pkgsFor system;
        in
        {
          default = pkgs.mkShell {
            packages = [
              pkgs.go
              pkgs.gopls
              pkgs.gotools
              pkgs.nixfmt
              pkgs.jq
            ];
            env.CGO_ENABLED = "0";
          };
        }
      );
    };
}
