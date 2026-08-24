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
import { Check, ChevronsUpDown } from 'lucide-react';
import * as React from 'react';

import { useAppDispatch } from '@/app/hooks';
import { updateManageConfig } from '@/app/manageEnvironmentThunks';
import { runtimeChartChoices } from '@/app/runtimeChartChoices';
import type { AppState } from '@/app/state';

type ManageDialog = AppState['manageDialog'];

const PAIRED_LABEL = 'Published with the deployed version';

// RuntimeChartField states the second deploy coordinate: which chart this
// environment's runtime is installed from, independent of the version being
// deployed (which names the image). The paired default is a named choice rather
// than an empty field, so the common case is discoverable and the operator can
// always get back to it.
export function RuntimeChartField({ dialog }: { dialog: ManageDialog }): React.ReactElement {
  const dispatch = useAppDispatch();
  const [open, setOpen] = React.useState(false);
  const value = dialog.config.runtimeChart ?? '';
  const choices = runtimeChartChoices(dialog.versionSuggestions);
  const disabled = dialog.busy || dialog.configLoading;
  const setChart = (runtimeChart: string): void => {
    dispatch(updateManageConfig({ runtimeChart }));
  };
  return (
    <div className="grid gap-2">
      <Label htmlFor="environment-config-runtimechart" className="text-sm font-normal">
        Runtime chart
      </Label>
      <div className="relative min-w-0">
        <Input
          id="environment-config-runtimechart"
          className="pr-10"
          value={value}
          type="text"
          autoComplete="off"
          spellCheck={false}
          placeholder={PAIRED_LABEL}
          aria-describedby="environment-config-runtimechart-helper"
          disabled={disabled}
          onChange={(event) => {
            setChart(event.target.value);
          }}
        />
        <Popover open={open} onOpenChange={setOpen}>
          <PopoverTrigger asChild>
            <Button
              className="absolute right-1 top-1 size-7 text-muted-foreground"
              type="button"
              variant="ghost"
              size="icon"
              aria-label="Show runtime chart choices"
              disabled={disabled}
            >
              <ChevronsUpDown />
            </Button>
          </PopoverTrigger>
          <PopoverContent
            className="max-h-[var(--radix-popover-content-available-height)] w-[26rem] max-w-[calc(100vw-2rem)] overflow-y-auto p-0"
            align="start"
          >
            <Command>
              <CommandInput placeholder="Search charts..." />
              <CommandList>
                <CommandEmpty>No chart version found.</CommandEmpty>
                <CommandGroup heading="Runtime chart">
                  <RuntimeChartItem
                    label={PAIRED_LABEL}
                    detail="Use the chart published with the version being deployed."
                    selected={value.trim() === ''}
                    onSelect={() => {
                      setChart('');
                      setOpen(false);
                    }}
                  />
                  {choices.map((choice) => (
                    <RuntimeChartItem
                      key={choice.reference}
                      label={choice.label}
                      detail={choice.reference}
                      selected={value.trim() === choice.reference}
                      onSelect={() => {
                        setChart(choice.reference);
                        setOpen(false);
                      }}
                    />
                  ))}
                </CommandGroup>
              </CommandList>
            </Command>
          </PopoverContent>
        </Popover>
      </div>
      <div
        id="environment-config-runtimechart-helper"
        className="text-[12px] leading-[1.4] text-muted-foreground"
      >
        The chart this environment&apos;s runtime is installed from. Set it when the runtime image
        is versioned on your project&apos;s own release line — the chart is ERun&apos;s and exists
        only at ERun&apos;s versions, so one version cannot name both.
      </div>
    </div>
  );
}

function RuntimeChartItem({
  label,
  detail,
  selected,
  onSelect,
}: {
  label: string;
  detail: string;
  selected: boolean;
  onSelect: () => void;
}): React.ReactElement {
  return (
    <CommandItem value={`${label} ${detail}`} onSelect={onSelect}>
      <Check className={cn('mr-2 size-4', selected ? 'opacity-100' : 'opacity-0')} />
      <span className="flex min-w-0 flex-col">
        <span className="truncate">{label}</span>
        <span className="truncate text-[12px] text-muted-foreground">{detail}</span>
      </span>
    </CommandItem>
  );
}
