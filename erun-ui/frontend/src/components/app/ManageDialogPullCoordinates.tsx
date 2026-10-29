import * as React from 'react';

import { ImagePullSecretsField } from '@/components/app/ImagePullSecretsField';
import { TextField } from '@/components/app/ManageDialog.fields';
import type { UIEnvironmentConfig } from '@/types';

// The chart source and pull secrets that decide whether the runtime pod can pull.
export function PullCoordinatesFields({
  config,
  disabled,
  onChange,
}: {
  config: UIEnvironmentConfig;
  disabled: boolean;
  onChange: (patch: Partial<UIEnvironmentConfig>) => void;
}): React.ReactElement {
  return (
    <>
      <TextField
        id="environment-config-runtimeregistry"
        label="Runtime registry"
        value={config.runtimeRegistry ?? ''}
        disabled={disabled}
        placeholder="ghcr.io/sophium"
        helper="Where this environment resolves erun's own chart and platform images from. Set it when the registries above hold only this project's images."
        onChange={(runtimeRegistry) => {
          onChange({ runtimeRegistry });
        }}
      />
      <ImagePullSecretsField
        secrets={config.imagePullSecrets ?? []}
        disabled={disabled}
        onChange={(imagePullSecrets) => {
          onChange({ imagePullSecrets });
        }}
      />
    </>
  );
}
