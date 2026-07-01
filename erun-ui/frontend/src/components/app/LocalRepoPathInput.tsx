import * as React from 'react';

import { useChooseLocalRepoPathMutation } from '@/app/api/environmentApi';
import { readError } from '@/app/errors';
import { useAppDispatch } from '@/app/hooks';
import { showTerminalMessage } from '@/app/notificationThunks';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';

// LocalRepoPathInput is the shared "text input + native Browse picker" control
// for a local-agent env's host worktree path (EnvConfig.LocalRepoPath). Both the
// init dialog (EnvironmentTypeFields) and the env-settings General tab render it
// so the two stay in lockstep. Free-text is the correct control for a path the
// user genuinely authors; the Browse button opens the native OpenDirectoryDialog
// (via ChooseLocalRepoPath) so they need not type an absolute path by hand.
// Thunk-agnostic: the caller supplies value + onChange so init and settings can
// wire their own dialog state.
export function LocalRepoPathInput({
  id,
  label,
  helper,
  value,
  disabled,
  onChange,
}: {
  id: string;
  label: string;
  helper: string;
  value: string;
  disabled?: boolean;
  onChange: (value: string) => void;
}): React.ReactElement {
  const dispatch = useAppDispatch();
  const [chooseLocalRepoPath, { isLoading: choosing }] = useChooseLocalRepoPathMutation();
  const helperId = `${id}-helper`;
  const disableBrowse = Boolean(disabled) || choosing;
  const onBrowse = async (): Promise<void> => {
    try {
      const picked = await chooseLocalRepoPath(value).unwrap();
      if (picked.trim()) {
        onChange(picked);
      }
    } catch (error) {
      dispatch(showTerminalMessage(readError(error)));
    }
  };
  return (
    <div className="grid gap-2">
      <Label htmlFor={id}>{label}</Label>
      <div className="flex items-stretch gap-2">
        <Input
          id={id}
          value={value}
          type="text"
          autoComplete="off"
          spellCheck={false}
          placeholder="/Users/you/code/your-project"
          aria-describedby={helperId}
          disabled={disabled}
          onChange={(event) => {
            onChange(event.target.value);
          }}
          className="flex-1"
        />
        <Button
          type="button"
          variant="outline"
          disabled={disableBrowse}
          onClick={() => {
            void onBrowse();
          }}
        >
          Browse…
        </Button>
      </div>
      <p id={helperId} className="text-[12px] leading-[1.4] text-muted-foreground">
        {helper}
      </p>
    </div>
  );
}
