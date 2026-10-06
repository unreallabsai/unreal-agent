class UnrealAgent < Formula
  desc "Interactive terminal client for the Unreal Agent harness"
  homepage "https://github.com/unreallabsai/unreal-agent"
  version "RELEASE_VERSION"
  license "MIT"

  depends_on "unreallabsai/tap/unreal-agent-runner"

  on_macos do
    on_arm do
      url "https://github.com/unreallabsai/unreal-agent/releases/download/v#{version}/unreal-agent-tui_#{version}_darwin_arm64.tar.gz"
      sha256 "SHA256_DARWIN_ARM64"
    end

    on_intel do
      url "https://github.com/unreallabsai/unreal-agent/releases/download/v#{version}/unreal-agent-tui_#{version}_darwin_amd64.tar.gz"
      sha256 "SHA256_DARWIN_AMD64"
    end
  end

  on_linux do
    on_arm do
      url "https://github.com/unreallabsai/unreal-agent/releases/download/v#{version}/unreal-agent-tui_#{version}_linux_arm64.tar.gz"
      sha256 "SHA256_LINUX_ARM64"
    end

    on_intel do
      url "https://github.com/unreallabsai/unreal-agent/releases/download/v#{version}/unreal-agent-tui_#{version}_linux_amd64.tar.gz"
      sha256 "SHA256_LINUX_AMD64"
    end
  end

  def install
    bin.install "unreal-agent-tui"
    bin.install_symlink "unreal-agent-tui" => "unreal-agent"
    bin.install_symlink "unreal-agent-tui" => "uat"
  end

  test do
    ["unreal-agent-tui", "unreal-agent", "uat"].each do |command|
      assert_match "unreal-agent-tui #{version}", shell_output("#{bin}/#{command} -version")
    end
  end
end
