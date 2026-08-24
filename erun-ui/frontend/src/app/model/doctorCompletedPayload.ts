export interface DoctorCompletedPayload {
  tenant: string;
  environment: string;
  success: boolean;
  message?: string;
}
