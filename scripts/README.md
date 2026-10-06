# Releases

Push a stable `vMAJOR.MINOR.PATCH` tag on `main` to publish binaries, Docker, and Homebrew:

```sh
git tag v0.3.1
git push origin v0.3.1
```

Configure `release-env` with secrets `DOCKERHUB_TOKEN`, `HOMEBREW_TAP_DEPLOY_KEY`
and variable `DOCKERHUB_USERNAME`. The tap deploy key requires write access.

Local checks (no publishing):

```sh
goreleaser check
goreleaser release --snapshot --clean
```

Add `--skip=docker` to skip Docker builds.
