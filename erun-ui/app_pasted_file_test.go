package main

import (
	"context"
	"encoding/base64"
	"strings"
	"testing"
	"time"

	configpkg "github.com/sophium/erun/erun-common/base/config"
)

func TestSavePastedFileCopiesIntoCurrentRuntime(t *testing.T) {
	projectRoot := t.TempDir()
	store := stubUIStore{
		tenants: map[string]configpkg.TenantConfig{
			"erun": {
				Name:               "erun",
				DefaultEnvironment: "local",
			},
		},
		envs: map[string]configpkg.EnvConfig{
			"erun/local": {
				Name:              "local",
				LocalRepoPath:     projectRoot,
				KubernetesContext: "rancher-desktop",
			},
		},
	}

	fileData := []byte("pdf-data")
	var saved pastedFileSaveParams
	app := NewApp(erunUIDeps{
		store: store,
		savePastedFile: func(params pastedFileSaveParams) (string, error) {
			saved = params
			return "/home/erun/.codex/attachments/paste-report.pdf", nil
		},
	})
	defer app.shutdown(context.Background())

	selection := uiSelection{Tenant: "erun", Environment: "local"}
	app.mu.Lock()
	app.nextSerial++
	serial := app.nextSerial
	managed := &managedTerminal{
		session:   newStubTerminalSession(),
		selection: selection,
		key:       openSessionKey(selection, 0),
		serial:    serial,
	}
	app.sessions[managed.key] = managed
	app.mu.Unlock()

	result, err := app.SavePastedFile(serial, pastedFilePayload{
		Data:     base64.StdEncoding.EncodeToString(fileData),
		MIMEType: "application/pdf",
		Name:     "report.pdf",
	})
	if err != nil {
		t.Fatalf("SavePastedFile failed: %v", err)
	}
	if result.Path != "/home/erun/.codex/attachments/paste-report.pdf" {
		t.Fatalf("unexpected pasted file path: %q", result.Path)
	}
	if string(saved.Data) != string(fileData) {
		t.Fatalf("unexpected saved data: %q", string(saved.Data))
	}
	if saved.MIMEType != "application/pdf" || saved.Name != "report.pdf" {
		t.Fatalf("unexpected saved metadata: %+v", saved)
	}
	if saved.Result.Tenant != "erun" || saved.Result.Environment != "local" {
		t.Fatalf("unexpected resolved target: %+v", saved.Result)
	}
}

func TestDecodePastedFilePayloadAcceptsDataURL(t *testing.T) {
	imageData := []byte("png-data")
	got, mimeType, err := decodePastedFilePayload(pastedFilePayload{
		Data: "data:image/png;base64," + base64.StdEncoding.EncodeToString(imageData),
	})
	if err != nil {
		t.Fatalf("decodePastedFilePayload failed: %v", err)
	}
	if string(got) != string(imageData) {
		t.Fatalf("unexpected decoded data: %q", string(got))
	}
	if mimeType != "image/png" {
		t.Fatalf("unexpected mime type: %q", mimeType)
	}
}

func TestDecodePastedFilePayloadAcceptsNonImage(t *testing.T) {
	// A PDF, or a file with no MIME type, must decode without an image-only gate.
	pdfData := []byte("%PDF-1.7 body")
	cases := []struct {
		name     string
		payload  pastedFilePayload
		wantMIME string
	}{
		{
			name:     "pdf_data_url",
			payload:  pastedFilePayload{Data: "data:application/pdf;base64," + base64.StdEncoding.EncodeToString(pdfData)},
			wantMIME: "application/pdf",
		},
		{
			name:     "empty_mime",
			payload:  pastedFilePayload{Data: base64.StdEncoding.EncodeToString(pdfData)},
			wantMIME: "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, mimeType, err := decodePastedFilePayload(tc.payload)
			if err != nil {
				t.Fatalf("decodePastedFilePayload failed: %v", err)
			}
			if string(got) != string(pdfData) {
				t.Fatalf("unexpected decoded data: %q", string(got))
			}
			if mimeType != tc.wantMIME {
				t.Fatalf("unexpected mime type: %q", mimeType)
			}
		})
	}
}

