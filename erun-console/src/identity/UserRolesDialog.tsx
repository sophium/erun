import {
  Button,
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
  SelectField,
} from 'erun-kit';
import { LoaderCircle } from 'lucide-react';
import * as React from 'react';

import type { Role } from '../app/api/rolesApi';
import { useUserRolesController } from './rolesController';

function HeldRolesList({
  roles,
  busyRoleId,
  onRevoke,
}: {
  roles: Role[];
  busyRoleId: string | undefined;
  onRevoke: (roleId: string) => void;
}): React.ReactElement {
  if (roles.length === 0) {
    return (
      <p className="text-sm text-muted-foreground" role="status">
        This user holds no roles yet.
      </p>
    );
  }
  return (
    <ul className="grid gap-2">
      {roles.map((role) => (
        <li key={role.roleId} className="flex items-center justify-between gap-2 text-sm">
          <span className="font-medium text-foreground">{role.name}</span>
          <Button
            type="button"
            variant="outline"
            size="sm"
            disabled={busyRoleId === role.roleId}
            onClick={() => {
              onRevoke(role.roleId);
            }}
          >
            {busyRoleId === role.roleId && (
              <LoaderCircle className="animate-spin" aria-hidden="true" />
            )}
            Revoke
          </Button>
        </li>
      ))}
    </ul>
  );
}

function GrantRoleForm({
  grantableRoles,
  busy,
  onGrant,
}: {
  grantableRoles: Role[];
  busy: boolean;
  onGrant: (roleId: string) => void;
}): React.ReactElement {
  const [selected, setSelected] = React.useState('');
  if (grantableRoles.length === 0) {
    return (
      <p className="text-sm text-muted-foreground" role="status">
        Every defined role is already granted to this user.
      </p>
    );
  }
  return (
    <div className="flex items-end gap-2">
      <SelectField
        id="grant-role-select"
        label="Grant a role"
        value={selected}
        onChange={setSelected}
        options={grantableRoles.map((role) => ({ value: role.roleId, label: role.name }))}
        placeholder="Choose a role"
      />
      <Button
        type="button"
        disabled={selected === '' || busy}
        onClick={() => {
          onGrant(selected);
          setSelected('');
        }}
      >
        {busy && <LoaderCircle className="animate-spin" aria-hidden="true" />}
        Grant
      </Button>
    </div>
  );
}

// UserRolesDialog is the grant/revoke surface for one enrolled user: which
// roles they hold today, a way to revoke one, and a picker for any tenant
// role they do not yet hold. Revoking the tenant's last
// grant-capable role is refused server-side (the lockout guard) and surfaces
// here as an ordinary action error, the same as any other failed mutation.
export function UserRolesDialog({
  username,
  userId,
  token,
  onClose,
}: {
  username: string;
  userId: string;
  token: string;
  onClose: () => void;
}): React.ReactElement {
  const { tenantRoles, userRolesState, actionError, grant, revoke } = useUserRolesController(
    token,
    userId,
  );
  const [busyRoleId, setBusyRoleId] = React.useState<string | undefined>(undefined);

  const heldRoles = userRolesState.status === 'ready' ? userRolesState.roles : [];
  const heldRoleIds = new Set(heldRoles.map((role) => role.roleId));
  const grantableRoles = tenantRoles.filter((role) => !heldRoleIds.has(role.roleId));

  const handleGrant = (roleId: string): void => {
    setBusyRoleId(roleId);
    void grant(roleId).then(() => {
      setBusyRoleId(undefined);
    });
  };
  const handleRevoke = (roleId: string): void => {
    setBusyRoleId(roleId);
    void revoke(roleId).then(() => {
      setBusyRoleId(undefined);
    });
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
      <DialogContent aria-labelledby="user-roles-heading">
        <DialogHeader>
          <DialogTitle id="user-roles-heading">Roles for {username}</DialogTitle>
          <DialogDescription>
            Grant or revoke this user's access to erun API paths, one role at a time.
          </DialogDescription>
        </DialogHeader>
        <div className="grid gap-4">
          {userRolesState.status === 'loading' && (
            <p className="text-sm text-muted-foreground" role="status">
              Loading roles…
            </p>
          )}
          {userRolesState.status === 'error' && (
            <p className="text-sm text-destructive" role="alert">
              Could not load this user's roles: {userRolesState.message}
            </p>
          )}
          {userRolesState.status === 'ready' && (
            <HeldRolesList roles={heldRoles} busyRoleId={busyRoleId} onRevoke={handleRevoke} />
          )}
          <GrantRoleForm
            grantableRoles={grantableRoles}
            busy={busyRoleId !== undefined}
            onGrant={handleGrant}
          />
          {actionError !== undefined && (
            <p className="text-sm text-destructive" role="alert">
              {actionError}
            </p>
          )}
        </div>
        <DialogFooter>
          <Button type="button" variant="outline" onClick={onClose}>
            Done
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
