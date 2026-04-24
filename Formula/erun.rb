class Erun < Formula
  desc "Multi-tenant multi-environment deployment and management tool"
  homepage "https://github.com/sophium/erun"
  url "https://github.com/sophium/erun/archive/refs/tags/v1.0.48.tar.gz"
  sha256 "246fea721324b9f037041813a7420bc1b1536e3223e70581576d408ba032e93f"
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

    cd "erun-ui" do
      ENV["CGO_ENABLED"] = "1"
      ENV["MACOSX_DEPLOYMENT_TARGET"] = "11.0"
      ENV.append "CGO_CFLAGS", "-mmacosx-version-min=#{ENV["MACOSX_DEPLOYMENT_TARGET"]}"
      ENV.append "CGO_CXXFLAGS", "-mmacosx-version-min=#{ENV["MACOSX_DEPLOYMENT_TARGET"]}"
      ENV.append "CGO_LDFLAGS", "-mmacosx-version-min=#{ENV["MACOSX_DEPLOYMENT_TARGET"]}"
      system "go", "build",
             "-trimpath",
             "-tags", "desktop,production",
             "-ldflags", "-s -w -X github.com/sophium/erun/erun-ui.buildVersion=#{version}",
             "-o", bin/"erun-app",
             "."
    end
  end

  test do
    assert_match "Tenants:", shell_output("#{bin}/erun list")
    assert_match "Launch the ERun desktop app", shell_output("#{bin}/erun help app")
    assert_match "Usage of emcp:", shell_output("#{bin}/emcp --help 2>&1")
    assert_predicate bin/"erun-app", :exist?
  end
end
