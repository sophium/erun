package model

// ReadEnvironmentJobOutputParams pages through a job's captured output.
type ReadEnvironmentJobOutputParams struct {
	Tenant      string
	Environment string
	ID          string
	Offset      int64
	MaxBytes    int64
}
