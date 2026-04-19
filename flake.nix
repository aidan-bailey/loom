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
      version = "0.1.0";
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
