{ lib, buildGoModule }:

let
  version = "0.1.0";
in
buildGoModule {
  pname = "porthole";
  inherit version;

  src = lib.cleanSource ../.;

  vendorHash = "sha256-F72bC4UiLEoWUKJk8JI6hSrxe0JwUHPlF6xPljWcWok=";

  env.CGO_ENABLED = 0;

  ldflags = [
    "-s"
    "-w"
    "-X main.version=${version}"
  ];

  meta = {
    description = "Curated, read-only Kubernetes MCP server for AI troubleshooting";
    homepage = "https://github.com/snarlysodboxer/porthole";
    license = lib.licenses.asl20;
    mainProgram = "porthole";
  };
}
