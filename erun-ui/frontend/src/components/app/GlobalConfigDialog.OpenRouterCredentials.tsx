import {
  Button,
  cn,
  Command,
  CommandEmpty,
  CommandGroup,
  CommandInput,
  CommandItem,
  CommandList,
  Input,
  Label,
  Popover,
  PopoverContent,
  PopoverTrigger,
} from 'erun-kit';
import { Check, ChevronsUpDown, LoaderCircle, RefreshCw } from 'lucide-react';
import * as React from 'react';

import type { UIGatewayCredentialCandidate, UIOpenRouterConfig } from '@/uiOpenRouterTypes';

const DEFAULT_TOKEN_KEY = 'token';

// GatewayCredentialFields names the Secret the gateway token is read from.
//
// The Secret lives per environment, so the names that exist are read from the
// environment namespaces and offered as choices — inventing a name the cluster
// does not carry is the failure this avoids. Both fields stay typeable, because
// a Secret may legitimately not exist yet when the catalog is first configured.
export function GatewayCredentialFields({
  gateway,
  candidates,
  problems,
  namespaces,
  disabled,
  loading,
  onFindCandidates,
  patch,
}: {
  gateway: UIOpenRouterConfig;
  candidates: UIGatewayCredentialCandidate[];
  problems: string[];
  namespaces: number;
  disabled?: boolean;
  loading: boolean;
  onFindCandidates: () => void;
  patch: (values: Partial<UIOpenRouterConfig>) => void;
}): React.ReactElement {
  const secret = gateway.authTokenSecret ?? '';
  const selected = candidates.find((candidate) => candidate.name === secret);
  return (
    <div className="grid gap-2">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <Label>Credential</Label>
        <Button
          type="button"
          variant="outline"
          size="sm"
          disabled={disabled === true || loading}
          onClick={onFindCandidates}
        >
          {loading ? (
            <LoaderCircle className="size-3.5 animate-spin" />
          ) : (
            <RefreshCw className="size-3.5" />
          )}
          {candidates.length > 0 ? 'Reload Secrets' : 'Find Secrets'}
        </Button>
      </div>
      <div className="grid gap-2 sm:grid-cols-2">
        <ChoiceField
          id="global-config-openrouter-secret"
          label="Credential Secret (optional)"
          value={secret}
          placeholder="erun-claude-gateway"
          options={candidates.map((candidate) => candidate.name)}
          disabled={disabled}
          onValueChange={(next) => {
            patch({ authTokenSecret: next });
          }}
        />
        <ChoiceField
          id="global-config-openrouter-secret-key"
          label="Secret key (optional)"
          value={gateway.authTokenKey ?? ''}
          placeholder={DEFAULT_TOKEN_KEY}
          // The keys come from the Secret that is actually selected, so the key
          // is picked from what that Secret carries rather than guessed.
          options={selected?.keys ?? []}
          disabled={disabled}
          onValueChange={(next) => {
            patch({ authTokenKey: next });
          }}
        />
      </div>
      <div className="text-[12px] leading-[1.4] text-muted-foreground">
        {credentialHelperText(candidates.length, namespaces)}
      </div>
      {/* Named rather than swallowed: a Secret that exists and cannot be read
          must not look like a Secret that is missing. */}
      {problems.length === 0 ? null : (
        <ul className="grid gap-1 text-[12px] leading-[1.4] text-destructive">
          {problems.map((problem) => (
            <li key={problem}>{problem}</li>
          ))}
        </ul>
      )}
    </div>
  );
}

// credentialHelperText says what was found and what an empty field means. An
// empty result still names the default, so the field is never a dead end.
function credentialHelperText(found: number, namespaces: number): string {
  const fallback = `Leaving an empty name uses erun-claude-gateway and its ${DEFAULT_TOKEN_KEY} key.`;
  if (found === 0) {
    return `A Secret in each environment's namespace. ${fallback} Find the Secrets that already exist to pick one.`;
  }
  const where = namespaces === 1 ? 'namespace' : 'namespaces';
  return `Found ${String(found)} in ${String(namespaces)} environment ${where}. ${fallback}`;
}

// ChoiceField is a field that is both typeable and pickable from a searched
// list. A Secret name may not exist yet, and a key may be one the operator adds
// later, so neither can be a closed list.
function ChoiceField({
  id,
  label,
  value,
  placeholder,
  options,
  disabled,
  onValueChange,
}: {
  id: string;
  label: string;
  value: string;
  placeholder: string;
  options: string[];
  disabled?: boolean;
  onValueChange: (value: string) => void;
}): React.ReactElement {
  const [open, setOpen] = React.useState(false);
  return (
    <div className="grid gap-1">
      <Label htmlFor={id}>{label}</Label>
      <div className="relative">
        <Input
          id={id}
          className={options.length > 0 ? 'pr-10' : undefined}
          autoComplete="off"
          value={value}
          disabled={disabled}
          placeholder={placeholder}
          onChange={(event) => {
            onValueChange(event.target.value);
          }}
        />
        {options.length > 0 ? (
          <Popover open={open} onOpenChange={setOpen}>
            <PopoverTrigger asChild>
              <Button
                className="absolute right-1 top-1 size-7 text-muted-foreground"
                type="button"
                variant="ghost"
                size="icon"
                aria-label={`Show ${label}`}
                disabled={disabled === true}
              >
                <ChevronsUpDown />
              </Button>
            </PopoverTrigger>
            <PopoverContent className="w-80 p-0" align="start" collisionPadding={12}>
              <Command>
                <CommandInput placeholder="Search..." />
                <CommandList>
                  <CommandEmpty>No match.</CommandEmpty>
                  <CommandGroup>
                    {options.map((option) => (
                      <CommandItem
                        key={option}
                        value={option}
                        onSelect={() => {
                          setOpen(false);
                          onValueChange(option);
                        }}
                      >
                        <Check
                          className={cn(
                            'size-4 shrink-0',
                            option === value ? 'opacity-100' : 'opacity-0',
                          )}
                        />
                        <span className="truncate text-sm">{option}</span>
                      </CommandItem>
                    ))}
                  </CommandGroup>
                </CommandList>
              </Command>
            </PopoverContent>
          </Popover>
        ) : null}
      </div>
    </div>
  );
}
