import {
  Button,
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from 'erun-kit';
import { ArrowRight, RotateCw } from 'lucide-react';
import * as React from 'react';

import { closeGatewayRedeployDialog, confirmGatewayRedeploy } from '@/app/gatewayRedeployThunks';
import { useAppDispatch, useAppSelector } from '@/app/hooks';
import type { UIGatewayRouting } from '@/uiOpenRouterTypes';

// GatewayRedeployDialog says what a gateway switch in ERun settings has not
// done yet: the gateway is rendered into each runtime at deploy time, so the
// listed pods keep routing the old way until they are redeployed. Redeploying
// restarts their pods, so it is offered, never done on the operator's behalf.
export function GatewayRedeployDialog(): React.ReactElement {
  const dispatch = useAppDispatch();
  const { open, items } = useAppSelector((state) => state.gatewayRedeploy);
  return (
    <Dialog
      open={open}
      onOpenChange={(next) => {
        if (!next) {
          dispatch(closeGatewayRedeployDialog());
        }
      }}
    >
      <DialogContent className="sm:max-w-2xl" data-testid="gateway-redeploy-dialog">
        <DialogHeader>
          <DialogTitle>Apply the gateway change</DialogTitle>
          <DialogDescription>
            These environments are still running with the gateway routing they were deployed with.
            The change reaches them on their next redeploy, which restarts their pods.
          </DialogDescription>
        </DialogHeader>
        <div className="max-h-80 overflow-y-auto">
          <table className="w-full text-sm" aria-label="Environments to redeploy">
            <thead>
              <tr className="text-left text-[11px] tracking-wide text-muted-foreground uppercase">
                <th scope="col" className="pr-4 pb-2 font-medium">
                  Environment
                </th>
                <th scope="col" className="pb-2 font-medium">
                  Claude Code routing
                </th>
              </tr>
            </thead>
            <tbody>
              {items.map((item) => (
                <GatewayRedeployRow key={`${item.tenant}/${item.environment}`} item={item} />
              ))}
            </tbody>
          </table>
        </div>
        <DialogFooter>
          <Button
            type="button"
            variant="outline"
            onClick={() => {
              dispatch(closeGatewayRedeployDialog());
            }}
          >
            Later
          </Button>
          <Button
            type="button"
            onClick={() => {
              void dispatch(confirmGatewayRedeploy());
            }}
          >
            <RotateCw aria-hidden="true" />
            {items.length === 1 ? 'Redeploy' : `Redeploy ${String(items.length)}`}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

function GatewayRedeployRow({ item }: { item: UIGatewayRouting }): React.ReactElement {
  return (
    <tr className="border-t border-border/60 align-top">
      <td className="py-2.5 pr-4">
        <div className="leading-tight font-medium break-words text-foreground">
          {item.environment}
        </div>
        <div className="text-[11px] break-words text-muted-foreground">{item.tenant}</div>
      </td>
      <td className="py-2.5">
        <span className="inline-flex flex-wrap items-center gap-1.5 [overflow-wrap:anywhere]">
          <span className="text-muted-foreground">{routingLabel(item.deployed)}</span>
          <ArrowRight className="size-3.5 flex-none" aria-label="will become" />
          <span className="text-foreground">{routingLabel(item.wanted)}</span>
        </span>
      </td>
    </tr>
  );
}

function routingLabel(endpoint: string): string {
  return endpoint === '' ? 'Own Claude sign-in' : `Gateway ${endpoint}`;
}
