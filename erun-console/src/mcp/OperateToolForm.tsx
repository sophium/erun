import { Button, Checkbox, FieldLabel, Input, Label, SelectField } from 'erun-kit';
import * as React from 'react';

import { useMcpToolCallController } from './controller';
import { hostnameFieldLabel } from './hostnameFieldLabel';
import { DriveToolResult } from './mcpFormShared';
import { buildOperateArgs, OPERATE_TOOL_OPTIONS, operateFormValid } from './operateToolArgs';

// OperateToolForm drives the four tools an erun:operate-scoped token grants
// directly (mcpCapabilities.go's mcpOperateTools), over the same live
// JSON-RPC edge DriveToolForm already speaks -- so a console session holding
// only erun:operate can do real operate-shaped work without leaving the
// browser, rather than just minting the token and handing it to another MCP
// client (DriveToolForm only ever calls the read-only `version` tool, which
// erun:operate deliberately cannot reach).

interface OperateFieldsProps {
  tool: string;
  version: string;
  onVersionChange: (value: string) => void;
  contextName: string;
  onContextNameChange: (value: string) => void;
  force: boolean;
  onForceChange: (value: boolean) => void;
  cpu: string;
  onCpuChange: (value: string) => void;
  memory: string;
  onMemoryChange: (value: string) => void;
  dindCpu: string;
  onDindCpuChange: (value: string) => void;
  dindMemory: string;
  onDindMemoryChange: (value: string) => void;
  applyRecommendation: boolean;
  onApplyRecommendationChange: (value: boolean) => void;
}

function DeployFields({
  version,
  onChange,
}: {
  version: string;
  onChange: (value: string) => void;
}): React.ReactElement {
  return (
    <div className="grid gap-2">
      <FieldLabel htmlFor="operate-version" required>
        Version
      </FieldLabel>
      <Input
        id="operate-version"
        value={version}
        onChange={(e) => {
          onChange(e.target.value);
        }}
        placeholder="1.2.3"
        required
      />
    </div>
  );
}

function ContextFields({
  tool,
  name,
  force,
  onNameChange,
  onForceChange,
}: {
  tool: string;
  name: string;
  force: boolean;
  onNameChange: (value: string) => void;
  onForceChange: (value: boolean) => void;
}): React.ReactElement {
  return (
    <>
      <div className="grid gap-2">
        <FieldLabel htmlFor="operate-context-name" required>
          Cloud context name
        </FieldLabel>
        <Input
          id="operate-context-name"
          value={name}
          onChange={(e) => {
            onNameChange(e.target.value);
          }}
          required
        />
      </div>
      {tool === 'context_start' && (
        <div className="flex items-center gap-2">
          <Checkbox
            id="operate-context-force"
            checked={force}
            onCheckedChange={(value) => {
              onForceChange(value === true);
            }}
          />
          <Label htmlFor="operate-context-force">Override the working-hours start gate</Label>
        </div>
      )}
    </>
  );
}

function ResizeFields({
  cpu,
  memory,
  dindCpu,
  dindMemory,
  applyRecommendation,
  onCpuChange,
  onMemoryChange,
  onDindCpuChange,
  onDindMemoryChange,
  onApplyRecommendationChange,
}: {
  cpu: string;
  memory: string;
  dindCpu: string;
  dindMemory: string;
  applyRecommendation: boolean;
  onCpuChange: (value: string) => void;
  onMemoryChange: (value: string) => void;
  onDindCpuChange: (value: string) => void;
  onDindMemoryChange: (value: string) => void;
  onApplyRecommendationChange: (value: boolean) => void;
}): React.ReactElement {
  return (
    <>
      <div className="grid grid-cols-2 gap-2">
        <div className="grid gap-2">
          <FieldLabel htmlFor="operate-cpu">Runtime pod CPU</FieldLabel>
          <Input
            id="operate-cpu"
            value={cpu}
            onChange={(e) => {
              onCpuChange(e.target.value);
            }}
            placeholder="6"
          />
        </div>
        <div className="grid gap-2">
          <FieldLabel htmlFor="operate-memory">Runtime pod memory</FieldLabel>
          <Input
            id="operate-memory"
            value={memory}
            onChange={(e) => {
              onMemoryChange(e.target.value);
            }}
            placeholder="12Gi"
          />
        </div>
        <div className="grid gap-2">
          <FieldLabel htmlFor="operate-dind-cpu">erun-dind CPU</FieldLabel>
          <Input
            id="operate-dind-cpu"
            value={dindCpu}
            onChange={(e) => {
              onDindCpuChange(e.target.value);
            }}
            placeholder="6"
          />
        </div>
        <div className="grid gap-2">
          <FieldLabel htmlFor="operate-dind-memory">erun-dind memory</FieldLabel>
          <Input
            id="operate-dind-memory"
            value={dindMemory}
            onChange={(e) => {
              onDindMemoryChange(e.target.value);
            }}
            placeholder="16Gi"
          />
        </div>
      </div>
      <div className="flex items-center gap-2">
        <Checkbox
          id="operate-apply-recommendation"
          checked={applyRecommendation}
          onCheckedChange={(value) => {
            onApplyRecommendationChange(value === true);
          }}
        />
        <Label htmlFor="operate-apply-recommendation">
          Size from this environment&apos;s own standing recommendation instead
        </Label>
      </div>
    </>
  );
}

