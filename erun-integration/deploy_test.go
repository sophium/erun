package integration

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sophium/erun/erun-integration/internal/env"
	"github.com/sophium/erun/erun-integration/internal/erun"
	"github.com/sophium/erun/erun-integration/internal/fixture"
	"github.com/sophium/erun/erun-integration/internal/golden"
	"github.com/sophium/erun/erun-integration/internal/normalize"
)

var deployScenarios = []struct {
	name string
	run  func(*testing.T)
}{
	{"help_outside_devops_cwd", testDeployHelpOutsideDevopsCwd},
	{"version_flag_recognized_outside_devops_cwd", testDeployVersionFlagRecognizedOutsideDevopsCwd},
	{"dry_run_from_devops_cwd", testDeployDryRunFromDevopsCwd},
	{"dry_run_retrofits_the_platform_alias_on_a_pre_existing_env", testDeployDryRunRetrofitsThePlatformAliasOnAPreExistingEnv},
	{"dry_run_unaffected_by_kubectl_pod_watch_library_execution_mode", testDeployDryRunUnaffectedByKubectlPodWatchLibraryExecutionMode},
	{"refuses_host_environment", testDeployRefusesHostEnvironment},
	{"dry_run_stopped_env_renders_replicas_zero", testDeployDryRunStoppedEnvRendersReplicasZero},
	{"dry_run_platform_account_binds_cluster_admin", testDeployDryRunPlatformAccountBindsClusterAdmin},
	{"dry_run_with_mcp_auth_public_key", testDeployDryRunWithMCPAuthPublicKey},
	{"dry_run_copies_images_from_to_before_deploy", testDeployDryRunCopiesImagesFromToBeforeDeploy},
	{"dry_run_from_devops_module_root", testDeployDryRunFromDevopsModuleRoot},
	{"dry_run_from_nested_devops_subdir", testDeployDryRunFromNestedDevopsSubdir},
	{"dry_run_from_k8s_dir", testDeployDryRunFromK8sDir},
	{"dry_run_configured_k8s_path", testDeployDryRunConfiguredK8sPath},
	{"dry_run_gitignored_project_config_warns", testDeployDryRunGitignoredProjectConfigWarns},
	{"configured_k8s_path_wrong_name_errors", testDeployConfiguredK8sPathWrongNameErrors},
	{"dry_run_from_git_project_root_detects_tenant_devops", testDeployDryRunFromGitProjectRootDetectsTenantDevops},
	{"dry_run_multiple_devops_modules_errors", testDeployDryRunMultipleDevopsModulesErrors},
	{"dry_run_scope_defaulted_early_failure_names_resolved_env", testDeployDryRunScopeDefaultedEarlyFailureNamesResolvedEnv},
	{"dry_run_unresolvable_scope_failure_omits_env_pair", testDeployDryRunUnresolvableScopeFailureOmitsEnvPair},
	{"dry_run_default_environment_not_configured_names_open_and_recovery", testDeployDryRunDefaultEnvironmentNotConfiguredNamesOpenAndRecovery},
	{"dry_run_no_devops_module_bootstraps_published_runtime", testDeployDryRunNoDevopsModuleBootstrapsPublishedRuntime},
	{"dry_run_unconfigured_alias_derives_provider_and_region", testDeployDryRunUnconfiguredAliasDerivesProviderAndRegion},
	{"dry_run_tenant_aliases_resolve_oidc_issuers", testDeployDryRunTenantAliasesResolveOIDCIssuers},
	{"dry_run_aws_alias_without_cloud_context_resolves_region_from_alias", testDeployDryRunAWSAliasWithoutCloudContextResolvesRegionFromAlias},
	{"dry_run_aws_alias_resolves_region_from_ecr_registry_host", testDeployDryRunAWSAliasResolvesRegionFromECRRegistryHost},
	{"real_run_remote_env_published_chart_via_stubs", testDeployRealRunRemoteEnvPublishedChartViaStubs},
	{"real_run_refreshes_image_pull_secret_via_stubbed_kubectl", testDeployRealRunRefreshesImagePullSecretViaStubbedKubectl},
	{"real_run_image_pull_secret_merge_preserves_unresolved_host", testDeployRealRunImagePullSecretMergePreservesUnresolvedHost},
	{"real_run_image_pull_secret_refuses_malformed_existing_secret", testDeployRealRunImagePullSecretRefusesMalformedExistingSecret},
	{"dry_run_unaffected_by_kubectl_secret_get_library_execution_mode", testDeployDryRunUnaffectedByKubectlSecretGetLibraryExecutionMode},
	{"real_run_kubectl_secret_get_library_execution_mode_reaches_the_api_server_directly", testDeployRealRunKubectlSecretGetLibraryExecutionModeReachesTheAPIServerDirectly},
	{"dry_run_unaffected_by_kubectl_secret_apply_library_execution_mode", testDeployDryRunUnaffectedByKubectlSecretApplyLibraryExecutionMode},
	{"real_run_kubectl_secret_apply_library_execution_mode_client_side_applies_via_the_api_server", testDeployRealRunKubectlSecretApplyLibraryExecutionModeClientSideAppliesViaTheAPIServer},
	{"real_run_new_worktree_volume_announces_the_adoption", testDeployRealRunNewWorktreeVolumeAnnouncesTheAdoption},
	{"real_run_unreadable_worktree_volume_still_announces", testDeployRealRunUnreadableWorktreeVolumeStillAnnounces},
	{"dry_run_unaffected_by_kubectl_pvc_get_library_execution_mode", testDeployDryRunUnaffectedByKubectlPVCGetLibraryExecutionMode},
	{"real_run_kubectl_pvc_get_library_execution_mode_reaches_the_api_server_directly", testDeployRealRunKubectlPVCGetLibraryExecutionModeReachesTheAPIServerDirectly},
	{"real_run_remote_env_tenant_umbrella_pulls_bundled_values", testDeployRealRunRemoteEnvTenantUmbrellaPullsBundledValues},
	{"real_run_recreates_deployment_on_immutable_selector_change", testDeployRealRunRecreatesDeploymentOnImmutableSelectorChange},
	{"real_run_copies_images_from_to_via_stubs", testDeployRealRunCopiesImagesFromToViaStubs},
	{"real_run_preflight_starts_stopped_cloud_context", testDeployRealRunPreflightStartsStoppedCloudContext},
	{"version_required_without_switch_errors", testDeployVersionRequiredWithoutSwitchErrors},
	{"force_flag_appears_in_trace", testDeployForceFlagAppearsInTrace},
	{"dry_run_env_deploy_timeout_overrides_default", testDeployDryRunEnvDeployTimeoutOverridesDefault},
	{"dry_run_rollout_timeout_flag_overrides_env_config", testDeployDryRunRolloutTimeoutFlagOverridesEnvConfig},
	{"rollout_timeout_flag_invalid_duration_errors", testDeployRolloutTimeoutFlagInvalidDurationErrors},
	{"dry_run_max_cpu_memory_storage_flags_apply_namespace_quota", testDeployDryRunMaxCPUMemoryStorageFlagsApplyNamespaceQuota},
	{"max_cpu_memory_storage_flags_partial_set_errors", testDeployMaxCPUMemoryStorageFlagsPartialSetErrors},
	{"env_deploy_timeout_invalid_duration_errors", testDeployEnvDeployTimeoutInvalidDurationErrors},
	{"dry_run_pinned_version_installs_buildable_chart_without_building", testDeployDryRunPinnedVersionInstallsBuildableChartWithoutBuilding},
	{"real_run_pinned_version_missing_image_errors", testDeployRealRunPinnedVersionMissingImageErrors},
	{"dry_run_outside_devops_with_tenant_env", testDeployDryRunOutsideDevopsWithTenantEnv},
	{"dry_run_remote_env_refuses_when_no_runtime_chart_is_confirmed", testDeployDryRunRemoteEnvRefusesWhenNoRuntimeChartIsConfirmed},
	{"dry_run_refuses_shared_chart_confirmed_only_at_a_different_version", testDeployDryRunRefusesSharedChartConfirmedOnlyAtADifferentVersion},
	{"dry_run_remote_env_prefers_tenant_published_runtime_chart", testDeployDryRunRemoteEnvPrefersTenantPublishedRuntimeChart},
	{"dry_run_remote_env_tenant_umbrella_explicit_runtime_image_wins", testDeployDryRunRemoteEnvTenantUmbrellaExplicitRuntimeImageWins},
	{"dry_run_remote_env_cluster_registry_chart_from_runtime_image", testDeployDryRunRemoteEnvClusterRegistryChartFromRuntimeImage},
	{"dry_run_runtime_chart_resolves_from_the_platform_registry", testDeployDryRunRuntimeChartResolvesFromThePlatformRegistry},
	{"dry_run_runtime_chart_prefers_the_deploy_registry_over_the_platform_registry", testDeployDryRunRuntimeChartPrefersTheDeployRegistryOverThePlatformRegistry},
	{"dry_run_operator_set_runtime_registry_stands_over_a_different_resolution", testDeployDryRunOperatorSetRuntimeRegistryStandsOverADifferentResolution},
	{"real_run_records_the_registry_the_runtime_chart_resolved_from", testDeployRealRunRecordsTheRegistryTheRuntimeChartResolvedFrom},
	{"real_run_records_the_deploy_registry_when_the_chart_resolves_there", testDeployRealRunRecordsTheDeployRegistryWhenTheChartResolvesThere},
	{"real_run_keeps_an_operator_set_runtime_registry", testDeployRealRunKeepsAnOperatorSetRuntimeRegistry},
	{"dry_run_remote_env_image_pull_secrets", testDeployDryRunRemoteEnvImagePullSecrets},
	{"dry_run_remote_env_image_pull_secret_refreshed_from_host_credential", testDeployDryRunRemoteEnvImagePullSecretRefreshedFromHostCredential},
	{"dry_run_remote_env_image_pull_secret_covers_runtime_image_override_registry", testDeployDryRunRemoteEnvImagePullSecretCoversRuntimeImageOverrideRegistry},
	{"dry_run_remote_env_umbrella_ignores_stale_stock_runtimeimage", testDeployDryRunRemoteEnvUmbrellaIgnoresStaleStockRuntimeimage},
	{"dry_run_remote_env_umbrella_honours_tenants_own_runtimeimage_tag", testDeployDryRunRemoteEnvUmbrellaHonoursTenantsOwnRuntimeimageTag},
	{"dry_run_remote_env_stated_chart_ignores_stale_stock_runtimeimage", testDeployDryRunRemoteEnvStatedChartIgnoresStaleStockRuntimeimage},
	{"dry_run_moves_the_tenant_umbrella_pin_with_the_deploy_version", testDeployDryRunMovesTheTenantUmbrellaPinWithTheDeployVersion},
	{"dry_run_says_when_it_holds_the_tenant_umbrella_back", testDeployDryRunSaysWhenItHoldsTheTenantUmbrellaBack},
	{"dry_run_runtime_chart_flag_rescues_a_stock_runtime_image_override", testDeployDryRunRuntimeChartFlagRescuesAStockRuntimeImageOverride},
	{"dry_run_runtime_chart_flag_alone_heals_a_persisted_stock_runtimeimage_memo", testDeployDryRunRuntimeChartFlagAloneHealsAPersistedStockRuntimeimageMemo},
	{"dry_run_remote_env_explicit_runtime_image_wins_on_the_tenants_own_version_line", testDeployDryRunRemoteEnvExplicitRuntimeImageWinsOnTheTenantsOwnVersionLine},
	{"dry_run_remote_env_deploys_published_components", testDeployDryRunRemoteEnvDeploysPublishedComponents},
	{"dry_run_runtime_env_unreachable_repo_path_falls_back_to_local_plan", testDeployDryRunRuntimeEnvUnreachableRepoPathFallsBackToLocalPlan},
	{"dry_run_remote_env_deploys_tenant_component_charts_by_reference", testDeployDryRunRemoteEnvDeploysTenantComponentChartsByReference},
	{"dry_run_remote_env_tenant_component_verifies_against_the_cluster_registry", testDeployDryRunRemoteEnvTenantComponentVerifiesAgainstTheClusterRegistry},
	{"dry_run_remote_env_component_threads_resolved_oidc_issuer", testDeployDryRunRemoteEnvComponentThreadsResolvedOIDCIssuer},
	{"dry_run_remote_env_tenant_artifacts_require_published_charts", testDeployDryRunRemoteEnvTenantArtifactsRequirePublishedCharts},
	{"dry_run_stated_runtime_chart_is_exempt_from_the_tenant_chart_mandate", testDeployDryRunStatedRuntimeChartIsExemptFromTheTenantChartMandate},
	{"dry_run_runtime_env_mount_source_clones_at_release_ref", testDeployDryRunRuntimeEnvMountSourceClonesAtReleaseRef},
	{"dry_run_remote_env_custom_runtime_image", testDeployDryRunRemoteEnvCustomRuntimeImage},
	{"dry_run_env_states_its_runtime_chart_in_config", testDeployDryRunEnvStatesItsRuntimeChartInConfig},
	{"dry_run_runtime_chart_flag_beats_the_env_config_chart", testDeployDryRunRuntimeChartFlagBeatsTheEnvConfigChart},
	{"dry_run_remote_env_runtime_chart_and_image_on_separate_lines", testDeployDryRunRemoteEnvRuntimeChartAndImageOnSeparateLines},
	{"dry_run_runtime_chart_override_off_a_tenant_umbrella_drops_the_subchart_scope", testDeployDryRunRuntimeChartOverrideOffATenantUmbrellaDropsTheSubchartScope},
	{"dry_run_runtime_chart_without_version_keeps_the_deploy_version", testDeployDryRunRuntimeChartWithoutVersionKeepsTheDeployVersion},
	{"dry_run_remote_env_runtime_image_without_tag", testDeployDryRunRemoteEnvRuntimeImageWithoutTag},
	{"dry_run_runtime_image_override_uses_published_chart", testDeployDryRunRuntimeImageOverrideUsesPublishedChart},
	{"dry_run_runtime_image_override_no_k8s_tree_uses_published_chart", testDeployDryRunRuntimeImageOverrideNoK8sTreeUsesPublishedChart},
	{"dry_run_remote_env_no_repo_path_deploys_published_chart", testDeployDryRunRemoteEnvNoRepoPathDeploysPublishedChart},
	{"dry_run_private_ghcr_runtime_image_with_no_host_credential_traces_pull_secret_decision", testDeployDryRunPrivateGHCRRuntimeImageWithNoHostCredentialTracesPullSecretDecision},
	{"real_run_refuses_before_rollout_when_private_runtime_image_has_no_pull_credential", testDeployRealRunRefusesBeforeRolloutWhenPrivateRuntimeImageHasNoPullCredential},
	{"real_run_heals_stale_runtimeimage_when_moving_onto_the_tenants_own_line", testDeployRealRunHealsStaleRuntimeimageWhenMovingOntoTheTenantsOwnLine},
	{"real_run_refuses_cross_line_runtime_image_switch_before_any_mutation", testDeployRealRunRefusesCrossLineRuntimeImageSwitchBeforeAnyMutation},
	{"dry_run_consistent_stock_runtime_image_env_deploys_unchanged", testDeployDryRunConsistentStockRuntimeImageEnvDeploysUnchanged},
	{"dry_run_unclassifiable_prior_runtime_image_warns_but_proceeds", testDeployDryRunUnclassifiablePriorRuntimeImageWarnsButProceeds},
	{"dry_run_refuses_a_deploy_version_from_another_products_release_line", testDeployDryRunRefusesADeployVersionFromAnotherProductsReleaseLine},
	{"dry_run_remote_env_values_overlay", testDeployDryRunRemoteEnvValuesOverlay},
	{"default_skips_optin_backend_charts", testDeployDefaultSkipsOptinBackendCharts},
	{"components_includes_backend_in_deploy_order", testDeployComponentsIncludesBackendInDeployOrder},
	{"components_config_scopes_component_chart_lookup", testDeployComponentsConfigScopesComponentChartLookup},
	{"dry_run_umbrella_component_builds_helm_dependencies", testDeployDryRunUmbrellaComponentBuildsHelmDependencies},
	{"dry_run_runtime_umbrella_rescopes_set_values", testDeployDryRunRuntimeUmbrellaRescopesSetValues},
	{"project_k8s_plan_groups_parallel_step", testDeployProjectK8sPlanGroupsParallelStep},
	{"project_k8s_plan_includes_listed_charts_without_components_flag", testDeployProjectK8sPlanIncludesListedChartsWithoutComponentsFlag},
	{"project_k8s_plan_rejects_invalid_step_node", testDeployProjectK8sPlanRejectsInvalidStepNode},
	{"rejects_inconsistent_platform_config", testDeployRejectsInconsistentPlatformConfig},
	{"platform_config_threads_into_helm_set", testDeployPlatformConfigThreadsIntoHelmSet},
	{"platform_config_threads_white_label_values_into_helm_set", testDeployPlatformConfigThreadsWhiteLabelValuesIntoHelmSet},
	{"rejects_malformed_platform_logo_url", testDeployRejectsMalformedPlatformLogoURL},
	{"components_rejects_unknown_name", testDeployComponentsRejectsUnknownName},
	{"default_deploys_runtime_only_not_stray_non_optin_chart", testDeployDefaultDeploysRuntimeOnlyNotStrayNonOptinChart},
	{"component_only_tree_bootstraps_published_runtime", testDeployComponentOnlyTreeBootstrapsPublishedRuntime},
	{"saved_deploy_components_drive_selection_without_flag", testDeploySavedDeployComponentsDriveSelectionWithoutFlag},
	{"saved_deploy_components_shadowing_plan_refuses_to_deploy", testDeploySavedDeployComponentsShadowingPlanRefusesToDeploy},
	{"explicit_components_still_narrows_and_does_not_resurrect_shadowed_plan_extras", testDeployExplicitComponentsStillNarrowsAndDoesNotResurrectShadowedPlanExtras},
	{"real_run_via_stubs", testDeployRealRunViaStubs},
	{"dry_run_unaffected_by_kubectl_namespace_get_library_execution_mode", testDeployDryRunUnaffectedByKubectlNamespaceGetLibraryExecutionMode},
	{"real_run_kubectl_namespace_get_library_execution_mode_reaches_the_api_server_directly", testDeployRealRunKubectlNamespaceGetLibraryExecutionModeReachesTheAPIServerDirectly},
	{"real_run_failure_shows_deploy_failed_header_at_default_verbosity", testDeployRealRunFailureShowsDeployFailedHeaderAtDefaultVerbosity},
	{"real_run_persists_runtime_version_and_registry_to_env_config", testDeployRealRunPersistsRuntimeVersionAndRegistryToEnvConfig},
	{"dry_run_persisted_version_reopen_uses_runtime_registry_provenance", testDeployDryRunPersistedVersionReopenUsesRuntimeRegistryProvenance},
	{"dry_run_explicit_version_uses_project_registry_not_provenance", testDeployDryRunExplicitVersionUsesProjectRegistryNotProvenance},
	{"dry_run_persisted_version_without_provenance_uses_project_registry", testDeployDryRunPersistedVersionWithoutProvenanceUsesProjectRegistry},
	{"dry_run_with_managed_cloud_traces_helm_set_strings", testDeployDryRunWithManagedCloudTracesHelmSetStrings},
	{"dry_run_with_aws_claude_models_traces_set_strings", testDeployDryRunWithAWSClaudeModelsTracesSetStrings},
	{"dry_run_with_openrouter_gateway_traces_set_strings", testDeployDryRunWithOpenrouterGatewayTracesSetStrings},
	{"real_run_helm_pending_recovery_via_auto_recover_env", testDeployRealRunHelmPendingRecoveryViaAutoRecoverEnv},
	{"real_run_pending_helm_lock_fails_and_names_the_recovery", testDeployRealRunPendingHelmLockFailsAndNamesTheRecovery},
	{"real_run_kubectl_pod_watch_library_execution_mode_reaches_the_api_server_directly", testDeployRealRunKubectlPodWatchLibraryExecutionModeReachesTheAPIServerDirectly},
	{"real_run_pod_watch_logs_clean_rollout", testDeployRealRunPodWatchLogsCleanRollout},
	{"real_run_pod_watch_waits_through_image_pull_backoff", testDeployRealRunPodWatchWaitsThroughImagePullBackoff},
	{"real_run_reports_a_deadline_that_expired_while_the_image_was_still_pulling", testDeployRealRunReportsADeadlineThatExpiredWhileTheImageWasStillPulling},
	{"real_run_pod_watch_aborts_on_permanent_image_pull_failure", testDeployRealRunPodWatchAbortsOnPermanentImagePullFailure},
	{"real_run_pod_watch_aborts_on_crashloop_after_threshold", testDeployRealRunPodWatchAbortsOnCrashloopAfterThreshold},
	{"real_run_pod_watch_waits_out_the_unschedulable_grace_period", testDeployRealRunPodWatchWaitsOutTheUnschedulableGracePeriod},
	{"real_run_published_chart_not_found_reports_actionable_error", testDeployRealRunPublishedChartNotFoundReportsActionableError},
	{"dry_run_snapshot_version_resets_postgres_database", testDeployDryRunSnapshotVersionResetsPostgresDatabase},
	{"dry_run_released_version_does_not_reset_postgres_database", testDeployDryRunReleasedVersionDoesNotResetPostgresDatabase},
	{"dry_run_from_chart_cwd_resolves_single_chart_context", testDeployDryRunFromChartCwdResolvesSingleChartContext},
	{"devops_k8s_deploy_component_dry_run", testDeployDevopsK8sDeployComponentDryRun},
	{"devops_k8s_deploy_component_in_pod_local_agent_runtime_refused", testDeployDevopsK8sDeployComponentInPodLocalAgentRuntimeRefused},
	{"devops_k8s_deploy_component_in_pod_local_agent_non_runtime_allowed", testDeployDevopsK8sDeployComponentInPodLocalAgentNonRuntimeAllowed},
	{"devops_k8s_deploy_component_in_pod_non_local_agent_runtime_refused", testDeployDevopsK8sDeployComponentInPodNonLocalAgentRuntimeRefused},
	{"real_run_parallel_step_deploys_charts_concurrently", testDeployRealRunParallelStepDeploysChartsConcurrently},
	{"real_run_dedup_skip_when_identical_marker_alive", testDeployRealRunDedupSkipWhenIdenticalMarkerAlive},
	{"real_run_dedup_reclaims_aged_marker", testDeployRealRunDedupReclaimsAgedMarker},
	{"real_run_dedup_replaces_unreadable_marker", testDeployRealRunDedupReplacesUnreadableMarker},
	{"real_run_dedup_conflict_on_different_params", testDeployRealRunDedupConflictOnDifferentParams},
	{"real_run_dedup_reclaim_when_marker_pid_dead", testDeployRealRunDedupReclaimWhenMarkerPIDDead},
	{"dry_run_dedup_skip_when_identical_marker_alive", testDeployDryRunDedupSkipWhenIdenticalMarkerAlive},
	{"dry_run_dedup_conflict_on_different_params", testDeployDryRunDedupConflictOnDifferentParams},
	{"dry_run_dedup_reclaim_when_marker_pid_dead", testDeployDryRunDedupReclaimWhenMarkerPIDDead},
	{"dry_run_rethreads_recorded_mcp_auth_public_key", testDeployDryRunRethreadsRecordedMCPAuthPublicKey},
	{"dry_run_no_mcp_auth_drops_recorded_key", testDeployDryRunNoMCPAuthDropsRecordedKey},
	{"dry_run_refuses_to_downgrade_live_mcp_auth", testDeployDryRunRefusesToDowngradeLiveMCPAuth},
	{"dry_run_no_mcp_auth_clears_live_mcp_auth", testDeployDryRunNoMCPAuthClearsLiveMCPAuth},
	{"real_run_records_then_clears_mcp_auth_public_key", testDeployRealRunRecordsThenClearsMCPAuthPublicKey},
	{"real_run_failed_rollout_still_records_mcp_auth_public_key", testDeployRealRunFailedRolloutStillRecordsMCPAuthPublicKey},
	{"dry_run_refusal_names_the_desktop_identity_key_the_release_trusts", testDeployDryRunRefusalNamesTheDesktopIdentityKeyTheReleaseTrusts},
	{"dry_run_refusal_names_the_secret_when_the_key_is_not_this_host_s", testDeployDryRunRefusalNamesTheSecretWhenTheKeyIsNotThisHostS},
	{"dry_run_refusal_for_an_oidc_release_points_at_the_issuer_not_a_key", testDeployDryRunRefusalForAnOIDCReleasePointsAtTheIssuerNotAKey},
	{"in_pod_local_agent_runtime_deploy_refused", testDeployInPodLocalAgentRuntimeDeployRefused},
	{"in_pod_remote_agent_runtime_deploy_refused", testDeployInPodRemoteAgentRuntimeDeployRefused},
	{"in_pod_runtime_env_runtime_deploy_refused", testDeployInPodRuntimeEnvRuntimeDeployRefused},
	{"in_pod_runtime_env_explicit_runtime_component_refused", testDeployInPodRuntimeEnvExplicitRuntimeComponentRefused},
	{"in_pod_runtime_env_saved_selection_refused_by_the_runtime_chart_guard", testDeployInPodRuntimeEnvSavedSelectionRefusedByTheRuntimeChartGuard},
	{"in_pod_local_agent_component_deploy_allowed", testDeployInPodLocalAgentComponentDeployAllowed},
	{"real_run_reports_stale_runtime_port_forward", testDeployRealRunReportsStaleRuntimePortForward},
	{"real_run_silent_when_env_has_no_tracked_port_forward", testDeployRealRunSilentWhenEnvHasNoTrackedPortForward},
}

