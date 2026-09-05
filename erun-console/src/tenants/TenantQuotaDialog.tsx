import {
  Button,
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
  FieldLabel,
  Input,
} from 'erun-kit';
import { LoaderCircle } from 'lucide-react';
import * as React from 'react';

import type { SetTenantQuotaInput, TenantQuota } from '../app/api/quotaApi';
import { useSetTenantQuotaMutation } from '../app/api/quotaApi';
import { queryErrorMessage } from '../app/queryError';
import { useQuotaController } from '../quota/controller';

// QUOTA_FIELDS drives both the form's inputs and the request body, so a new
// cap only needs one new entry here rather than a field repeated per form and
// per submit handler.
const QUOTA_FIELDS: { key: keyof SetTenantQuotaInput; label: string; suffix: string }[] = [
  { key: 'maxEnvironments', label: 'Environments', suffix: '' },
  { key: 'maxCpuMillicores', label: 'Per-environment CPU', suffix: 'm' },
  { key: 'maxMemoryMb', label: 'Per-environment memory', suffix: 'MB' },
  { key: 'maxStorageGb', label: 'Per-environment storage', suffix: 'GB' },
  { key: 'maxTotalCpuMillicores', label: 'Total CPU', suffix: 'm' },
  { key: 'maxTotalMemoryMb', label: 'Total memory', suffix: 'MB' },
  { key: 'maxTotalStorageGb', label: 'Total storage', suffix: 'GB' },
];

type QuotaFieldValues = Record<keyof SetTenantQuotaInput, string>;

const EMPTY_VALUES: QuotaFieldValues = {
  maxEnvironments: '',
  maxCpuMillicores: '',
  maxMemoryMb: '',
  maxStorageGb: '',
  maxTotalCpuMillicores: '',
  maxTotalMemoryMb: '',
  maxTotalStorageGb: '',
};

function parseQuotaInput(values: QuotaFieldValues): SetTenantQuotaInput {
  return {
    maxEnvironments: Number(values.maxEnvironments),
    maxCpuMillicores: Number(values.maxCpuMillicores),
    maxMemoryMb: Number(values.maxMemoryMb),
    maxStorageGb: Number(values.maxStorageGb),
    maxTotalCpuMillicores: Number(values.maxTotalCpuMillicores),
    maxTotalMemoryMb: Number(values.maxTotalMemoryMb),
    maxTotalStorageGb: Number(values.maxTotalStorageGb),
  };
}

// quotaToFieldValues renders a loaded TenantQuota back into the same string
// shape the form's inputs hold, so a PUT that fully replaces the row (see
// setTenantQuotaRequest's own comment) starts from the target tenant's actual
// current caps instead of a blank form the operator would otherwise have to
// author blind.
function quotaToFieldValues(quota: TenantQuota): QuotaFieldValues {
  return {
    maxEnvironments: String(quota.maxEnvironments),
    maxCpuMillicores: String(quota.maxCpuMillicores),
    maxMemoryMb: String(quota.maxMemoryMb),
    maxStorageGb: String(quota.maxStorageGb),
    maxTotalCpuMillicores: String(quota.maxTotalCpuMillicores),
    maxTotalMemoryMb: String(quota.maxTotalMemoryMb),
    maxTotalStorageGb: String(quota.maxTotalStorageGb),
  };
}

function QuotaFields({
  values,
  onChange,
}: {
  values: QuotaFieldValues;
  onChange: (key: keyof SetTenantQuotaInput, value: string) => void;
}): React.ReactElement {
  return (
    <div className="grid grid-cols-2 gap-3">
      {QUOTA_FIELDS.map((field) => (
        <div className="grid gap-2" key={field.key}>
          <FieldLabel htmlFor={`quota-${field.key}`} required>
            {field.label}
            {field.suffix !== '' ? ` (${field.suffix})` : ''}
          </FieldLabel>
          <Input
            id={`quota-${field.key}`}
            type="number"
            min={0}
            value={values[field.key]}
            onChange={(e) => {
              onChange(field.key, e.target.value);
            }}
            required
          />
        </div>
      ))}
    </div>
  );
}

// TenantQuotaDialog is the operations-only cross-tenant write side of the
// quota surface (PUT /v1/tenants/{tenant_id}/quota) -- the counterpart to
// every tenant's own read-only QuotaPanel. A PUT always fully replaces the
// row, so every field is required on every submit, matching the backend's
// own validation (erun-backend-api/internal/routes/tenant_quotas.go).
export function TenantQuotaDialog({
  tenantId,
  tenantName,
  token,
  onClose,
}: {
  tenantId: string;
  tenantName: string;
  token: string;
  onClose: () => void;
}): React.ReactElement {
  const [values, setValues] = React.useState<QuotaFieldValues>(EMPTY_VALUES);
  const [setTenantQuota, setTenantQuotaState] = useSetTenantQuotaMutation();
  const busy = setTenantQuotaState.isLoading;
  const currentQuotaState = useQuotaController(token, tenantId);
  const loadedQuota = currentQuotaState.status === 'ready' ? currentQuotaState.quota : undefined;

  // Seeds the form from the target tenant's actual current caps once they
  // load, the same "a fresh value always wins" resync RateLimitPanel uses for
  // its own single field.
  React.useEffect(() => {
    if (loadedQuota !== undefined) {
      setValues(quotaToFieldValues(loadedQuota));
    }
  }, [loadedQuota]);

  const submit = (event: React.SyntheticEvent): void => {
    event.preventDefault();
    void setTenantQuota({ token, tenantId, input: parseQuotaInput(values) });
  };

  return (
    <Dialog
      open
      onOpenChange={(open) => {
        if (!open) {
          onClose();
        }
      }}
    >
      <DialogContent aria-labelledby="tenant-quota-heading">
        <DialogHeader>
          <DialogTitle id="tenant-quota-heading">Set quota for {tenantName}</DialogTitle>
          <DialogDescription>
            A PUT fully replaces this tenant&apos;s quota row — every cap below is required.
          </DialogDescription>
        </DialogHeader>
        <form className="grid gap-4" onSubmit={submit}>
          {currentQuotaState.status === 'loading' && (
            <p className="text-sm text-muted-foreground" role="status">
              Loading current quota…
            </p>
          )}
          {currentQuotaState.status === 'error' && (
            <p className="text-sm text-destructive" role="alert">
              Could not load the current quota: {currentQuotaState.message}
            </p>
          )}
          <QuotaFields
            values={values}
            onChange={(key, value) => {
              setValues((current) => ({ ...current, [key]: value }));
            }}
          />
          {setTenantQuotaState.isSuccess && (
            <p className="text-sm text-muted-foreground" role="status">
              Quota updated.
            </p>
          )}
          {setTenantQuotaState.isError && (
            <p className="text-sm text-destructive" role="alert">
              Could not set quota: {queryErrorMessage(setTenantQuotaState.error)}
            </p>
          )}
          <DialogFooter>
            <Button type="button" variant="outline" onClick={onClose}>
              Close
            </Button>
            <Button type="submit" disabled={busy}>
              {busy && <LoaderCircle className="animate-spin" aria-hidden="true" />}
              Save quota
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}
