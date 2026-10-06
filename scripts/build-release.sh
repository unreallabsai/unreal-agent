#!/usr/bin/env bash
set -euo pipefail

release_tag=${1:?Usage: scripts/build-release.sh vMAJOR.MINOR.PATCH}
[[ "$release_tag" =~ ^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$ ]]
version=${release_tag#v}
commit=$(git rev-parse HEAD)
build_date=$(git show -s --format=%cI HEAD)
export CGO_ENABLED=0
export COPYFILE_DISABLE=1

mkdir -p dist
for target_os in linux darwin; do
  for target_arch in amd64 arm64; do
    for binary in unreal-agent-runner unreal-agent-tui; do
      archive="${binary}_${version}_${target_os}_${target_arch}"
      mkdir -p "dist/$archive"
      if [[ "$binary" == unreal-agent-tui ]]; then
        GOOS="$target_os" GOARCH="$target_arch" go -C cmd/unreal-agent-tui build \
          -mod=readonly -trimpath \
          -ldflags "-X main.version=$version -X main.commit=$commit -X main.date=$build_date" \
          -o "../../dist/$archive/$binary" .
      else
        GOOS="$target_os" GOARCH="$target_arch" go build -mod=readonly -trimpath \
          -o "dist/$archive/$binary" "./cmd/$binary"
      fi
      cp LICENSE "dist/$archive/"
      tar -czf "dist/$archive.tar.gz" -C "dist/$archive" "$binary" LICENSE
    done
  done
done
python3 - "$version" <<'PY'
import hashlib
import pathlib
import sys

dist = pathlib.Path("dist")
archives = sorted(dist.glob(f"unreal-agent-*_{sys.argv[1]}_*.tar.gz"))
with (dist / "SHA256SUMS").open("w") as checksums:
    for archive in archives:
        digest = hashlib.sha256(archive.read_bytes()).hexdigest()
        checksums.write(f"{digest}  ./{archive.name}\n")
PY
