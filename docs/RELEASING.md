# Releasing and updates

Build from source (the repo is not tagged yet, so `go install …@latest` will
work only once a release is published):

```sh
git clone https://github.com/rutvikchandla3/paircli
cd paircli
go build ./cmd/paircli
./paircli --help
```

Once tagged:

```sh
go install github.com/rutvikchandla3/paircli/cmd/paircli@latest
```


The Pair plugin does not clone this repository or require Go on a user's
machine. It downloads the platform binary named in the public release manifest
at `https://github.com/rutvikchandla3/paircli/releases/latest/download/latest.json`,
verifies its SHA-256, and then runs `paircli update` on later PRs. The CLI
repeats the manifest and checksum verification before replacing itself.

Build the four GitHub Release artifacts and manifest for a release with:

```sh
bash scripts/build-release-artifacts.sh 0.2.0
```

This writes the immutable binary set and `SHA256SUMS` to
`dist/paircli/0.2.0/`, plus the mutable pointer
`dist/paircli/latest.json`. The release workflow uploads them to the GitHub
Release for tag `v0.2.0`: its binaries resolve from
`/rutvikchandla3/paircli/releases/download/v0.2.0/paircli-<platform>`, while
GitHub's latest-release URL resolves `latest.json`. This preserves rollback
and pinned-release options while updating only the small `latest.json`
pointer. Set `PAIRCLI_RELEASE_BASE_URL` when producing artifacts for an
approved staging endpoint. Local testing can point both the plugin bootstrap
(`PAIRCLI_RELEASE_MANIFEST_URL`) and CLI updater
(`PAIRCLI_UPDATE_MANIFEST_URL`) at a local manifest; set
`PAIRCLI_ALLOW_INSECURE_DOWNLOAD=1` only for `localhost` HTTP.

The manifest is schema-versioned and contains `minimum_version`,
`min_plugin_version`, and `revoked`. The plugin refuses a release that requires
a newer plugin, rather than failing later with an incompatible CLI interface.
The CLI refuses a revoked latest release and skips updates from locally-built
development versions. The plugin owns only the first bootstrap download. Once
a binary is present, `paircli update` owns all later manifest checks and
replacements; Homebrew installations remain managed by `brew upgrade paircli`.

Pushing a `vX.Y.Z` tag runs `.github/workflows/release.yml`: it tests the
module, builds the same immutable artifact set, creates SHA-256 checksums,
attests the build, and publishes a GitHub Release containing the manifest and
all versioned binaries.

For enterprise mirrors or pinned releases, set both
`PAIRCLI_RELEASE_MANIFEST_URL` (plugin bootstrap) and
`PAIRCLI_UPDATE_MANIFEST_URL` (CLI updates) to the approved HTTPS manifest URL.
An offline install can place an approved executable at the plugin's
`paircli/bin/paircli` path before approval; the plugin then skips bootstrap and
the CLI remains responsible for future updates.

The production manifest endpoint must return `Content-Type: application/json`.
`paircli update` has a 30-second network timeout and allows only GitHub's
expected HTTPS redirects from `github.com` to its release-asset hosts; it
rejects other cross-origin redirects as release configuration errors. GitHub
serves release assets as `application/octet-stream`, which Pair accepts only
for that pinned GitHub Release redirect.