func TestDeploy(t *testing.T) {
	t.Parallel()
	for _, scenario := range deployScenarios {
		t.Run(scenario.name, scenario.run)
	}
}

func testDeployDryRunCopiesImagesFromToBeforeDeploy(t *testing.T) {
	// When the project registry list marks a FROM source and a TO
	// destination, deploy mirrors every image the cluster pulls (the
	// erun-devops runtime image plus any locally-built component) from FROM
	// to each TO with a manifest-aware `docker buildx imagetools create`
	// before the helm upgrade, and the cluster pulls from the DEPLOY (TO)
	// registry. The copy is a dry-run trace line gated behind real-run.
	setup := env.New(t)
	fixture.SeedTenantEnvNoRegistry(t, setup, "team", "dev")
	fixture.SeedDevopsRepo(t, setup, "team", "dev")
	fixture.SeedProjectK8sConfig(t, setup,
		"containerregistries:\n"+
			"    - registry: ghcr.io/sophium\n"+
			"      roles: [build, from]\n"+
			"    - registry: registry.internal/team\n"+
			"      roles: [to, deploy]\n",
	)
	result := erun.Run(t, []string{"deploy", "team", "dev", "--version", "1.0.0", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "deploy/dry_run_copies_images_from_to_before_deploy", normalize.Apply(result.Combined))
}

func testDeployRealRunCopiesImagesFromToViaStubs(t *testing.T) {
	// Real-run deploy of an env whose registry list marks a FROM source and
	// a TO destination: before helm upgrade, the runtime image is mirrored
	// from FROM to TO with `docker buildx imagetools create` (executed
	// through the docker stub), and the cluster pulls from the DEPLOY (TO)
	// registry. This proves the copy ACTION runs in real-run, not just its
	// dry-run trace.
	setup := env.New(t)
	root := filepath.Join(setup.ConfigHome, "erun")
	tenantDir := filepath.Join(root, "team")
	envDir := filepath.Join(tenantDir, "dev")
	for _, dir := range []string{root, tenantDir, envDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", dir, err)
		}
	}
	mustWriteFile(t, filepath.Join(root, "config.yaml"), "defaulttenant: team\n")
	mustWriteFile(t, filepath.Join(tenantDir, "config.yaml"),
		"projectroot: /nonexistent-remote/team\nname: team\ndefaultenvironment: dev\n")
	mustWriteFile(t, filepath.Join(envDir, "config.yaml"),
		"name: dev\nrepopath: /nonexistent-remote/team\nkubernetescontext: test-context\ntype: remote-agent\nruntimeversion: 1.0.0\n"+
			"containerregistries:\n"+
			"    - registry: ghcr.io/sophium\n      roles: [build, from]\n"+
			"    - registry: registry.internal/team\n      roles: [to, deploy]\n")
	stubs := setup.Cwd + "/stubs"
	fixture.StubBinary(t, stubs, "kubectl", "")
	fixture.StubBinary(t, stubs, "helm", "")
	fixture.StubBinary(t, stubs, "docker", "")
	// The ERUN_PUBLISHED_CHART_PROBE_OVERRIDE seam marks erun-devops published
	// so the runtime chart ladder confirms it instead of refusing.
	envVars := append(setup.Env(), "ERUN_PUBLISHED_CHART_PROBE_OVERRIDE=erun-devops:1.0.0")
	envVars = append(envVars, fixture.StubEnv(stubs, "kubectl", "helm", "docker")...)
	result := erun.Run(t, []string{"deploy", "team", "dev", "--version", "1.0.0"}, erun.RunOptions{Cwd: setup.Home, Env: envVars})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "deploy/real_run_copies_images_from_to_via_stubs", normalize.Apply(result.Combined))
}

