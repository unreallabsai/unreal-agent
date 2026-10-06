# Release maintenance

After the publishing PR is merged, push a stable `vMAJOR.MINOR.PATCH` tag whose
commit is on `main`. The Release workflow tests the code and runs the root
[`.goreleaser.yaml`](../.goreleaser.yaml) configuration using GoReleaser 2.18.2.
It builds the runner and TUI once for each macOS/Linux and ARM64/AMD64 target,
creates the release archives, and copies those same Linux runner binaries into
Docker images using [Dockerfile.release](../Dockerfile.release).

The Docker pre-hook builds and smoke-tests both image platforms before pushing.
GoReleaser uploads the archives to a draft GitHub release. Once it succeeds,
the workflow publishes that release and calls Publish Homebrew to update
`unreallabsai/homebrew-tap`. The original Dockerfile supports source builds for
development and CI.

The TUI archives include the release version, source commit and commit date in
`unreal-agent -version`. Each archive includes its executable and MIT license.
`SHA256SUMS` covers all eight archives.

The Homebrew update has three steps:

1. Verify the downloaded archives against `SHA256SUMS`.
2. Fill in the version and four platform checksums in each of the two
   [formula templates](homebrew/).
3. Commit the formulae and aliases to the tap.

The publishing workflow only accepts the latest stable release, so retries of
older releases cannot downgrade the tap. The formula templates contain the
install commands and the Homebrew tests.

`unreal-agent` installs the TUI and depends on `unreal-agent-runner`. The TUI
executable has `unreal-agent` and `uat` symlinks; the tap also accepts
`unreal-agent-tui` and `uat` as formula aliases.

## Tap credentials

The source repository's `release-env` environment contains the
`HOMEBREW_TAP_DEPLOY_KEY` Actions secret, a dedicated SSH private key. Its public
key is a write-enabled deploy key on `unreallabsai/homebrew-tap`. The Homebrew
job uses `environment: release-env` to access it directly; the caller does not
pass a repository secret. No personal access token is required. To rotate it,
create a new key, add the public deploy key to the tap, replace the secret in
`release-env`, and remove the old deploy key.

## Recovery and local verification

If tap publishing fails after a release succeeds, run the Publish Homebrew
workflow manually with that release's tag. It downloads the published assets
again and safely skips an unchanged formula. Manually published GitHub releases
also trigger that workflow; the Release workflow calls it directly because
events created with `GITHUB_TOKEN` do not start additional workflows.

`release-env` permits deployments from `v*` tags. For a manual retry, select the
release tag as the workflow's ref as well as its `release_tag` input:

```sh
gh workflow run homebrew.yml --ref v0.3.1 -f release_tag=v0.3.1
```

```sh
goreleaser check
# Local binaries, archives, checksums, and Docker images; nothing is published.
goreleaser release --snapshot --clean
# Skip Docker when only verifying the release archives.
goreleaser release --snapshot --clean --skip=docker
# Updating the tap requires a published stable release and its downloaded assets.
bash scripts/update-homebrew.sh v0.3.1 dist /path/to/homebrew-tap
brew style unreallabsai/tap/unreal-agent unreallabsai/tap/unreal-agent-runner
brew test unreallabsai/tap/unreal-agent
brew test unreallabsai/tap/unreal-agent-runner
```

The tap CI installs and tests the published packages on Linux AMD64 and macOS
ARM64 and AMD64. Release archives can also be downloaded directly from GitHub.
