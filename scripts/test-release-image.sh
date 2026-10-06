#!/usr/bin/env bash
set -euo pipefail

context=${1:?Usage: scripts/test-release-image.sh GORELEASER_DOCKER_CONTEXT}
for arch in amd64 arm64; do
  # Snapshot builds provide one platform per context; releases provide both.
  if [[ ! -f "$context/linux/$arch/unreal-agent-runner" ]]; then
    continue
  fi
  docker buildx build --platform "linux/$arch" --load \
    --file Dockerfile.release --tag "unreal-agent-release-smoke:$arch" "$context"
  docker run --rm --platform "linux/$arch" "unreal-agent-release-smoke:$arch" -h
done
