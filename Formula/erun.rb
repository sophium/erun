class Erun < Formula
  desc "Multi-tenant multi-environment deployment and management tool"
  homepage "https://github.com/sophium/erun"
  url "https://github.com/sophium/erun/archive/refs/tags/v1.0.50.tar.gz"
  sha256 "e336e9f4b9b3fe147b0354cbc96f5cdc0a6b45bf9f1253bc6b695cbb35b8ef2e"
  license "MIT"

  depends_on "go" => :build
  depends_on "node" => :build
  depends_on "yarn" => :build

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

    cd "erun-backend/erun-backend-api" do
      system "go", "build",
             *std_go_args(
               output:  bin/"eapi",
               ldflags: "-s -w",
             ),
             "./cmd/eapi"
    end

    cd "erun-ui" do
      wails_bin = buildpath/"bin/wails"
      wails_version = shell_output("go list -m -f '{{.Version}}' github.com/wailsapp/wails/v2").strip
      mkdir_p wails_bin.dirname
      ENV["GOBIN"] = wails_bin.dirname
      system "go", "install", "github.com/wailsapp/wails/v2/cmd/wails@#{wails_version}"
      system wails_bin, "generate", "module"

      cd "frontend" do
        system "yarn", "install", "--frozen-lockfile"
        system "yarn", "build"
      end

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
    assert_match "Usage of eapi:", shell_output("#{bin}/eapi --help 2>&1")
    assert_predicate bin/"erun-app", :exist?
  end
end
