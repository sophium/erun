class Erun < Formula
  desc "Multi-tenant multi-environment deployment and management tool"
  homepage "https://github.com/sophium/erun"
  url "https://github.com/sophium/erun/archive/refs/tags/v1.0.306.tar.gz"
  sha256 "0c1c37442a283dfe566f6146fdb9816a25d874876ffbe628642e22e8380f7942"
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
      # The binding generator compiles this package, whose production assets
      # embed frontend/dist, so the directory has to exist before the bundle.
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
             # `main.`, not the module path: erun-app's build vars are declared
             # in package main, and -X against a path no package declares is
             # dropped in silence, leaving the binary reporting "dev".
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