func TestPastedFileFilenameDerivation(t *testing.T) {
	now := time.Date(2026, 1, 2, 3, 4, 5, 600000000, time.UTC)
	stamp := "paste-20260102-030405.600000000"
	cases := []struct {
		name     string
		mimeType string
		fileName string
		want     string
	}{
		{name: "preserves_pdf_name", mimeType: "application/pdf", fileName: "report.pdf", want: stamp + "-report.pdf"},
		{name: "preserves_csv_name", mimeType: "text/csv", fileName: "data.csv", want: stamp + "-data.csv"},
		{name: "preserves_extensionless_name", mimeType: "", fileName: "Makefile", want: stamp + "-Makefile"},
		{name: "image_name_preserved", mimeType: "image/png", fileName: "screenshot.png", want: stamp + "-screenshot.png"},
		{name: "no_name_known_mime_keeps_extension", mimeType: "image/png", fileName: "", want: stamp + ".png"},
		{name: "no_name_unknown_mime_falls_back_to_bin", mimeType: "", fileName: "", want: stamp + ".bin"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := pastedFileFilename(now, tc.mimeType, tc.fileName)
			if got != tc.want {
				t.Fatalf("pastedFileFilename(%q, %q) = %q, want %q", tc.mimeType, tc.fileName, got, tc.want)
			}
		})
	}
}

func TestPastedFileFilenameRejectsTraversal(t *testing.T) {
	now := time.Date(2026, 1, 2, 3, 4, 5, 600000000, time.UTC)
	// The derived filename must contain no separator and not be "."/"..".
	cases := []string{
		"../../etc/passwd",
		"/etc/passwd",
		"..\\..\\windows\\system32\\evil.dll",
		"sub/dir/evil.sh",
		"with\nnewline.txt",
		"..",
		".",
		"",
	}
	for _, name := range cases {
		t.Run(name, func(t *testing.T) {
			got := pastedFileFilename(now, "application/octet-stream", name)
			segment := strings.TrimPrefix(got, "paste-20260102-030405.600000000")
			if strings.ContainsAny(segment, "/\\") {
				t.Fatalf("derived filename %q contains a path separator for input %q", got, name)
			}
			joined := pastedFileRemoteDir() + "/" + got
			if !strings.HasPrefix(joined, pastedFileRemoteDir()+"/paste-") {
				t.Fatalf("derived path %q escaped the staging dir for input %q", joined, name)
			}
		})
	}
}

func TestBuildPastedFileCopyCommandTargetsRuntimeDeployment(t *testing.T) {
	// A non-erun tenant keeps the Helm release name distinct from the container name.
	result := configpkg.OpenResult{
		Tenant:      "petios",
		Environment: "local",
		RepoPath:    "/Users/example/git/petios",
		EnvConfig: configpkg.EnvConfig{
			KubernetesContext: "rancher-desktop",
		},
	}

	remoteDir := pastedFileRemoteDir()
	if remoteDir != "/home/erun/.codex/attachments" {
		t.Fatalf("unexpected remote dir: %q", remoteDir)
	}

	name, args, script := buildPastedFileCopyCommand(result, remoteDir, remoteDir+"/paste.png")
	if name != "kubectl" {
		t.Fatalf("unexpected command name: %q", name)
	}
	wantArgs := []string{
		"--context", "rancher-desktop",
		"--namespace", "petios-local",
		"exec", "-i",
		"-c", "erun-devops",
		"deployment/petios-devops",
		"--",
		"/bin/sh", "-lc",
		"mkdir -p '/home/erun/.codex/attachments' && base64 -d > '/home/erun/.codex/attachments/paste.png'",
	}
	if strings.Join(args, "\n") != strings.Join(wantArgs, "\n") {
		t.Fatalf("unexpected args:\n%q\nwant:\n%q", args, wantArgs)
	}
	if script != wantArgs[len(wantArgs)-1] {
		t.Fatalf("unexpected script: %q", script)
	}
}
