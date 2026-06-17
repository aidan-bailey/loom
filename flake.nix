{
  description = "Loom - Manage multiple AI coding agents in parallel";

  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixpkgs-unstable";
  };

  outputs =
    { self, nixpkgs }:
    let
      supportedSystems = [
        "x86_64-linux"
        "aarch64-linux"
        "x86_64-darwin"
        "aarch64-darwin"
      ];
      forAllSystems = nixpkgs.lib.genAttrs supportedSystems;
      nixpkgsFor = forAllSystems (system: import nixpkgs { inherit system; });
      # Single source of truth: read the version constant from main.go so
      # release-prep commits only need to bump one file. The line pattern
      # matches the same `version = "X.Y.Z"` declaration that the
      # `grep -E '^[[:space:]]*version[[:space:]]*='` extractor in
      # .github/workflows/release.yml looks for — keep them in sync.
      version =
        let
          lines = builtins.filter builtins.isString (
            builtins.split "\n" (builtins.readFile ./main.go)
          );
          matchingLines = builtins.filter (
            line:
            builtins.match "[[:space:]]+version[[:space:]]+=[[:space:]]+\"[0-9.]+\"" line
            != null
          ) lines;
          extract =
            line:
            let
              m = builtins.match ".*\"([0-9.]+)\".*" line;
            in
            builtins.head m;
        in
        if matchingLines == [ ] then
          throw "flake.nix: failed to find `version = \"X.Y.Z\"` in main.go"
        else
          extract (builtins.head matchingLines);
    in
    {
      packages = forAllSystems (
        system:
        let
          pkgs = nixpkgsFor.${system};
        in
        {
          loom = pkgs.buildGoModule {
            pname = "loom";
            inherit version;
            src = ./.;

            # vendor/ is committed in-tree; use it directly rather than
            # re-deriving from go.sum through a fixed-output derivation.
            vendorHash = null;

            env.CGO_ENABLED = "0";

            ldflags = [
              "-s"
              "-w"
              "-X main.version=${version}"
            ];

            nativeBuildInputs = [ pkgs.makeWrapper ];
            nativeCheckInputs = [ pkgs.git ];

            preCheck = ''
              export HOME="$TMPDIR"
              git config --global init.defaultBranch main
              git config --global user.email "test@test.com"
              git config --global user.name "Test"
            '';

            postInstall = ''
              wrapProgram $out/bin/loom \
                --prefix PATH : ${
                  pkgs.lib.makeBinPath [
                    pkgs.tmux
                    pkgs.git
                    pkgs.gh
                  ]
                }
            '';

            meta = {
              description = "Manage multiple AI coding agents in parallel";
              homepage = "https://github.com/aidan-bailey/loom";
              license = pkgs.lib.licenses.agpl3Only;
              mainProgram = "loom";
              platforms = pkgs.lib.platforms.unix;
            };
          };

          default = self.packages.${system}.loom;
        }
      );

      devShells = forAllSystems (
        system:
        let
          pkgs = nixpkgsFor.${system};
        in
        {
          default = pkgs.mkShell {
            packages = [
              pkgs.go
              # CI uses golangci-lint v1.60.1; nixpkgs provides v2.x
              pkgs.golangci-lint
              pkgs.tmux
              pkgs.git
              pkgs.gh
            ];
          };
        }
      );
    };
}
