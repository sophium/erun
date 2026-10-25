package providerlifecycle

import (
	"time"
)

func generatedAWSProfileName() string {
	return "erun-sso-" + time.Now().UTC().Format("20060102150405")
}
