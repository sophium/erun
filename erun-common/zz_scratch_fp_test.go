package eruncommon

import (
	"testing"
)

func TestScratchDevopsFingerprintChurn(t *testing.T) {
	root := "/home/erun/git/erun"
	mk := func(version, baseVersion string) DockerBuildSpec {
		return DockerBuildSpec{
			ContextDir:     root + "/erun-devops",
			DockerfilePath: root + "/erun-devops/docker/erun-devops/Dockerfile",
			Image: DockerImageReference{
				ProjectRoot:         root,
				Environment:         "local",
				Registry:            "example.test",
				ImageName:           "erun-devops",
				Version:             version,
				BaseVersion:         baseVersion,
				Tag:                 "example.test/erun-devops:" + version,
				VersionFromBuildDir: false,
			},
		}
	}
	fp := func(s DockerBuildSpec) string {
		d, err := computeBuildFingerprint(s)
		if err != nil {
			t.Fatalf("computeBuildFingerprint: %v", err)
		}
		return d
	}
	snapA := fp(mk("1.0.278-snapshot-20260917T010101Z", "1.0.278-snapshot"))
	snapB := fp(mk("1.0.278-snapshot-20260917T020202Z", "1.0.278-snapshot"))
	rel := fp(mk("1.0.278", ""))
	t.Logf("snapshot A = %s", snapA)
	t.Logf("snapshot B = %s", snapB)
	t.Logf("release    = %s", rel)
	t.Logf("snapshotA==snapshotB: %v", snapA == snapB)
}
