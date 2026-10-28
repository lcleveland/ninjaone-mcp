# nixpkgs.overlays = [ ninjaone-mcp.overlays.default ]; gives pkgs.ninjaone-mcp.
# Not needed for the NixOS module, which defaults to this flake's build.
final: _prev: {
  ninjaone-mcp = final.callPackage ./pkgs/ninjaone-mcp.nix { };
}
