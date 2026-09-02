// GlobalConfigCloudProviderBusyAction names the in-flight cloud-alias-row
// action Settings' cloud aliases section is running, so the busy spinner and
// disabled state land on the one button the operator actually clicked.
export type GlobalConfigCloudProviderBusyAction =
  | 'cloud-provider-login'
  | 'cloud-provider-logout'
  | 'cloud-provider-switch';
