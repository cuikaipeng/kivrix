# Releasing

## Branch workflow

`dev` is the default development branch; `main` is the release branch.
Feature pull requests target `dev`. Pushes to either branch run checks.
Merging into `main` does not package or publish a release by itself.

1. Update both version fields on `dev`, commit and push the release changes.
2. Merge `dev` into `main` after the checks pass.
3. Create and push the matching `vX.Y.Z` tag on the merged `main` commit.
   The tag triggers checks, packaging and creation of a draft release.
4. Review the assets and notes, then publish the draft on GitHub. Published
   stable releases become visible to the in-app updater.

For example, after bumping both version fields to `0.1.1` and merging:

```sh
git switch main
git pull --ff-only origin main
python3 scripts/release.py check --tag v0.1.1
git tag -a v0.1.1 -m "Kivrix v0.1.1"
git push origin v0.1.1
git switch dev
```

## Version and checks

Keep `version` in `mygo.json` and `appVersion` in `internal/app/update.go`
in sync. `python3 scripts/release.py check` checks both; on a tag build it
also requires the tag to be exactly `v<version>`. Releases currently accept
stable `X.Y.Z` versions only. Tags with `-rc`, `-beta`, build metadata or
other suffixes are rejected before building. Manual Actions runs build the
current version without creating a release.

Before a release:

```sh
python3 scripts/release.py check --tag v0.1.0
python3 scripts/licenses.py
python3 -m unittest discover -s scripts -p 'test_*.py'
make test race vet integration
```

Integration tests require `redis-server`, `redis-cli` and Python 3 and start disposable
standalone, Sentinel and Cluster instances. They use the normal Go cache.

## Update signing key

In-app updates use an Ed25519 key pair. This is free and separate from Apple
or Microsoft code-signing certificates. The public key is in
`mygo.json` under `updates.publicKey`; the matching private key stays outside
the repository. Packaging fails if the private key is missing or does not
match, so a release cannot silently lose its update manifests.

For this project, keep the generated private key at
`~/.config/kivrix/update-keys/mygo-update.key` and back it up securely.
In GitHub **Settings → Secrets and variables → Actions**, create a repository
secret named `MYGO_UPDATER_PRIVATE_KEY` containing that file's contents.
The package jobs use this secret for both tag releases and manual builds.
Keep using the same key for future versions: already-installed apps trust
the corresponding public key. Never commit the private key or print it in
build logs.

For local packaging, set the environment variable first:

```sh
export MYGO_UPDATER_PRIVATE_KEY="$(cat ~/.config/kivrix/update-keys/mygo-update.key)"
```

Forks need their own pair, generated with `go tool mygo keygen -o <private-dir>`,
and must replace both `updates.publicKey` and `updates.github` in `mygo.json`.

## Package locally

Packaging needs Go and Python 3.9+ (standard library only). Run on the
matching OS for native installers. `scripts/package.sh` uses the pinned
MyGo version, builds from a temporary source snapshot, verifies the outputs,
and stages the files to publish under `build/release/<os>/`.

macOS packaging also uses Swift from Apple's Command Line Tools to render
the app and DMG volume icon with rounded corners and transparent margins.
The original logo stays unchanged; Windows and Linux use the original artwork.

```sh
# No certificates needed: macOS defaults to a universal, ad-hoc-signed DMG.
make package

# Linux: both supported architectures, plus one shared install.sh.
PLATFORM=linux/amd64,linux/arm64 ./scripts/package.sh

# Windows: run in Git Bash. MyGo downloads NSIS when needed.
PLATFORM=windows/amd64 ./scripts/package.sh

# Custom output directory (contains raw packages and release/<os>/).
./scripts/package.sh -o build/local
```

The wrapper derives macOS language declarations and `.lproj` resources from
`internal/app/locales/*.json` before signing, and keeps generated configuration
out of the working tree. Adding a language requires no second registration
list. `go tool mygo build` remains useful for development, but use this
wrapper for distributable packages and their normalized filenames.

The checks cover macOS bundle version, languages, universal architectures,
licenses, code signature and DMG integrity; Linux ELF architecture, DEB
version/architecture/dependencies, archive licenses and install-script syntax;
and Windows executable architecture/version, licenses and NSIS output.
Every bundled notice is compared byte-for-byte with the reviewed source set,
including the in-app update archives. A lone top-level `LICENSE` is not enough.
When dependencies change, review their exact licenses and run
`python3 scripts/licenses.py --write` before packaging; see
[the audit record](OPEN_SOURCE_AUDIT.md).

## Install without certificates

Both tag releases and manual builds work without OS signing certificates;
the update-signing secret above is required. macOS uses ad-hoc signing
(`codesign -s -`), which checks bundle integrity but does not establish a
trusted publisher or include Apple
notarization. Windows installers are unsigned. Linux DEB and tarball downloads
do not require a commercial code-signing certificate.

