class Erun < Formula
  desc "Multi-tenant multi-environment deployment and management tool"
  homepage "https://github.com/sophium/erun"
  url "https://github.com/sophium/erun/archive/refs/tags/v1.0.309.tar.gz"
  sha256 "99d9be966a4f605b44cac2fbba26b2fa9d697f76144978a7346ad77de4e51241"
  license "MIT"

  depends_on "go" => :build
  depends_on "node" => :build
  depends_on "yarn" => :build

  def install
    cd "erun-cli" do
      system "go", "build",
             *std_go_args(
               output:  bin/"erun",
               ldflags: "-s -w -X github.com/sophium/erun/cmd/shared.buildVersion=#{version}",
             ),
             "."
    end

    cd "erun-mcp" do
      system "go", "build",
             *std_go_args(
               output:  bin/"emcp",
               ldflags: "-s -w -X github.com/sophium/erun/erun-mcp/tools/shared/buildinfo.buildVersion=#{version}",
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
      # The binding generator compiles a package that embeds frontend/dist, so it must exist first.
      mkdir_p "frontend/dist"
      system "go", "tool", "wails", "generate", "module"

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
             # `main.`: erun-app declares its build vars in package main; -X on any other path is silently dropped.
             "-ldflags", "-s -w -X main.buildVersion=#{version}",
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
