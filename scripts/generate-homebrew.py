#!/usr/bin/env python3
"""Verify published release archives and write the Homebrew tap formulae."""

import hashlib
import pathlib
import re
import sys
import tarfile

REPOSITORY = "https://github.com/unreallabsai/unreal-agent"
VERSION = r"(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)"


def generate(tag, dist, tap):
    if not re.fullmatch("v" + VERSION, tag):
        raise ValueError("expected a stable release tag: vMAJOR.MINOR.PATCH")
    version = tag[1:]
    checksums = {}
    for line in (dist / "SHA256SUMS").read_text().splitlines():
        match = re.fullmatch(r"([0-9a-f]{64})  (?:\./)?([^/]+\.tar\.gz)", line)
        if not match or match[2] in checksums:
            raise ValueError("invalid or duplicate SHA256SUMS entry")
        checksums[match[2]] = match[1]

    formulae = {}
    for binary in ("unreal-agent-runner", "unreal-agent-tui"):
        blocks = []
        for target_os, homebrew_os in (("darwin", "macos"), ("linux", "linux")):
            architectures = []
            for target_arch, homebrew_arch in (("arm64", "arm"), ("amd64", "intel")):
                name = f"{binary}_{version}_{target_os}_{target_arch}.tar.gz"
                archive = dist / name
                actual = hashlib.sha256(archive.read_bytes()).hexdigest()
                if actual != checksums.get(name):
                    raise ValueError(f"checksum mismatch or missing checksum: {name}")
                with tarfile.open(archive) as contents:
                    members = contents.getmembers()
                    if {member.name for member in members} != {binary, "LICENSE"} or len(members) != 2:
                        raise ValueError(f"unexpected archive contents: {name}")
                    if any(not member.isfile() for member in members):
                        raise ValueError(f"archive contains a non-regular file: {name}")
                    if not contents.getmember(binary).mode & 0o111:
                        raise ValueError(f"binary is not executable: {name}")
                architectures.append(f'''    on_{homebrew_arch} do
      url "{REPOSITORY}/releases/download/{tag}/{name}"
      sha256 "{actual}"
    end''')
            blocks.append(f"  on_{homebrew_os} do\n" + "\n\n".join(architectures) + "\n  end")

        if binary == "unreal-agent-tui":
            formula_name, class_name = "unreal-agent", "UnrealAgent"
            description = "Interactive terminal client for the Unreal Agent harness"
            dependency = '\n  depends_on "unreallabsai/tap/unreal-agent-runner"\n'
            install = '''    bin.install "unreal-agent-tui"
    bin.install_symlink "unreal-agent-tui" => "unreal-agent"
    bin.install_symlink "unreal-agent-tui" => "uat"'''
            test = '''    ["unreal-agent-tui", "unreal-agent", "uat"].each do |command|
      assert_match "unreal-agent-tui #{version}", shell_output("#{bin}/#{command} -version")
    end'''
        else:
            formula_name, class_name = binary, "UnrealAgentRunner"
            description = "Run AI agents from prompts or JSON requests"
            dependency = ""
            install = '    bin.install "unreal-agent-runner"'
            test = '    assert_match "unreal-agent-runner", shell_output("#{bin}/unreal-agent-runner -h 2>&1")'
        formulae[formula_name] = f'''class {class_name} < Formula
  desc "{description}"
  homepage "{REPOSITORY}"
  version "{version}"
  license "MIT"
{dependency}
''' + "\n\n".join(blocks) + f'''

  def install
{install}
  end

  test do
{test}
  end
end
'''

    # An older, slower release must never overwrite a newer tap version.
    for name in formulae:
        current = tap / "Formula" / f"{name}.rb"
        if current.exists():
            match = re.search(r'^  version "(' + VERSION + r')"$', current.read_text(), re.MULTILINE)
            if not match:
                raise ValueError(f"cannot determine current version: {current}")
            if tuple(map(int, match[1].split("."))) > tuple(map(int, version.split("."))):
                raise ValueError(f"refusing to downgrade {name} from {match[1]} to {version}")

    (tap / "Formula").mkdir(parents=True, exist_ok=True)
    for name, formula in formulae.items():
        (tap / "Formula" / f"{name}.rb").write_text(formula)
    (tap / "Aliases").mkdir(exist_ok=True)
    for name in ("unreal-agent-tui", "uat"):
        alias = tap / "Aliases" / name
        if alias.is_symlink() and alias.readlink() == pathlib.Path("../Formula/unreal-agent.rb"):
            continue
        if alias.exists() or alias.is_symlink():
            raise ValueError(f"unexpected existing alias: {alias}")
        alias.symlink_to("../Formula/unreal-agent.rb")


if __name__ == "__main__":
    if len(sys.argv) != 4:
        sys.exit("Usage: generate-homebrew.py vMAJOR.MINOR.PATCH DIST_DIRECTORY TAP_DIRECTORY")
    generate(sys.argv[1], pathlib.Path(sys.argv[2]), pathlib.Path(sys.argv[3]))
