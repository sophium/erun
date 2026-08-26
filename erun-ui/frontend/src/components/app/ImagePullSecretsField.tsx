import { Button, Input, Label } from 'erun-kit';
import { Plus, Trash2 } from 'lucide-react';
import * as React from 'react';

// ImagePullSecretsField edits the Kubernetes secrets the runtime pod pulls its
// image with. It matters most when the environment carries a private runtime
// image: without a secret the pod never starts, and the empty state says so
// rather than leaving the operator to infer why a pod they created will not
// come up.
export function ImagePullSecretsField({
  secrets,
  disabled,
  onChange,
}: {
  secrets: string[];
  disabled?: boolean;
  onChange: (next: string[]) => void;
}): React.ReactElement {
  return (
    <div className="grid gap-2">
      <div className="flex items-center justify-between">
        <Label>Image pull secrets</Label>
        <Button
          id="environment-config-add-pull-secret"
          type="button"
          variant="outline"
          size="sm"
          disabled={disabled}
          onClick={() => {
            onChange([...secrets, '']);
          }}
          aria-label="Add image pull secret"
        >
          <Plus aria-hidden="true" />
          Add secret
        </Button>
      </div>
      {secrets.length === 0 ? (
        <p className="text-xs text-muted-foreground">
          None. A runtime image in a private registry needs a{' '}
          <code className="font-mono text-[11px]">dockerconfigjson</code> secret here, or the pod
          cannot pull it.
        </p>
      ) : (
        <div className="grid gap-2">
          {secrets.map((secret, index) => (
            <div key={index} className="flex items-center gap-2">
              <Input
                id={`environment-config-pull-secret-${String(index)}`}
                className="min-w-0 flex-1"
                value={secret}
                type="text"
                autoComplete="off"
                spellCheck={false}
                disabled={disabled}
                placeholder="ecr-pull"
                aria-label={`Image pull secret ${String(index + 1)}`}
                onChange={(event) => {
                  onChange(secrets.map((row, idx) => (idx === index ? event.target.value : row)));
                }}
              />
              <Button
                type="button"
                variant="ghost"
                size="icon"
                disabled={disabled}
                aria-label={`Remove image pull secret ${String(index + 1)}`}
                onClick={() => {
                  onChange(secrets.filter((_, idx) => idx !== index));
                }}
              >
                <Trash2 aria-hidden="true" />
              </Button>
            </div>
          ))}
        </div>
      )}
    </div>
  );
}