- **macOS:** copy Kivrix to Applications and try opening it. If macOS blocks it
  as an unidentified or unverified developer, and you trust the download, open
  **System Settings → Privacy & Security → Open Anyway**, then confirm.
  See [Apple's instructions](https://support.apple.com/en-gb/102445).
- **Windows:** SmartScreen may show **Windows protected your PC** or an unknown
  publisher warning. If available, use **More info → Run anyway** only for a
  download you trust. Enterprise policies and Smart App Control can block
  unsigned apps without that option. See [Microsoft's explanation](https://learn.microsoft.com/en-us/windows/apps/package-and-deploy/smartscreen-reputation).
- **Linux:** install the DEB with the distribution's package manager, or use
  the tarball and accompanying `install.sh` as described below. This is direct
  download distribution; an APT repository would have its own repository
  signing setup.

Managed Macs may also disallow the per-app exception. These packages therefore
cannot promise warning-free installation or compatibility with every device
policy. Self-signed publisher certificates do not solve that trust problem.
`SHA256SUMS` detects changed downloads; it does not provide OS publisher trust.

## Optional certificate signing

OS certificate signing is optional and is not configured in GitHub Actions.
If certificates become available later, local `--signed` builds require them and fail when
credentials are missing instead of silently producing unsigned packages.
For macOS, store a notarytool profile in your keychain and supply a
Developer ID Application identity:

```sh
KIVRIX_NOTARY_PROFILE=kivrix-notary \
  ./scripts/package.sh --signed --sign 'Developer ID Application: ...'
```

`KIVRIX_KEYCHAIN` optionally selects the keychain holding that profile.
The wrapper notarizes and staples the DMG and checks `stapler validate`
and Gatekeeper's assessment of the app. Default builds skip notarization.

For Windows, supply `KIVRIX_WINDOWS_CERTIFICATE` as the path to a PFX file
and `MYGO_WINDOWS_CERTIFICATE_PASSWORD` as its password, then run
`./scripts/package.sh --signed`. MyGo signs the app and installer with
an RFC 3161 timestamp. The wrapper verifies their Authenticode signatures.
MyGo's `macos.signingIdentity`, `macos.notarize`, `windows.certificate` and
`windows.signCommand` configuration also works for local signed builds.
Keep private keys, certificates and passwords outside the repository.

## Published assets and updates

For example, a new `0.1.1` release contains:

- `kivrix-0.1.1-macOS.dmg`
- `kivrix-0.1.1-windows-amd64.exe`
- `kivrix-0.1.1-macOS.tar.gz`
- `kivrix-0.1.1-windows-amd64.tar.gz`
- `kivrix-0.1.1-linux-amd64.deb`
- `kivrix-0.1.1-linux-arm64.deb`
- `kivrix-0.1.1-linux-amd64.tar.gz`
- `kivrix-0.1.1-linux-arm64.tar.gz`
- `install.sh`
- `update-darwin-universal.json`
- `update-windows-amd64.json`
- `update-linux-amd64.json`
- `update-linux-arm64.json`
- `SHA256SUMS`

The `macOS` packages support both Apple Silicon and Intel Macs. The updater's
`update-darwin-universal.json` name stays unchanged for installed apps; its URL
points to the renamed archive. Renaming leaves the signed archive bytes intact.
The published `v0.1.0` assets keep their original names.

`install.sh` is a separate download, shared by both Linux architectures.
Download it alongside the matching tarball and run
`sh install.sh ./kivrix-0.1.1-linux-amd64.tar.gz` (or the arm64 tarball).
DEB users can install with their package manager. Native Linux packages
require GTK, without pulling in WebKitGTK.

After collecting all three platform artifact directories under `artifacts/`:

```sh
python3 scripts/release.py verify artifacts
KIVRIX_RELEASE_ASSETS="$PWD/artifacts" CGO_ENABLED=0 \
  go test -count=1 ./internal/app -run '^TestPackagedUpdateAssets$'
```

The first command rejects missing, duplicate, empty or unexpected assets and
writes `SHA256SUMS` using published basenames. The second checks each update
manifest's version, URL and size, then verifies the archive's Ed25519 signature
using the public key embedded in the app. Users can download assets into one
directory and run
`shasum -a 256 -c SHA256SUMS` to check them.

Update checks run once at startup by default; an existing explicit opt-out
is preserved. Users can also check from General settings, the top-left
connection menu, or the native application menu. A new version appears in
the main window's top-right corner. Checks only fetch release metadata;
downloads require a click.

The app uses the [MyGo updater](https://github.com/egoist/mygo/blob/v0.6.2/docs/updates.md),
as [Kero](https://github.com/egoist/kero) does. It downloads the platform's
archive, verifies its signed SHA-256 digest, and prepares the replacement
in place. HTTPS redirects cannot downgrade to HTTP. The running process
keeps its current version; the new version runs at the next launch. After
preparation, **Install and restart** lets the user activate it immediately,
with a save/discard/cancel prompt for unsaved edits. No manual installer is
opened, and downloaded temporary files are cleaned up.

Self-updating requires a packaged release in a writable location: a copied
macOS `.app`, a per-user Windows installation, or the Linux tarball installed
with `install.sh`. Development builds, apps still on a read-only DMG, and
system-owned installations cannot replace themselves. Linux DEB users update
through their package manager. Settings explains when in-app updates are
unavailable. No OS signing certificate is needed for this update mechanism.

## GitHub Actions

| Trigger | Checks | Installers | Draft release |
|---|---|---|---|
| PR / push to `dev` or `main` | Version, release-script tests, unit/UI tests, Go/MyGo vet, five desktop builds, real Redis integration and race tests | No | No |
| Push `vX.Y.Z` matching the source | Same checks | macOS ad-hoc, Windows unsigned, Linux packages; signed update archives | Yes |
| Manual run | Same checks | Same packages; update-signing secret required | No |

Tag runs reject an already published version before building and check it
again immediately before uploading. Draft reruns may replace draft assets;
published releases require a new version. Enable GitHub immutable releases
as an additional repository-level guarantee against changing published assets.

All three package jobs must succeed. The release job verifies the complete
asset set, writes checksums, tests updater compatibility, and creates or
refreshes a **draft** with the signing status and installation notes. Review
the notes and perform installation/launch checks
on the supported systems before publishing the draft on GitHub. Drafts are
not offered by the in-app stable updater.

Only the release job has `contents: write`. Ordinary checks cancel stale runs;
manual and tag builds are allowed to finish. Go caches remain enabled. Actions
artifacts expire after seven days; assets uploaded into a release remain there.
