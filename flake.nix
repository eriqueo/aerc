{
  description = "aerc — eriqueo fork (which-key + tweaks backlog)";

  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";
    flake-utils.url = "github:numtide/flake-utils";
  };

  outputs = { self, nixpkgs, flake-utils }:
    flake-utils.lib.eachDefaultSystem (system:
      let pkgs = import nixpkgs { inherit system; };
      in {
        # Reuse nixpkgs' aerc recipe verbatim, only swapping in the fork source.
        # Guarantees filters/, stylesets/, man pages, and the wrapper come out
        # identical to upstream. vendorHash is reused as long as go.mod/go.sum are
        # unchanged from the pinned tag; if a patch adds a Go dep the build fails
        # with a hash mismatch — paste the `got:` sha into an explicit
        # `vendorHash = "sha256-...";` line here (fakeHash dance).
        packages.default = pkgs.aerc.overrideAttrs (old: { src = self; });

        # Iterate without a rebuild: nix develop → make → ./aerc
        devShells.default = pkgs.mkShell {
          packages = with pkgs; [ go gnumake scdoc notmuch ];
        };
      });
}
