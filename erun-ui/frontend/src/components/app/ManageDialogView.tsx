import * as React from 'react';
import { Rocket, Trash2 } from 'lucide-react';

import type { ERunUIController } from '@/app/ERunUIController';
import { readError } from '@/app/errors';
import type { AppState } from '@/app/state';
import { deleteConfirmationValue, findVersionSuggestion, normalizeDialogValue, selectedVersionSourceText } from '@/app/versionSuggestions';
import { Button } from '@/components/ui/button';
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs';
import type { ManageTab } from '@/types';
import { VersionField } from './VersionField';

export function ManageDialogView({ controller, state }: { controller: ERunUIController; state: AppState }): React.ReactElement {
  const dialog = state.manageDialog;
  const versionRef = React.useRef<HTMLInputElement>(null);
  const confirmationRef = React.useRef<HTMLInputElement>(null);
  const selection = dialog.selection;
  const expected = selection ? deleteConfirmationValue(selection) : '';
  const deleteEnabled = !dialog.busy && normalizeDialogValue(dialog.confirmation) === expected;

  React.useEffect(() => {
    if (!dialog.open) {
      return;
    }
    window.setTimeout(() => {
      if (dialog.tab === 'deploy') {
        versionRef.current?.focus();
        versionRef.current?.select();
        return;
      }
      confirmationRef.current?.focus();
    }, 0);
  }, [dialog.open, dialog.tab]);

  return (
    <Dialog open={dialog.open} onOpenChange={(open) => !open && controller.closeManageDialog()}>
      <DialogContent className="sm:max-w-md">
        <form
          className="grid gap-4"
          onSubmit={(event) => {
            event.preventDefault();
            if (dialog.tab === 'deploy') {
              void controller.submitManageDeploy(event.currentTarget).catch((error: unknown) => {
                controller.showTerminalMessage(readError(error));
              });
            }
          }}
        >
          <DialogHeader>
            <DialogTitle>Manage environment</DialogTitle>
            <DialogDescription>
              {selection ? `${selection.tenant} / ${selection.environment}` : ''}
            </DialogDescription>
          </DialogHeader>
          <Tabs
            className="gap-3"
            value={dialog.tab}
            onValueChange={(value) => controller.setManageTab(value as ManageTab)}
          >
            <TabsList className="grid w-full grid-cols-2" aria-label="Environment actions">
              <TabsTrigger value="deploy" disabled={dialog.busy}>
                Deploy
              </TabsTrigger>
              <TabsTrigger value="delete" disabled={dialog.busy}>
                Delete
              </TabsTrigger>
            </TabsList>
            <div className="min-h-20">
              <TabsContent className="mt-0 grid gap-3" value="deploy">
                <VersionField
                  id="manage-version"
                  inputRef={versionRef}
                  value={dialog.version}
                  sourceText={selectedVersionSourceText(findVersionSuggestion(state.versionSuggestions, dialog.version, dialog.versionImage))}
                  suggestions={state.versionSuggestions}
                  choicesOpen={dialog.choicesOpen}
                  required
                  disabled={dialog.busy}
                  onValueChange={(version) => controller.updateManageDialog({ version })}
                  onChoicesOpenChange={(open) => controller.setManageVersionChoicesOpen(open)}
                  onSelect={(suggestion) => controller.selectManageVersionSuggestion(suggestion)}
                />
              </TabsContent>
              <TabsContent className="mt-0 grid gap-3" value="delete">
                <p className="text-sm text-muted-foreground">
                  {selection ? `Type ${expected} to delete ${selection.tenant} / ${selection.environment}.` : ''}
                </p>
                <div className="grid gap-2">
                  <Label htmlFor="manage-confirmation">Confirmation</Label>
                  <Input
                    id="manage-confirmation"
                    ref={confirmationRef}
                    value={dialog.confirmation}
                    type="text"
                    autoComplete="off"
                    spellCheck={false}
                    disabled={dialog.busy}
                    onChange={(event) => controller.updateManageDialog({ confirmation: event.target.value })}
                    onKeyDown={(event) => {
                      if (event.key === 'Enter') {
                        event.preventDefault();
                        void controller.submitManageDelete();
                      }
                    }}
                  />
                </div>
              </TabsContent>
            </div>
          </Tabs>
          <DialogFooter>
            <Button type="button" variant="outline" size="sm" disabled={dialog.busy} onClick={() => controller.closeManageDialog()}>
              Cancel
            </Button>
            {dialog.tab === 'deploy' ? (
              <Button type="submit" size="sm" disabled={dialog.busy}>
                <Rocket aria-hidden="true" />
                Deploy
              </Button>
            ) : (
              <Button
                type="button"
                variant={deleteEnabled || dialog.busy ? 'destructive' : 'outline'}
                size="sm"
                disabled={!deleteEnabled}
                onClick={() => {
                  void controller.submitManageDelete();
                }}
              >
                <Trash2 aria-hidden="true" />
                {dialog.busy ? 'Deleting...' : 'Delete'}
              </Button>
            )}
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}
