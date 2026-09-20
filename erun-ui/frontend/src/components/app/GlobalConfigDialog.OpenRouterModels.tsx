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
import { Check, ChevronsUpDown, LoaderCircle, Plus, RefreshCw, Trash2 } from 'lucide-react';
import * as React from 'react';

import { isClaudeModelToken } from '@/components/app/claudeModels.helpers';
import type { UIGatewayModel, UIOpenRouterModel } from '@/uiOpenRouterTypes';

// The catalog's model rows, split from the section that owns the catalog's
// state: these are presentational, taking the current rows and the edit
// callbacks, and the section keeps the dispatch and the credential wiring.

// OpenRouterModelsField edits the catalog rows: the model ids an environment may
// select, and each id's context window.
export function OpenRouterModelsField({
  models,
  candidates,
  disabled,
  onAddModel,
  onSetModel,
  onRemoveModel,
  onFetchCandidates,
  fetchingCandidates,
  canFetchCandidates,
  candidatesError,
}: {
  models: UIOpenRouterModel[];
  candidates: UIGatewayModel[];
  disabled?: boolean;
  onAddModel: () => void;
  onSetModel: (index: number, values: Partial<UIOpenRouterModel>) => void;
  onRemoveModel: (index: number) => void;
  onFetchCandidates: () => void;
  fetchingCandidates: boolean;
  canFetchCandidates: boolean;
  candidatesError: string;
}): React.ReactElement {
  return (
    <div className="grid gap-2">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <Label>Models</Label>
        <div className="flex flex-wrap items-center gap-2">
          <Button
            type="button"
            variant="outline"
            size="sm"
            disabled={!canFetchCandidates || fetchingCandidates}
            onClick={onFetchCandidates}
          >
            {fetchingCandidates ? (
              <LoaderCircle className="size-3.5 animate-spin" />
            ) : (
              <RefreshCw className="size-3.5" />
            )}
            {candidates.length > 0 ? 'Reload from gateway' : 'Load from gateway'}
          </Button>
          <Button
            type="button"
            variant="secondary"
            size="sm"
            disabled={disabled}
            onClick={onAddModel}
          >
            <Plus className="size-3.5" />
            Add model
          </Button>
        </div>
      </div>
      <div className="text-[12px] leading-[1.4] text-muted-foreground">
        {candidates.length > 0
          ? `Offering ${String(candidates.length)} models from the gateway. A window the gateway reports is filled in for you.`
          : 'Load the gateway’s own model list to pick ids, or type one and set its window.'}
      </div>
      {candidatesError === '' ? null : (
        <div className="text-[12px] leading-[1.4] text-destructive">{candidatesError}</div>
      )}
      {models.length === 0 ? (
        <div className="text-[12px] leading-[1.4] text-muted-foreground">
          No models yet. Add the gateway model ids an environment may select.
        </div>
      ) : (
        models.map((model, index) => (
          <OpenRouterModelRow
            key={index}
            model={model}
            index={index}
            candidates={candidates}
            disabled={disabled}
            onSetModel={onSetModel}
            onRemoveModel={onRemoveModel}
          />
        ))
      )}
      <div className="text-[12px] leading-[1.4] text-muted-foreground">
        A model&apos;s window must be the provider-level figure, which can be smaller than its
        advertised maximum. Leaving it empty makes Claude Code assume one.
      </div>
    </div>
  );
}

function OpenRouterModelRow({
  model,
  index,
  candidates,
  disabled,
  onSetModel,
  onRemoveModel,
}: {
  model: UIOpenRouterModel;
  index: number;
  candidates: UIGatewayModel[];
  disabled?: boolean;
  onSetModel: (index: number, values: Partial<UIOpenRouterModel>) => void;
  onRemoveModel: (index: number) => void;
}): React.ReactElement {
  const id = model.id.trim();
  const invalid = id !== '' && !isClaudeModelToken(id);
  return (
    <div
      data-openrouter-model={index}
      className="grid gap-2 sm:grid-cols-[1fr_10rem_auto] sm:items-start"
    >
      <div className="grid gap-1">
        <div className="relative">
          {/* The field stays typeable as well as pickable: a gateway serves ids
              it does not list, and refusing one would leave no way to reach it.
              The list is searched rather than scrolled, because a gateway can
              serve hundreds of models. */}
          <Input
            aria-label={`Model id ${String(index + 1)}`}
            className={candidates.length > 0 ? 'pr-10' : undefined}
            autoComplete="off"
            value={model.id}
            disabled={disabled}
            placeholder="deepseek/deepseek-v4.1-flash"
            aria-invalid={invalid}
            onChange={(event) => {
              onSetModel(index, { id: event.target.value });
            }}
          />
          {candidates.length > 0 ? (
            <GatewayModelChoices
              index={index}
              selectedId={id}
              candidates={candidates}
              disabled={disabled}
              onPick={(candidate) => {
                // A window the gateway reports comes with the pick; one that
                // reports none leaves whatever is there.
                onSetModel(index, {
                  id: candidate.id,
                  ...(candidate.context ? { context: candidate.context } : {}),
                });
              }}
            />
          ) : null}
        </div>
        {invalid ? (
          <div className="text-[12px] leading-[1.4] text-destructive">
            A model id may contain only letters, digits, and . _ : / -
          </div>
        ) : null}
      </div>
      <Input
        aria-label={`Context window for model ${String(index + 1)}`}
        type="number"
        inputMode="numeric"
        min={1}
        step={1}
        autoComplete="off"
        value={model.context === undefined ? '' : String(model.context)}
        disabled={disabled}
        placeholder="Context window"
        onChange={(event) => {
          const next = event.target.value.trim();
          // An empty window is unknown rather than zero, so the launch leaves
          // Claude Code's own assumption in place.
          onSetModel(index, { context: next === '' ? undefined : Math.trunc(Number(next)) });
        }}
      />
      <OpenRouterModelRowActions
        model={model}
        index={index}
        disabled={disabled}
        onSetModel={onSetModel}
        onRemoveModel={onRemoveModel}
      />
    </div>
  );
}

