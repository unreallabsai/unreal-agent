#!/usr/bin/env bash
set -euo pipefail

release_tag=${1:?Usage: scripts/update-homebrew.sh TAG DIST_DIRECTORY TAP_DIRECTORY}
dist=${2:?Missing release archive directory}
tap=${3:?Missing tap directory}
[[ "$release_tag" =~ ^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$ ]]
version=${release_tag#v}
script_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)

# Verify the downloaded archives before using their checksums in the formulae.
(
  cd "$dist"
  if command -v sha256sum >/dev/null; then
    sha256sum --check SHA256SUMS
  else
    shasum -a 256 --check SHA256SUMS
  fi
)

checksum_for() {
  local filename="${binary}_${version}_$1_$2.tar.gz"
  local checksum
  checksum=$(awk -v filename="$filename" '$2 == filename || $2 == "./" filename { print $1 }' "$dist/SHA256SUMS")
  [[ "$checksum" =~ ^[0-9a-f]{64}$ ]] || return 1
  printf '%s' "$checksum"
}

# The templates contain the install commands, aliases, and Homebrew tests.
mkdir -p "$tap/Formula" "$tap/Aliases"
for binary in unreal-agent-runner unreal-agent-tui; do
  formula=unreal-agent-runner
  if [[ "$binary" == unreal-agent-tui ]]; then
    formula=unreal-agent
  fi
  darwin_arm64=$(checksum_for darwin arm64)
  darwin_amd64=$(checksum_for darwin amd64)
  linux_arm64=$(checksum_for linux arm64)
  linux_amd64=$(checksum_for linux amd64)
  sed -e "s/RELEASE_VERSION/$version/g" \
    -e "s/SHA256_DARWIN_ARM64/$darwin_arm64/g" \
    -e "s/SHA256_DARWIN_AMD64/$darwin_amd64/g" \
    -e "s/SHA256_LINUX_ARM64/$linux_arm64/g" \
    -e "s/SHA256_LINUX_AMD64/$linux_amd64/g" \
    "$script_dir/homebrew/$formula.rb" > "$tap/Formula/$formula.rb"
done
ln -sfn ../Formula/unreal-agent.rb "$tap/Aliases/unreal-agent-tui"
ln -sfn ../Formula/unreal-agent.rb "$tap/Aliases/uat"
