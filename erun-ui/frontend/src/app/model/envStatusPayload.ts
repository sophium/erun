// EnvStatusPayload status: '' healthy or opening, 'stopped', or 'failed'.
export interface EnvStatusPayload {
  tenant: string;
  environment: string;
  status: string;
}
