package config

// OperatorSettableConfigField is a config field an operator sets through a product
// surface; the desktop-surface gate fails any non-Internal entry no surface references.
type OperatorSettableConfigField struct {
	Name string
	// Token is matched case-insensitively as a substring of the operator surface source.
	Token string
	// Internal is the explicit opt-out for fields no operator surface sets.
	Internal bool
	// InternalReason is required when Internal is true.
	InternalReason string
}

var OperatorSettableConfigFields = []OperatorSettableConfigField{
	{
		Name:  "OrchestratorEnvConfig.Role",
		Token: "orchestratorenvrole",
	},
	{
		Name:  "EnvConfig.Hosted",
		Token: "hostedenvironment",
	},
	{
		Name:  "OrchestratorConfig.Alias",
		Token: "orchestratoralias",
	},
	{
		Name:  "OrchestratorConfig.UseGateway",
		Token: "orchestratorusegateway",
	},
	{
		Name:  "OrchestratorConfig.Model",
		Token: "orchestratormodel",
	},
	{
		Name:  "OrchestratorConfig.Effort",
		Token: "orchestratoreffort",
	},
}