// OperateToolFields dispatches to the one tool-shaped set of fields that
// matches the current selection -- kept out of OperateToolForm itself to
// stay under this module's complexity/line budget.
function OperateToolFields(props: OperateFieldsProps): React.ReactElement | null {
  switch (props.tool) {
    case 'deploy':
      return <DeployFields version={props.version} onChange={props.onVersionChange} />;
    case 'context_start':
    case 'context_stop':
      return (
        <ContextFields
          tool={props.tool}
          name={props.contextName}
          force={props.force}
          onNameChange={props.onContextNameChange}
          onForceChange={props.onForceChange}
        />
      );
    case 'resize':
      return (
        <ResizeFields
          cpu={props.cpu}
          memory={props.memory}
          dindCpu={props.dindCpu}
          dindMemory={props.dindMemory}
          applyRecommendation={props.applyRecommendation}
          onCpuChange={props.onCpuChange}
          onMemoryChange={props.onMemoryChange}
          onDindCpuChange={props.onDindCpuChange}
          onDindMemoryChange={props.onDindMemoryChange}
          onApplyRecommendationChange={props.onApplyRecommendationChange}
        />
      );
    default:
      return null;
  }
}

interface OperateFormState extends OperateFieldsProps {
  setTool: (value: string) => void;
  preview: boolean;
  setPreview: (value: boolean) => void;
}

// useOperateFormState owns every field the four operate tools can take
// between them, split out of OperateToolForm to keep that component under
// this module's line/complexity budget.
function useOperateFormState(): OperateFormState {
  const [tool, setTool] = React.useState(OPERATE_TOOL_OPTIONS[0]?.value ?? 'deploy');
  const [version, setVersion] = React.useState('');
  const [contextName, setContextName] = React.useState('');
  const [force, setForce] = React.useState(false);
  const [cpu, setCpu] = React.useState('');
  const [memory, setMemory] = React.useState('');
  const [dindCpu, setDindCpu] = React.useState('');
  const [dindMemory, setDindMemory] = React.useState('');
  const [applyRecommendation, setApplyRecommendation] = React.useState(false);
  const [preview, setPreview] = React.useState(true);
  return {
    tool,
    setTool,
    version,
    onVersionChange: setVersion,
    contextName,
    onContextNameChange: setContextName,
    force,
    onForceChange: setForce,
    cpu,
    onCpuChange: setCpu,
    memory,
    onMemoryChange: setMemory,
    dindCpu,
    onDindCpuChange: setDindCpu,
    dindMemory,
    onDindMemoryChange: setDindMemory,
    applyRecommendation,
    onApplyRecommendationChange: setApplyRecommendation,
    preview,
    setPreview,
  };
}

export function OperateToolForm({
  mcpToken,
  exposedHostname,
}: {
  mcpToken: string;
  exposedHostname?: string;
}): React.ReactElement {
  const [hostname, setHostname] = React.useState(exposedHostname ?? '');
  const fields = useOperateFormState();
  const { state, callTool } = useMcpToolCallController();
  const calling = state.status === 'loading';
  const valid = operateFormValid(fields);

  const submit = (event: React.SyntheticEvent): void => {
    event.preventDefault();
    if (hostname.trim() === '' || !valid) {
      return;
    }
    callTool(hostname, mcpToken, fields.tool, buildOperateArgs(fields));
  };

  return (
    <form
      className="grid gap-2 border-t border-border pt-3"
      onSubmit={submit}
      aria-labelledby="mcp-operate-heading"
    >
      <h3 id="mcp-operate-heading" className="text-sm font-semibold text-foreground">
        Drive an operate tool
      </h3>
      <p className="text-sm text-muted-foreground">
        This token is scoped to <code>erun:operate</code>: it can deploy an already-published
        version, start or stop the cloud context, and resize the runtime pod. It cannot call
        read/observe tools or anything reserved for <code>erun:admin</code>.
      </p>
      <FieldLabel htmlFor="operate-hostname" required>
        {hostnameFieldLabel('MCP hostname', exposedHostname)}
      </FieldLabel>
      <Input
        id="operate-hostname"
        value={hostname}
        onChange={(e) => {
          setHostname(e.target.value);
        }}
        placeholder="mcp.acme-prod.services.example.com"
        required
      />
      <SelectField
        id="operate-tool"
        label="Tool"
        value={fields.tool}
        options={OPERATE_TOOL_OPTIONS}
        onChange={fields.setTool}
      />
      <OperateToolFields {...fields} />
      <div className="flex items-center gap-2">
        <Checkbox
          id="operate-preview"
          checked={fields.preview}
          onCheckedChange={(value) => {
            fields.setPreview(value === true);
          }}
        />
        <Label htmlFor="operate-preview">
          Preview only — resolve and trace the plan without changing anything
        </Label>
      </div>
      <Button
        type="submit"
        disabled={calling || hostname.trim() === '' || !valid}
        className="justify-self-start"
      >
        {calling ? 'Calling…' : 'Call the tool'}
      </Button>
      <DriveToolResult state={state} />
    </form>
  );
}
