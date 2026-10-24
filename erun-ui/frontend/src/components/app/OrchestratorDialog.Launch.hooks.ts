import * as React from 'react';

import { readError } from '@/app/errors';
import type {
  OrchestratorEffort,
  OrchestratorInfo,
  OrchestratorLaunchSettings,
  OrchestratorModel,
  OrchestratorUseGateway,
} from '@/app/slices/orchestratorsSlice';
import {
  type OrchestratorLaunchChoices,
  orchestratorLaunchSettingsOf,
} from '@/components/app/OrchestratorDialog.Launch.helpers';

import { ListOrchestratorLaunchChoices } from '../../../wailsjs/go/main/App';

// OrchestratorLaunchForm holds the gateway, model and effort settings.
export interface OrchestratorLaunchForm {
  choices: OrchestratorLaunchChoices | null;
  choicesError: string;
  choicesLoading: boolean;
  // retryChoices re-reads the catalog after a failed read.
  retryChoices: () => void;
  settings: OrchestratorLaunchSettings;
  setGateway: (useGateway: OrchestratorUseGateway) => void;
  setModel: (model: OrchestratorModel) => void;
  setEffort: (effort: OrchestratorEffort) => void;
}

export function useOrchestratorLaunchForm(
  open: boolean,
  editing: OrchestratorInfo | null,
): OrchestratorLaunchForm {
  const [choices, setChoices] = React.useState<OrchestratorLaunchChoices | null>(null);
  const [choicesError, setChoicesError] = React.useState('');
  const [choicesLoading, setChoicesLoading] = React.useState(false);
  const [settings, setSettings] = React.useState<OrchestratorLaunchSettings>(() =>
    orchestratorLaunchSettingsOf(editing),
  );

  const readChoices = React.useCallback(() => {
    setChoicesLoading(true);
    ListOrchestratorLaunchChoices().then(
      (next) => {
        setChoices(next);
        setChoicesError('');
        setChoicesLoading(false);
      },
      (error: unknown) => {
        setChoices(null);
        setChoicesError(readError(error));
        setChoicesLoading(false);
      },
    );
  }, []);

  React.useEffect(() => {
    if (!open) {
      return;
    }
    setSettings(orchestratorLaunchSettingsOf(editing));
    setChoicesError('');
    readChoices();
  }, [open, editing, readChoices]);

  return {
    choices,
    choicesError,
    choicesLoading,
    retryChoices: readChoices,
    settings,
    setGateway: (useGateway) => {
      setSettings((current) => ({ ...current, useGateway }));
    },
    setModel: (model) => {
      setSettings((current) => ({ ...current, model }));
    },
    setEffort: (effort) => {
      setSettings((current) => ({ ...current, effort }));
    },
  };
}
