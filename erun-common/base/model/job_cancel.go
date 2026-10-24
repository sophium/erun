package model

type CancelEnvironmentJobParams struct {
	Tenant      string
	Environment string
	ID          string
	Signal      string
}
