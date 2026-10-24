package build

import (
	"github.com/sophium/erun/erun-common/base/model"
)

func releasePushTestSpec() model.ReleaseSpec {
	return model.ReleaseSpec{
		ProjectRoot: "/repo",
		Branch:      "main",
		Version:     "1.4.2",
	}
}
