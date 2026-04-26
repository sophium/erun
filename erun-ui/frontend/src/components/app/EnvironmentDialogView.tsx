import * as React from 'react';
import { FolderPlus, LoaderCircle, Rocket } from 'lucide-react';

import type { ERunUIController } from '@/app/ERunUIController';
import { readError } from '@/app/errors';
import type { AppState } from '@/app/state';
import { findVersionSuggestion, selectedVersionSourceText } from '@/app/versionSuggestions';
import { Button } from '@/components/ui/button';
import { Checkbox } from '@/components/ui/checkbox';
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { VersionField } from './VersionField';

export function EnvironmentDialogView({ controller, state }: { controller: ERunUIController; state: AppState }): React.ReactElement {
  const dialog = state.environmentDialog;
  const tenantRef = React.useRef<HTMLInputElement>(null);
  const environmentRef = React.useRef<HTMLInputElement>(null);
  const isDeploy = dialog.actionMode === 'deploy';
  const submitLabel = isDeploy ? 'Deploy' : 'Create';
  const busyLabel = isDeploy ? 'Deploying...' : 'Creating...';

  React.useEffect(() => {
    if (!dialog.open) {
      return undefined;
    }
    const timeout = window.setTimeout(() => {
      const target = dialog.tenant ? environmentRef.current : tenantRef.current;
      target?.focus();
      target?.select();
    }, 0);
    return () => window.clearTimeout(timeout);
  }, [dialog.open]);

  return (
    <Dialog open={dialog.open} onOpenChange={(open) => !open && controller.closeEnvironmentDialog()}>
      <DialogContent
        className="sm:max-w-md"
        onCloseAutoFocus={(event) => {
          event.preventDefault();
          controller.focusTerminalSoon();
        }}
      >
        <form
          className="grid gap-4"
          onSubmit={(event) => {
            event.preventDefault();
            void controller.submitEnvironmentDialog(event.currentTarget).catch((error: unknown) => {
              controller.showTerminalMessage(readError(error));
            });
          }}
        >
          <DialogHeader>
            <DialogTitle>
              {isDeploy ? 'Deploy environment' : 'New environment'}
            </DialogTitle>
            <DialogDescription>
              {dialog.tenant && dialog.environment ? `${dialog.tenant} / ${dialog.environment}` : 'Enter the tenant and environment name.'}
            </DialogDescription>
          </DialogHeader>
          <div className="grid gap-2">
            <Label htmlFor="environment-tenant">Tenant</Label>
            <Input
              id="environment-tenant"
              ref={tenantRef}
              value={dialog.tenant}
              type="text"
              autoComplete="off"
              spellCheck={false}
              required
              disabled={dialog.busy || isDeploy}
              onChange={(event) => controller.updateEnvironmentDialog({ tenant: event.target.value })}
            />
          </div>
          <div className="grid gap-2">
            <Label htmlFor="environment-name">Environment</Label>
            <Input
              id="environment-name"
              ref={environmentRef}
              value={dialog.environment}
              type="text"
              autoComplete="off"
              spellCheck={false}
              required
              disabled={dialog.busy || isDeploy}
              onChange={(event) => controller.updateEnvironmentDialog({ environment: event.target.value })}
            />
          </div>
          <VersionField
            id="environment-version"
            value={dialog.version}
            sourceText={selectedVersionSourceText(findVersionSuggestion(state.versionSuggestions, dialog.version, dialog.versionImage))}
            suggestions={state.versionSuggestions}
            choicesOpen={dialog.choicesOpen}
            required={isDeploy}
            disabled={dialog.busy}
            onValueChange={(version) => controller.updateEnvironmentDialog({ version })}
            onChoicesOpenChange={(open) => controller.setEnvironmentVersionChoicesOpen(open)}
            onSelect={(suggestion) => controller.selectEnvironmentVersionSuggestion(suggestion)}
          />
          <div className="flex items-center gap-2">
            <Checkbox
              id="environment-no-git"
              checked={dialog.noGit}
              disabled={dialog.busy}
              onCheckedChange={(checked) => controller.updateEnvironmentDialog({ noGit: checked === true })}
            />
            <Label htmlFor="environment-no-git" className="text-sm font-normal">
              Initialize without Git checkout
            </Label>
          </div>
          {dialog.error && (
            <div className="dialog-error" role="alert">
              {dialog.error}
            </div>
          )}
          <DialogFooter>
            <Button type="button" variant="outline" size="sm" disabled={dialog.busy} onClick={() => controller.closeEnvironmentDialog()}>
              Cancel
            </Button>
            <Button type="submit" size="sm" disabled={dialog.busy}>
              {dialog.busy ? (
                <LoaderCircle className="animate-spin" aria-hidden="true" />
              ) : isDeploy ? (
                <Rocket aria-hidden="true" />
              ) : (
                <FolderPlus aria-hidden="true" />
              )}
              {dialog.busy ? busyLabel : submitLabel}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}
