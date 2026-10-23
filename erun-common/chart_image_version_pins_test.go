package eruncommon

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// A wrapped image's erun-devops/docker/<image>/VERSION is the tag its build
// publishes under, and the charts repeat that tag as a template default.
// Nothing else ties the two together, so a bump could build and publish an
// image no environment ever deploys: a dind daemon.json fix once shipped in a
// new tag while the chart kept defaulting to the previous one.
var (
	// printf "%s/<image>:<tag>" -- a literal tag written beside the image name.
	chartLiteralImageTagRe = regexp.MustCompile(`%s/(erun-[a-z0-9-]+):([A-Za-z0-9][^"\s]*)"`)
	// default "<tag>" .Values.<image>ImageTag -- the tag behind a value.
	chartValueImageTagRe = regexp.MustCompile(`default "([A-Za-z0-9][^"]*)" \.Values\.([a-z0-9-]+)ImageTag`)
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
	// The dind sidecar's default sits behind .Values.dindImageTag; a parse
	// that stopped seeing it would leave the case this test exists for
	// unchecked while the rest still passed.
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

// imageVersionPin returns the image's pinned VERSION, or "" when the repo
// ships no such wrapper (a release-versioned image takes the chart's
// appVersion instead).
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
