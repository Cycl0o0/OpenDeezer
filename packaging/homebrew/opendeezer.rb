# Homebrew formula for the OpenDeezer terminal client.
#
# Installs the prebuilt TUI binary from the GitHub release. Update `version` and
# the four `sha256` values on each release (the release `checksums` job prints
# them as SHA256SUMS.txt). Place this in a tap, e.g. Cycl0o0/homebrew-tap, then:
#   brew install Cycl0o0/tap/opendeezer
class Opendeezer < Formula
  desc "Native terminal client for browsing and streaming a Deezer library"
  homepage "https://github.com/Cycl0o0/OpenDeezer"
  version "3.1.3"
  license "AGPL-3.0-only"

  on_macos do
    on_arm do
      url "https://github.com/Cycl0o0/OpenDeezer/releases/download/v#{version}/opendeezer-tui-darwin-arm64"
      sha256 "cb720573ecbae2169fc0df44fc7033d2ec91a8ef9c567d8ac4c4cee5a37860b9"
    end
    on_intel do
      url "https://github.com/Cycl0o0/OpenDeezer/releases/download/v#{version}/opendeezer-tui-darwin-amd64"
      sha256 "b6512c0e5a1a25d7fb21b101f0a04f3989db75c07c7e2248bac8d9f0966428ab"
    end
  end

  on_linux do
    depends_on "alsa-lib"

    on_arm do
      url "https://github.com/Cycl0o0/OpenDeezer/releases/download/v#{version}/opendeezer-tui-linux-arm64"
      sha256 "fc243d1fcc9bf167399608fa6010f44383f380a6afa2b63e46b0372dd7377e74"
    end
    on_intel do
      url "https://github.com/Cycl0o0/OpenDeezer/releases/download/v#{version}/opendeezer-tui-linux-amd64"
      sha256 "0e12afce9760fb8d5ec74d2e67c226533d4c4d2d376904851f68176990037f31"
    end
  end

  def install
    bin.install Dir["*"].first => "opendeezer"
  end

  test do
    assert_match "opendeezer", shell_output("#{bin}/opendeezer -version")
  end
end
