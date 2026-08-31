import {
  Button,
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from 'erun-kit';
import { LoaderCircle } from 'lucide-react';
import * as React from 'react';

import type { PlatformTenant } from '../app/api/tenantsApi';
import { usePlatformEnrollController } from '../identity/platformEnrollController';
import {
  FirstUserNotice,
  PlatformEnrollFeedback,
  PlatformEnrollUsernameFields,
} from '../identity/PlatformEnrollFields';

// EnrollTenantUserDialog is the Tenants view's own way to put a user into a
// tenant (erun#1744) — offered where the empty tenant is actually visible,
// rather than only from the Users view with a tenant picker. The target
// tenant is fixed to the row it was opened from, so there is no selector
// here at all, only the same username/issuer/subject fields and first-user
// notice EnrollUserForm uses.
export function EnrollTenantUserDialog({
  tenant,
  token,
  onClose,
}: {
  tenant: PlatformTenant;
  token: string;
  onClose: () => void;
}): React.ReactElement {
  const [username, setUsername] = React.useState('');
  const [issuer, setIssuer] = React.useState('');
  const [subject, setSubject] = React.useState('');
  const { state, enroll } = usePlatformEnrollController(token);
  const busy = state.status === 'enrolling';
  const becameFirstUser = tenant.userCount === 0;

  const submit = (event: React.SyntheticEvent): void => {
    event.preventDefault();
    enroll({
      username: username.trim(),
      issuer: issuer.trim() || undefined,
      subject: subject.trim() || undefined,
      tenantId: tenant.tenantId,
    });
  };

  return (
    <Dialog
      open
      onOpenChange={(open) => {
        if (!open && !busy) {
          onClose();
        }
      }}
    >
      <DialogContent aria-labelledby="enroll-tenant-user-heading">
        <DialogHeader>
          <DialogTitle id="enroll-tenant-user-heading">
            Enroll a user into {tenant.name}
          </DialogTitle>
          <DialogDescription>
            Writes a user row directly into this tenant, the same way `erun platform user enroll
            --tenant-id` does.
          </DialogDescription>
        </DialogHeader>
        <form className="grid gap-3" onSubmit={submit}>
          <PlatformEnrollUsernameFields
            username={username}
            setUsername={setUsername}
            issuer={issuer}
            setIssuer={setIssuer}
            subject={subject}
            setSubject={setSubject}
          />
          <FirstUserNotice tenant={tenant} />
          <PlatformEnrollFeedback
            state={state}
            tenants={[tenant]}
            becameFirstUser={becameFirstUser}
          />
          <DialogFooter>
            <Button type="button" variant="outline" onClick={onClose}>
              Close
            </Button>
            <Button type="submit" disabled={busy}>
              {busy && <LoaderCircle className="animate-spin" aria-hidden="true" />}
              Enroll user
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}
