package model

// EnvironmentLifecycleState combines power state, deploy health, and idle eligibility.
type EnvironmentLifecycleState string

const (
	EnvironmentLifecycleRunning      EnvironmentLifecycleState = "running"
	EnvironmentLifecycleIdle         EnvironmentLifecycleState = "idle"
	EnvironmentLifecycleDeployFailed EnvironmentLifecycleState = "deploy-failed"
	EnvironmentLifecycleStopped      EnvironmentLifecycleState = "stopped"
	// EnvironmentLifecycleUnknown is reported when a signal the state depends on was not observed.
	EnvironmentLifecycleUnknown EnvironmentLifecycleState = "unknown"
)

// EnvironmentHealth is the root config gate and deploy diagnosis with its recommended recovery.
type EnvironmentHealth struct {
	RootConfig          RootConfigInspection  `json:"rootConfig"`
	Deploy              DeployDiagnosisResult `json:"deploy"`
	RecommendedRecovery DeployRecoveryAction  `json:"recommendedRecovery,omitempty"`
}
