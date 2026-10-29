package chartfiles

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// Nothing else ties a wrapped image's VERSION to the chart's default tag.
var (
	chartLiteralImageTagRe = regexp.MustCompile(`%s/(erun-[a-z0-9-]+):([A-Za-z0-9][^"\s]*)"`)
	chartValueImageTagRe   = regexp.MustCompile(`default "([A-Za-z0-9][^"]*)" \.Values\.([a-z0-9-]+)ImageTag`)
)

func TestChartImageTagsMatchTheirVersionPins(t *testing.T) {
	root := repoRootForDockerignoreTest(t)
	checked := map[string]bool{}
	err := filepath.WalkDir(filepath.Join(root, "erun-devops", "k8s"), func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() || filepath.Ext(path) != ".yaml" {
			return err
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		for _, pin := range chartImageTagPins(string(raw)) {
			want := imageVersionPin(t, root, pin.image)
			if want == "" {
				continue
			}
			checked[pin.image] = true
			if pin.tag != want {
				t.Errorf("%s pins %s:%s but erun-devops/docker/%s/VERSION is %s: bump the chart default with the VERSION, or the built image never reaches an environment",
					rel, pin.image, pin.tag, pin.image, want)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	// A parse that stopped seeing the dind default would leave the key case unchecked.
	if !checked["erun-dind"] {
		t.Fatal("no erun-dind image tag was found in the chart templates; the templates moved the tag somewhere this test does not read, so follow it rather than deleting the check")
	}
}

type chartImageTagPin struct {
	image string
	tag   string
}

func chartImageTagPins(template string) []chartImageTagPin {
	var pins []chartImageTagPin
	for _, match := range chartLiteralImageTagRe.FindAllStringSubmatch(template, -1) {
		pins = append(pins, chartImageTagPin{image: match[1], tag: match[2]})
	}
	for _, match := range chartValueImageTagRe.FindAllStringSubmatch(template, -1) {
		pins = append(pins, chartImageTagPin{image: "erun-" + match[2], tag: match[1]})
	}
	return pins
}

// imageVersionPin returns "" for a release-versioned image, which takes the chart's appVersion.
func imageVersionPin(t *testing.T, root, image string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(root, "erun-devops", "docker", image, "VERSION"))
	if os.IsNotExist(err) {
		return ""
	}
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(raw))
}

func TestChartImageTagPinsReadsBothForms(t *testing.T) {
	template := `{{- $a := default (printf "%s/erun-zitadel:v4.15.3" $containerRegistry) (index $imageOverrides "erun-zitadel") -}}
{{- $dindImageTag := default "28.1.1-5" .Values.dindImageTag -}}
{{- $b := default (printf "%s/erun-devops:%s" $containerRegistry .Chart.AppVersion) (index $imageOverrides "erun-devops") -}}`
	got := chartImageTagPins(template)
	want := []chartImageTagPin{{image: "erun-zitadel", tag: "v4.15.3"}, {image: "erun-dind", tag: "28.1.1-5"}}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("pins = %+v, want %+v (a release-versioned %%s tag is not a pin)", got, want)
	}
}
