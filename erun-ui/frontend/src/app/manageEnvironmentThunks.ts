// Manage-environment thunk facade. The implementation is split across
// per-domain modules; this file re-exports their public symbols so existing
// consumers can keep importing from `@/app/manageEnvironmentThunks` while the
// underlying code stays under the per-file line budget.

export { startManageCloudContext, stopManageCloudContext } from './manageCloudContextThunks';
export { submitManageDelete } from './manageDeleteThunks';
export { submitManageDeploy } from './manageDeployThunks';
export { manageDialogTabHasUnsavedChanges } from './manageDialogHelpers';
export {
  chooseWorkspaceSyncLocalFolder,
  closeManageDialog,
  loadManageConfig,
  openManageDialog,
  saveManageDeployComponents,
  selectManageVersionSuggestion,
  setManageTab,
  setManageVersionChoicesOpen,
  submitManageConfig,
  toggleManageDeployComponent,
  toggleManageVersionChoices,
  updateManageClaudeConfig,
  updateManageCloudAliasSlot,
  updateManageConfig,
  updateManageDialog,
  updateManageSSHDConfig,
} from './manageDialogThunks';
export { enableManageSSHD, startManageDoctor } from './manageHiddenSessionThunks';
