import { SelectField } from 'erun-kit';
import * as React from 'react';

import { describeServicePorts, exposeFormPatchForService } from '@/app/exposeServicePickController';
import { useAppDispatch } from '@/app/hooks';
import { updateExposeForm } from '@/app/manageEnvironmentThunks';
import type { AppState } from '@/app/state';

type ManageDialog = AppState['manageDialog'];

// ExposeServicePicker offers the Services the environment is actually running.
// Before it, exposing meant typing a name you had to know already -- and the
// Ingress backend was derived from that name as `<tenant>-<label>`, which only
// matches a chart erun scaffolded. Picking a real Service fills the backend in
// from the namespace, so a repo's own chart is routable without renaming
// anything it owns.
//
// It degrades rather than disappears: when the listing is restricted, failed,
// or empty, the label field below still accepts a typed name, so the form
// never becomes unusable because a read did not answer.
export function ExposeServicePicker({
  dialog,
}: {
  dialog: ManageDialog;
}): React.ReactElement | null {
  const dispatch = useAppDispatch();
  const listing = dialog.environmentServices;
  const tenant = dialog.selection?.tenant ?? '';
  const services = listing.services;
  if (listing.restricted) {
    return (
      <div className="text-[13px] leading-[1.35] text-muted-foreground">
        Your Kubernetes credentials cannot list this environment&apos;s services. Type the service
        name below instead.
      </div>
    );
  }
  if (listing.error) {
    return (
      <div role="alert" className="text-[13px] leading-[1.35] text-muted-foreground">
        Could not read this environment&apos;s services ({listing.error}). Type the service name
        below instead.
      </div>
    );
  }
  if (services.length === 0) {
    return (
      <div className="text-[13px] leading-[1.35] text-muted-foreground">
        This environment is running no services yet.
      </div>
    );
  }
  return (
    <SelectField
      id="expose-service-picker"
      label="Service"
      value={dialog.exposeForm.backendService}
      options={services.map((service) => ({
        value: service.name,
        label: service.hostname
          ? `${service.name} (${describeServicePorts(service)}) - already at ${service.hostname}`
          : `${service.name} (${describeServicePorts(service)})`,
      }))}
      placeholder="Pick a service to expose"
      disabled={dialog.exposeBusy}
      onChange={(name) => {
        const picked = services.find((service) => service.name === name);
        if (!picked) {
          return;
        }
        dispatch(updateExposeForm(exposeFormPatchForService(picked, tenant)));
      }}
    />
  );
}
