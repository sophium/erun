package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDeclaringFileFindsTheFunctionWhereverItLives(t *testing.T) {
	repo := t.TempDir()
	write := func(rel, content string) {
		path := filepath.Join(repo, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("erun-common/base/core/exec.go", "package core\n\nfunc Command() {}\n")
	write("erun-common/base/core/exec_test.go", "package core\n\nfunc CommandFake() {}\n")
	write("erun-common/deploy.go", "package eruncommon\n\nfunc (d deployer) RunDeploySpec() {}\n")
	write("erun-cli/cmd/build/build.go", "package build\n\nfunc runBuildCommand() {}\n")
	write("erun-cli/cmd/other.go", "package cmd\n\nfunc Command() {}\n")

	m := measurer{repo: repo}
	got, err := m.declaringFile("Command")
	if err != nil || got != filepath.Join(repo, "erun-common", "base", "core", "exec.go") {
		t.Fatalf("declaringFile(Command) = %q, %v", got, err)
	}
	if got, err := m.declaringFile("RunDeploySpec"); err != nil || filepath.Base(got) != "deploy.go" {
		t.Fatalf("a method is found by its name too: %q, %v", got, err)
	}
	if got, err := m.declaringFile("runBuildCommand"); err != nil || got != filepath.Join(repo, "erun-cli", "cmd", "build", "build.go") {
		t.Fatalf("a function only another module declares is found there: %q, %v", got, err)
	}
	if _, err := m.declaringFile("CommandFake"); err == nil {
		t.Fatal("a declaration only a test file has is not an edit target")
	}
}

func TestDirSizeSumsEveryFile(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "a"), 0o755); err != nil {
		t.Fatal(err)
	}
	for name, size := range map[string]int{"x": 3, "a/y": 5} {
		if err := os.WriteFile(filepath.Join(dir, filepath.FromSlash(name)), make([]byte, size), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if got, err := dirSize(dir); err != nil || got != 8 {
		t.Fatalf("dirSize = %d, %v; want 8", got, err)
	}
}
