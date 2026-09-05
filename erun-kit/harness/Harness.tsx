import * as React from 'react';

import type { StatusBadgeTone } from '../src';
import {
  Button,
  Checkbox,
  cn,
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
  DialogTrigger,
  EditableComboField,
  EmptyState,
  ErrorBoundary,
  FieldLabel,
  FileIcon,
  IconTooltip,
  Input,
  Popover,
  PopoverContent,
  PopoverTrigger,
  ResizeHandle,
  SelectField,
  StatusBadge,
  Tabs,
  TabsContent,
  TabsList,
  TabsTrigger,
} from '../src';

function Section({ title, children }: { title: string; children: React.ReactNode }) {
  return (
    <section className="grid gap-3 border-b border-border pb-8">
      <h2 className="text-sm font-semibold tracking-wide text-muted-foreground uppercase">
        {title}
      </h2>
      <div className="flex flex-wrap items-center gap-3">{children}</div>
    </section>
  );
}

const TONES: StatusBadgeTone[] = ['success', 'warning', 'destructive', 'in-progress', 'muted'];
const EXTENSIONS = ['ts', 'go', 'md', 'yaml', 'rb', 'unknown'];

function ThrowingChild(): React.ReactElement {
  throw new Error('Simulated render failure for the harness');
}

function ButtonsSection() {
  const variants = ['default', 'destructive', 'outline', 'secondary', 'ghost', 'link'] as const;
  return (
    <Section title="Button — every variant, default and disabled">
      {variants.map((variant) => (
        <Button key={variant} variant={variant}>
          {variant}
        </Button>
      ))}
      {variants.map((variant) => (
        <Button key={`${variant}-disabled`} variant={variant} disabled>
          {variant}
        </Button>
      ))}
    </Section>
  );
}

function InputsSection() {
  return (
    <Section title="Input, Checkbox — default, disabled, invalid">
      <Input placeholder="default" />
      <Input placeholder="disabled" disabled />
      <Input placeholder="invalid" aria-invalid />
      <Checkbox />
      <Checkbox defaultChecked />
      <Checkbox disabled />
    </Section>
  );
}

function OverlaysSection() {
  return (
    <Section title="Dialog, Popover, Tooltip">
      <Dialog>
        <DialogTrigger asChild>
          <Button variant="outline">Open dialog</Button>
        </DialogTrigger>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>Example dialog</DialogTitle>
            <DialogDescription>Rendered from the shared kit.</DialogDescription>
          </DialogHeader>
        </DialogContent>
      </Dialog>
      <Popover>
        <PopoverTrigger asChild>
          <Button variant="outline">Open popover</Button>
        </PopoverTrigger>
        <PopoverContent>Popover content</PopoverContent>
      </Popover>
      <IconTooltip label="Tooltip text">
        <Button variant="outline">Hover me</Button>
      </IconTooltip>
    </Section>
  );
}

function TabsSection() {
  return (
    <Section title="Tabs">
      <Tabs defaultValue="one" className="w-64">
        <TabsList>
          <TabsTrigger value="one">One</TabsTrigger>
          <TabsTrigger value="two">Two</TabsTrigger>
        </TabsList>
        <TabsContent value="one">First tab content.</TabsContent>
        <TabsContent value="two">Second tab content.</TabsContent>
      </Tabs>
    </Section>
  );
}

function StatusBadgeSection() {
  return (
    <Section title="StatusBadge — every tone">
      {TONES.map((tone) => (
        <StatusBadge key={tone} tone={tone} label={tone} />
      ))}
    </Section>
  );
}

function EmptyStateSection() {
  return (
    <Section title="EmptyState — with and without action">
      <EmptyState heading="Nothing here yet" body="Empty, no action" className="w-72" />
      <EmptyState
        heading="Nothing here yet"
        body="Empty, with action"
        action={<Button variant="outline">Add one</Button>}
        className="w-72"
      />
    </Section>
  );
}

function FieldsSection() {
  const [selectValue, setSelectValue] = React.useState('');
  const [comboValue, setComboValue] = React.useState('');
  return (
    <Section title="FieldLabel, SelectField, EditableComboField">
      <FieldLabel htmlFor="req">Required field</FieldLabel>
      <SelectField
        id="select-demo"
        label="Select field"
        value={selectValue}
        onChange={setSelectValue}
        options={[
          { value: 'a', label: 'Option A' },
          { value: 'b', label: 'Option B' },
        ]}
      />
      <SelectField
        id="select-empty"
        label="Empty select"
        value=""
        onChange={() => undefined}
        options={[]}
      />
      <EditableComboField
        id="combo-demo"
        label="Editable combo"
        value={comboValue}
        suggestions={['alpha', 'beta', 'gamma']}
        onValueChange={setComboValue}
      />
    </Section>
  );
}

function MiscSection() {
  return (
    <Section title="FileIcon, ResizeHandle, ErrorBoundary">
      <div className="flex gap-2">
        {EXTENSIONS.map((ext) => (
          <FileIcon key={ext} filePath={`example.${ext}`} />
        ))}
      </div>
      <div className="relative h-10 w-24 border border-border">
        <ResizeHandle
          orientation="vertical"
          label="Resize"
          className="absolute inset-y-0 right-0 w-1 cursor-col-resize bg-border"
          onMouseDown={() => undefined}
          value={{ now: 24, min: 0, max: 96 }}
          onStep={() => undefined}
        />
      </div>
      <ErrorBoundaryDemo />
    </Section>
  );
}

function ErrorBoundaryDemo() {
  const [shouldThrow, setShouldThrow] = React.useState(false);
  return (
    <div className="grid gap-2">
      <Button
        variant="outline"
        onClick={() => {
          setShouldThrow(true);
        }}
      >
        Trigger error boundary
      </Button>
      <div className="h-40 w-64 border border-border">
        <ErrorBoundary>{shouldThrow ? <ThrowingChild /> : <p>All good.</p>}</ErrorBoundary>
      </div>
    </div>
  );
}

export function Harness(): React.ReactElement {
  const [dark, setDark] = React.useState(false);
  return (
    <div className={cn(dark && 'dark')}>
      <div className="min-h-screen bg-background p-8 text-foreground">
        <div className="mb-8 flex items-center justify-between">
          <h1 className="text-lg font-semibold">erun-kit harness</h1>
          <Button
            variant="outline"
            onClick={() => {
              setDark((v) => !v);
            }}
          >
            Toggle {dark ? 'light' : 'dark'}
          </Button>
        </div>
        <div className="grid gap-8">
          <ButtonsSection />
          <InputsSection />
          <OverlaysSection />
          <TabsSection />
          <StatusBadgeSection />
          <EmptyStateSection />
          <FieldsSection />
          <MiscSection />
        </div>
      </div>
    </div>
  );
}