func testDeployRealRunPreflightStartsStoppedCloudContext(t *testing.T) {
	// Exercises CloudContextPreflight end to end: the env's kubernetes
	// context belongs to a managed cloud context whose live AWS state is
	// "stopped", so the deploy must start the instance (full
	// StartCloudContext flow through the aws stub) before any kubectl or
	// helm call targets the cluster. Dry-run cannot reach the start: the
	// dry-run describe-instances canned answer reports "running" by
	// design so plans never spuriously start contexts.
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	fixture.SeedDevopsRepo(t, setup, "team", "dev")
	// Pin an always-permitting working-hours window so the preflight's
	// gate clears no matter when the suite runs; without it the default
	// 08:00-20:00 local window fails this scenario outside office hours.
	// The permitting and gated arms themselves are locked by
	// context_test.go with explicit windows.
	envConfigPath := filepath.Join(setup.ConfigHome, "erun", "team", "dev", "config.yaml")
	envBody, err := os.ReadFile(envConfigPath)
	if err != nil {
		t.Fatalf("read env config: %v", err)
	}
	envBody = append(envBody, []byte("idle:\n  workinghours: 00:00-23:59\n  timezone: UTC\n")...)
	if err := os.WriteFile(envConfigPath, envBody, 0o644); err != nil {
		t.Fatalf("write env config: %v", err)
	}
	// The cloud context owns the env's kubernetes context name.
	seedCloudConfigWithContexts(t, setup, contextYAMLItem("test-context", "dev", "us-east-1", "i-0123456789abcdef0"))
	stubs := setup.Cwd + "/stubs"
	profileARN := "arn:aws:iam::123456789012:instance-profile/erun-test-context-host-stop"
	envVars := append(setup.Env(), fixture.StubAWSCloudContext(t, stubs, fixture.AWSCloudContextStubSpec{
		RoleName:             "erun-test-context-host-stop",
		InstanceProfileARN:   profileARN,
		ProfileRoleName:      "erun-test-context-host-stop",
		ActiveAssociationID:  "iip-assoc-0aa11bb22cc33dd44",
		ActiveAssociationARN: profileARN,
		InstanceStates:       "i-0123456789abcdef0\tstopped",
	})...)
	fixture.StubBinary(t, stubs, "kubectl", "")
	fixture.StubBinary(t, stubs, "helm", "")
	fixture.StubBinary(t, stubs, "docker", "")
	envVars = append(envVars, fixture.StubEnv(stubs, "kubectl", "helm", "docker")...)
	result := erun.Run(t, []string{"deploy", "team", "dev", "--version", "1.0.0"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "deploy/real_run_preflight_starts_stopped_cloud_context", normalize.Apply(result.Combined))
}

func envConfigPathFor(setup env.Setup, tenant, environment string) string {
	return filepath.Join(setup.ConfigHome, "erun", tenant, environment, "config.yaml")
}

func readEnvConfig(t *testing.T, setup env.Setup, tenant, environment string) string {
	t.Helper()
	raw, err := os.ReadFile(envConfigPathFor(setup, tenant, environment))
	if err != nil {
		t.Fatalf("read env config: %v", err)
	}
	return string(raw)
}

func appendEnvConfig(t *testing.T, setup env.Setup, tenant, environment, body string) {
	t.Helper()
	mustWriteFile(t, envConfigPathFor(setup, tenant, environment), readEnvConfig(t, setup, tenant, environment)+body)
}

// assertEnvConfigContains checks the env config erun persisted. Persisted state
// sits outside the streams the goldens snapshot, so it needs its own assertion.
func assertEnvConfigContains(t *testing.T, setup env.Setup, tenant, environment string, want ...string) {
	t.Helper()
	body := readEnvConfig(t, setup, tenant, environment)
	for _, line := range want {
		if !strings.Contains(body, line) {
			t.Fatalf("expected env config to record %q, got:\n%s", line, body)
		}
	}
}

// assertEnvConfigLacks is the other half: a dry run traces what it would write,
// and the proof that it wrote nothing is on disk, not in the streams.
func assertEnvConfigLacks(t *testing.T, setup env.Setup, tenant, environment string, unwanted ...string) {
	t.Helper()
	body := readEnvConfig(t, setup, tenant, environment)
	for _, line := range unwanted {
		if strings.Contains(body, line) {
			t.Fatalf("expected env config not to record %q, got:\n%s", line, body)
		}
	}
}

// deployStubEnv declares the external binaries a real-run deploy reaches. The
// scenario PATH is scrubbed, so nothing ambient stands in for them; the stubs
// exit 0 so the rollout completes and the post-deploy persist runs.
func deployStubEnv(t *testing.T, setup env.Setup, extra ...string) []string {
	t.Helper()
	stubs := setup.Cwd + "/stubs"
	fixture.StubBinary(t, stubs, "kubectl", "")
	fixture.StubBinary(t, stubs, "helm", "")
	fixture.StubBinary(t, stubs, "docker", "")
	envVars := append(setup.Env(), fixture.StubEnv(stubs, "kubectl", "helm", "docker")...)
	return append(envVars, extra...)
}
