package model

import (
	"time"
)

// AwaitEnvironmentJobParams bounds one wait; it never holds a connection open.
type AwaitEnvironmentJobParams struct {
	Tenant      string
	Environment string
	ID          string
	Timeout     time.Duration
	Poll        time.Duration
}
