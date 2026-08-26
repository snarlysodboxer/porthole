{ lib, buildGoModule }:

let
  version = "0.2.1";
in
buildGoModule {
  pname = "porthole";
  inherit version;

  src = lib.cleanSource ../.;

  vendorHash = "sha256-nnDGX/TEgc6LlgPb9WgVta7IWow3pvrS8xr595pOPg0=";

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
