class Erun < Formula
  desc "Multi-tenant multi-environment deployment and management tool"
  homepage "https://github.com/sophium/erun"
  url "https://github.com/sophium/erun/archive/refs/tags/v1.0.34.tar.gz"
  sha256 "bd4e59b4a3785ad7ec2e54036ebe149cecc0387d91ba02bf6275ba8330a39b1b"
  license "MIT"

  depends_on "go" => :build

  def install
    cd "erun-cli" do
      system "go", "build",
             *std_go_args(
               output:  bin/"erun",
               ldflags: "-s -w -X github.com/sophium/erun/cmd.buildVersion=#{version}",
             ),
             "."
    end

    cd "erun-mcp" do
      system "go", "build",
             *std_go_args(
               output:  bin/"emcp",
               ldflags: "-s -w -X github.com/sophium/erun/erun-mcp.buildVersion=#{version}",
             ),
             "./cmd/emcp"
    end
  end

  test do
    assert_match "Tenants:", shell_output("#{bin}/erun list")
    assert_match "Usage of emcp:", shell_output("#{bin}/emcp --help 2>&1")
  end
end
