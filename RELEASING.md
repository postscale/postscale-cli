# Releasing Postscale CLI

The source of truth is `cli/` in the Postscale monorepo. Export its committed
tracked contents into the root of `postscale/postscale-cli`; record that monorepo
revision in `SOURCE_COMMIT` in the public repository. Do not export monorepo
history or local generated files. Commit exports as
`postscale ops <ops@postscale.io>`.

1. Update the version default in `cmd/postscale/main.go` and `Makefile`, the
   installation examples, `CHANGELOG.md`, and `releases/vVERSION.md`.
2. Commit the CLI changes and export that exact subtree to the public repository.
   Keep the tested `go.mod` and `go.sum`; dependency upgrades are separate changes.
3. Run `make check` and `make release VERSION=VERSION` from a clean checkout.
   Packaging requires Python 3 and emits five archives plus `checksums.txt` in
   `dist/release/VERSION/`. It rejects dirty source, mismatched source versions,
   and existing output directories. The archive timestamps come from the source
   commit, and binaries omit checkout-specific VCS metadata.
4. Push the public source commit, then create and push the unused tag `vVERSION`.
   CI uses Go 1.25.11. The tag workflow builds the archives, verifies checksums
   and archive contents, and runs the packaged version/help/local dry-run on
   Linux, macOS, and Windows. It creates a draft release only after these pass.
5. Review the draft and verify source installation with
   `go install github.com/postscale/postscale-cli/cmd/postscale@vVERSION`.
   The public tag is already available to Go users while the release is a draft.
6. Publish the draft as stable/latest, verify anonymous downloads, and update
   the website documentation. Record the source commits, toolchain, release URL,
   and final checksum manifest in the monorepo's release record.

The release workflow uploads exactly the five archives and the checksum file.
Download those six files into an empty directory and run
`python3 scripts/check-release.py DIRECTORY` to repeat the offline packaging
checks on a supported host. No credentials or email submissions are needed.

Published tags and assets are permanent. Corrections receive a new patch
version; never move an existing version tag or replace its released binaries.

GitHub release assets are currently unsigned. SHA-256 checks detect file changes
against the published manifest; they are not platform code-signing signatures.
