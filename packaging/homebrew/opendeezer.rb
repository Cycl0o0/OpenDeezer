# Homebrew formula for the OpenDeezer terminal client.
#
# Installs the prebuilt TUI binary from the GitHub release. Update `version` and
# the four `sha256` values on each release (the release `checksums` job prints
# them as SHA256SUMS.txt). Place this in a tap, e.g. Cycl0o0/homebrew-tap, then:
#   brew install Cycl0o0/tap/opendeezer
class Opendeezer < Formula
  desc "Terminal Deezer client — browse and stream with your Premium ARL"
  homepage "https://github.com/Cycl0o0/OpenDeezer"
  version "0.4.1"
  license "AGPL-3.0-or-later"

  on_macos do
    on_arm do
      url "https://github.com/Cycl0o0/OpenDeezer/releases/download/v#{version}/opendeezer-tui-darwin-arm64"
      sha256 "REPLACE_WITH_DARWIN_ARM64_SHA256"
    end
    on_intel do
      url "https://github.com/Cycl0o0/OpenDeezer/releases/download/v#{version}/opendeezer-tui-darwin-amd64"
      sha256 "REPLACE_WITH_DARWIN_AMD64_SHA256"
    end
  end

  on_linux do
    on_arm do
      url "https://github.com/Cycl0o0/OpenDeezer/releases/download/v#{version}/opendeezer-tui-linux-arm64"
      sha256 "REPLACE_WITH_LINUX_ARM64_SHA256"
    end
    on_intel do
      url "https://github.com/Cycl0o0/OpenDeezer/releases/download/v#{version}/opendeezer-tui-linux-amd64"
      sha256 "REPLACE_WITH_LINUX_AMD64_SHA256"
    end
  end

  def install
    bin.install Dir["*"].first => "opendeezer"
  end

  test do
    assert_match "opendeezer", shell_output("#{bin}/opendeezer -version")
  end
end