// OpenRouterModelRowActions holds everything on a catalog row that acts on the
// listing rather than editing its fields: the reasoning-echo declaration and the
// remove control.
function OpenRouterModelRowActions({
  model,
  index,
  disabled,
  onSetModel,
  onRemoveModel,
}: {
  model: UIOpenRouterModel;
  index: number;
  disabled?: boolean;
  onSetModel: (index: number, values: Partial<UIOpenRouterModel>) => void;
  onRemoveModel: (index: number) => void;
}): React.ReactElement {
  return (
    <div className="flex shrink-0 items-center gap-2">
      <label
        className="flex items-center gap-1.5 whitespace-nowrap text-[12px] leading-[1.4] text-muted-foreground"
        title="The provider demands the model's own reasoning back, which no erun AI lane can supply: a conversation on it is refused mid-run. Declared listings are not offered for selection and are never the model an environment starts on."
      >
        <input
          type="checkbox"
          aria-label={`Requires reasoning echo for model ${String(index + 1)}`}
          className="size-3.5 accent-current"
          checked={model.requiresReasoningEcho === true}
          disabled={disabled}
          onChange={(event) => {
            // An undeclared row and a declared-false one mean the same thing, so
            // clearing the box removes the key rather than storing a false every
            // reader would then have to interpret.
            onSetModel(index, { requiresReasoningEcho: event.target.checked ? true : undefined });
          }}
        />
        Reasoning echo
      </label>
      <Button
        type="button"
        variant="ghost"
        size="sm"
        aria-label={`Remove model ${String(index + 1)}`}
        disabled={disabled}
        onClick={() => {
          onRemoveModel(index);
        }}
      >
        <Trash2 className="size-3.5" />
      </Button>
    </div>
  );
}

// GatewayModelChoices is the searchable list of what the gateway serves. It is
// searched rather than scrolled because a gateway can serve hundreds of models,
// and a flat list of them is unusable.
function GatewayModelChoices({
  index,
  selectedId,
  candidates,
  disabled,
  onPick,
}: {
  index: number;
  selectedId: string;
  candidates: UIGatewayModel[];
  disabled?: boolean;
  onPick: (candidate: UIGatewayModel) => void;
}): React.ReactElement {
  const [open, setOpen] = React.useState(false);
  return (
    <Popover open={open} onOpenChange={setOpen}>
      <PopoverTrigger asChild>
        <Button
          className="absolute right-1 top-1 size-7 text-muted-foreground"
          type="button"
          variant="ghost"
          size="icon"
          aria-label={`Show gateway models for model ${String(index + 1)}`}
          disabled={disabled === true}
        >
          <ChevronsUpDown />
        </Button>
      </PopoverTrigger>
      <PopoverContent className="w-96 p-0" align="start" collisionPadding={12}>
        <Command>
          <CommandInput placeholder="Search models..." />
          <CommandList>
            <CommandEmpty>No model matches.</CommandEmpty>
            <CommandGroup>
              {candidates.map((candidate) => (
                <GatewayModelItem
                  key={candidate.id}
                  candidate={candidate}
                  selected={candidate.id === selectedId}
                  onSelect={() => {
                    setOpen(false);
                    onPick(candidate);
                  }}
                />
              ))}
            </CommandGroup>
          </CommandList>
        </Command>
      </PopoverContent>
    </Popover>
  );
}

// GatewayModelItem is one searchable choice: the name the gateway gives it, the
// id it actually launches with, and the window that comes with the pick. The
// entry's value carries both name and id so a search matches either — an
// operator may know the model by its name and the launch needs the id.
function GatewayModelItem({
  candidate,
  selected,
  onSelect,
}: {
  candidate: UIGatewayModel;
  selected: boolean;
  onSelect: () => void;
}): React.ReactElement {
  const name =
    candidate.displayName === '' ? candidate.id : (candidate.displayName ?? candidate.id);
  return (
    <CommandItem
      className="min-w-0"
      value={`${candidate.displayName ?? ''} ${candidate.id}`}
      onSelect={onSelect}
    >
      <Check className={cn('size-4 shrink-0 opacity-0', selected && 'opacity-100')} />
      <span className="flex min-w-0 flex-1 flex-col gap-0.5">
        <span className="truncate text-sm font-medium leading-tight">{name}</span>
        <span className="truncate text-xs leading-tight text-muted-foreground">
          {candidate.context
            ? `${candidate.id} | ${String(candidate.context)} tokens`
            : candidate.id}
        </span>
      </span>
    </CommandItem>
  );
}
