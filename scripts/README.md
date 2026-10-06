# Release maintenance

The root [`.goreleaser.yaml`](../.goreleaser.yaml) defines the binaries, archives,
Docker image, and Homebrew cask. [Release](../.github/workflows/release.yml) runs
that configuration through the official GoReleaser action.

After this PR is merged, push a stable `vMAJOR.MINOR.PATCH` tag on `main`:

```sh
git tag v0.3.1
git push origin v0.3.1
```

The workflow tests the code, then GoReleaser:

1. Builds the runner and TUI once for each macOS/Linux and ARM64/AMD64 target.
2. Packages both executables and the MIT license in four release archives.
3. Smoke-tests and publishes Docker images using the same Linux runner binaries.
4. Publishes the GitHub release, then updates `Casks/unreal-agent.rb` in
   `unreallabsai/homebrew-tap`.

One `brew install unreallabsai/tap/unreal-agent` installs both binaries.
`unreal-agent` and `uat` launch `unreal-agent-tui`. The cask clears quarantine
only from its own binaries on macOS because these builds are not notarized.

`Dockerfile.release` copies prebuilt binaries. The original Dockerfile supports
source builds for development and CI.

## Credentials

The `release-env` environment supplies `DOCKERHUB_TOKEN` and
`HOMEBREW_TAP_DEPLOY_KEY`; the latter is an SSH private key whose public key is a
write-enabled deploy key on the tap. `DOCKERHUB_USERNAME` is an environment
variable. GitHub release uploads use the workflow's `GITHUB_TOKEN`.

The repository's Actions allowlist must permit `goreleaser/goreleaser-action@*`.
The workflow pins that action to a commit and GoReleaser to version 2.18.2.

## Local verification

```sh
goreleaser check
# Build binaries, archives, Docker images, and the cask locally; publish nothing.
goreleaser release --snapshot --clean
# For packaging checks without Docker:
goreleaser release --snapshot --clean --skip=docker
```

Snapshots need no publishing credentials. To retry a failed release after merge,
rerun its Release workflow in GitHub Actions. The tap CI installs the cask and
checks all four command names on Linux AMD64 and macOS ARM64/AMD64.
