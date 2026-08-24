export interface SSHDInitCompletedPayload {
  tenant: string;
  environment: string;
  success: boolean;
  message?: string;
}
