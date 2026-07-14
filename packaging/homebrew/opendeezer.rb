# Homebrew formula for the OpenDeezer terminal client.
#
# Installs the prebuilt TUI binary from the GitHub release. Update `version` and
# the four `sha256` values on each release (the release `checksums` job prints
# them as SHA256SUMS.txt). Place this in a tap, e.g. Cycl0o0/homebrew-tap, then:
#   brew install Cycl0o0/tap/opendeezer
class Opendeezer < Formula
  desc "Native terminal client for browsing and streaming a Deezer library"
  homepage "https://github.com/Cycl0o0/OpenDeezer"
  version "3.1.4"
  license "AGPL-3.0-only"

  on_macos do
    on_arm do
      url "https://github.com/Cycl0o0/OpenDeezer/releases/download/v#{version}/opendeezer-tui-darwin-arm64"
      sha256 "3a6ac99c330f92dbf575996fb2a252795f5758a49352f5e278e7093a1f24f7ac"
    end
    on_intel do
      url "https://github.com/Cycl0o0/OpenDeezer/releases/download/v#{version}/opendeezer-tui-darwin-amd64"
      sha256 "749227757523f62d9efcd32c6abd2b8fe69d2c3909bc9ef194f7d41d12a32dea"
    end
  end

  on_linux do
    depends_on "alsa-lib"

    on_arm do
      url "https://github.com/Cycl0o0/OpenDeezer/releases/download/v#{version}/opendeezer-tui-linux-arm64"
      sha256 "7ce9629fa7ed000f76a3563fd2aa0e6a53afdcaba5450937996b9a61e7eea227"
    end
    on_intel do
      url "https://github.com/Cycl0o0/OpenDeezer/releases/download/v#{version}/opendeezer-tui-linux-amd64"
      sha256 "4701e62b75af845c82b071ced6fffe521cd32e3f3e211e33837e696e46bf89d0"
    end
  end

  def install
    bin.install Dir["*"].first => "opendeezer"
  end

  test do
    assert_match "opendeezer", shell_output("#{bin}/opendeezer -version")
  end
end
