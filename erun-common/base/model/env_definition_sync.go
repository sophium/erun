package model

// EnvDefinitionPushParams names the local environment to upload; only its marker names the row.
type EnvDefinitionPushParams struct {
	Tenant      string
	Environment string
}

type EnvDefinitionAdoptParams struct {
	Tenant      string
	Environment string
}
