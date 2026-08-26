import * as React from 'react';

import { ImagePullSecretsField } from '@/components/app/ImagePullSecretsField';
import { TextField } from '@/components/app/ManageDialog.fields';
import type { UIEnvironmentConfig } from '@/types';

// The two coordinates that decide whether the runtime pod can pull at all:
// where erun's own chart comes from, and the secrets the image is pulled with.
// Grouped so the General tab reads as sections rather than a flat field list.
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
