class UnrealAgentRunner < Formula
  desc "Run AI agents from prompts or JSON requests"
  homepage "https://github.com/unreallabsai/unreal-agent"
  version "RELEASE_VERSION"
  license "MIT"

  on_macos do
    on_arm do
      url "https://github.com/unreallabsai/unreal-agent/releases/download/v#{version}/unreal-agent-runner_#{version}_darwin_arm64.tar.gz"
      sha256 "SHA256_DARWIN_ARM64"
    end

    on_intel do
      url "https://github.com/unreallabsai/unreal-agent/releases/download/v#{version}/unreal-agent-runner_#{version}_darwin_amd64.tar.gz"
      sha256 "SHA256_DARWIN_AMD64"
    end
  end

  on_linux do
    on_arm do
      url "https://github.com/unreallabsai/unreal-agent/releases/download/v#{version}/unreal-agent-runner_#{version}_linux_arm64.tar.gz"
      sha256 "SHA256_LINUX_ARM64"
    end

    on_intel do
      url "https://github.com/unreallabsai/unreal-agent/releases/download/v#{version}/unreal-agent-runner_#{version}_linux_amd64.tar.gz"
      sha256 "SHA256_LINUX_AMD64"
    end
  end

  def install
    bin.install "unreal-agent-runner"
  end

  test do
    assert_match "unreal-agent-runner", shell_output("#{bin}/unreal-agent-runner -h 2>&1")
  end
end
