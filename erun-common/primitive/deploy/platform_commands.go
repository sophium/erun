package deploy

import (
	"errors"

	"github.com/sophium/erun/erun-common/infra/cloud"
)

// IsPlatformAliasAmbiguity reports whether err is that ambiguity, through any
// markPlatformAliasUnusable the resolver applied on the way out.
func IsPlatformAliasAmbiguity(err error) bool {
	return errors.Is(err, cloud.ErrPlatformAliasAmbiguous)
}
