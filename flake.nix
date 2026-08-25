{
  description = "porthole - a curated, read-only Kubernetes MCP server for AI troubleshooting";

  inputs.nixpkgs.url = "github:NixOS/nixpkgs/nixpkgs-unstable";

  outputs =
    { self, nixpkgs }:
    let
      systems = [
        "x86_64-linux"
        "aarch64-linux"
        "x86_64-darwin"
        "aarch64-darwin"
      ];
      forAllSystems = f: nixpkgs.lib.genAttrs systems (system: f nixpkgs.legacyPackages.${system});
    in
    {
      overlays.default = final: prev: { porthole = final.callPackage ./nix/package.nix { }; };

      packages = forAllSystems (pkgs: rec {
        porthole = pkgs.callPackage ./nix/package.nix { };
        default = porthole;
      });

      devShells = forAllSystems (pkgs: {
        default = pkgs.mkShell {
          packages = with pkgs; [
            go
            gopls
            golangci-lint
          ];
        };
      });

      # `nix flake check` = build + `go test ./...` (buildGoModule's default
      # checkPhase) + golangci-lint.
      checks = forAllSystems (
        pkgs:
        let
          porthole = pkgs.callPackage ./nix/package.nix { };
        in
        {
          build = porthole;
          lint =
            pkgs.runCommand "porthole-lint"
              {
                nativeBuildInputs = [
                  pkgs.go
                  pkgs.golangci-lint
                ];
              }
              ''
                cp -r ${self} src
                chmod -R u+w src
                cd src
                ln -s ${porthole.goModules} vendor
                export HOME=$TMPDIR
                export CGO_ENABLED=0
                export GOFLAGS=-mod=vendor
                export GOCACHE=$TMPDIR/gocache
                export GOLANGCI_LINT_CACHE=$TMPDIR/lintcache
                golangci-lint run ./...
                touch $out
              '';
        }
      );
    };
}
