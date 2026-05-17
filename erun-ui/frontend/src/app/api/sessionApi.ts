import type { PastedImageResult, StartSessionResult, UISelection } from '@/types';

import {
  CloseSession,
  OpenIDE,
  ReconnectMCP,
  ResizeSession,
  SavePastedImage,
  SendSessionInput,
  StartAISession,
  StartCloudInitAWSSession,
  StartDeploySession,
  StartDoctorSession,
  StartForceDeploySession,
  StartInitSession,
  StartLocalSession,
  StartSession,
  StartSSHDInitSession,
} from '../../../wailsjs/go/main/App';
import { wailsApi } from './wailsApi';
import { wailsQueryFn } from './wailsBaseQuery';

export interface PastedImagePayload {
  data: string;
  mimeType: string;
  name: string;
}

interface StartSessionArgs {
  selection: UISelection;
  slot: number;
  cols: number;
  rows: number;
}

interface StartUnslottedArgs {
  selection: UISelection;
  cols: number;
  rows: number;
}

interface StartCloudInitArgs {
  cols: number;
  rows: number;
}

interface ResizeArgs {
  sessionId: number;
  cols: number;
  rows: number;
}

interface InputArgs {
  sessionId: number;
  data: string;
}

interface OpenIDEArgs {
  selection: UISelection;
  ide: string;
}

interface SavePastedImageArgs {
  sessionId: number;
  payload: PastedImagePayload;
}

export const sessionApi = wailsApi.injectEndpoints({
  endpoints: (builder) => ({
    startSession: builder.mutation<StartSessionResult, StartSessionArgs>({
      queryFn: wailsQueryFn<StartSessionArgs, StartSessionResult>(
        ({ selection, slot, cols, rows }) =>
          StartSession(selection, slot, cols, rows) as Promise<StartSessionResult>,
      ),
    }),
    startLocalSession: builder.mutation<StartSessionResult, StartSessionArgs>({
      queryFn: wailsQueryFn<StartSessionArgs, StartSessionResult>(
        ({ selection, slot, cols, rows }) =>
          StartLocalSession(selection, slot, cols, rows) as Promise<StartSessionResult>,
      ),
    }),
    startAISession: builder.mutation<StartSessionResult, StartSessionArgs>({
      queryFn: wailsQueryFn<StartSessionArgs, StartSessionResult>(
        ({ selection, slot, cols, rows }) =>
          StartAISession(selection, slot, cols, rows) as Promise<StartSessionResult>,
      ),
    }),
    startInitSession: builder.mutation<StartSessionResult, StartUnslottedArgs>({
      queryFn: wailsQueryFn<StartUnslottedArgs, StartSessionResult>(
        ({ selection, cols, rows }) =>
          StartInitSession(selection, cols, rows) as Promise<StartSessionResult>,
      ),
    }),
    startDeploySession: builder.mutation<StartSessionResult, StartUnslottedArgs>({
      queryFn: wailsQueryFn<StartUnslottedArgs, StartSessionResult>(
        ({ selection, cols, rows }) =>
          StartDeploySession(selection, cols, rows) as Promise<StartSessionResult>,
      ),
    }),
    startForceDeploySession: builder.mutation<StartSessionResult, StartUnslottedArgs>({
      queryFn: wailsQueryFn<StartUnslottedArgs, StartSessionResult>(
        ({ selection, cols, rows }) =>
          StartForceDeploySession(selection, cols, rows) as Promise<StartSessionResult>,
      ),
    }),
    startDoctorSession: builder.mutation<StartSessionResult, StartUnslottedArgs>({
      queryFn: wailsQueryFn<StartUnslottedArgs, StartSessionResult>(
        ({ selection, cols, rows }) =>
          StartDoctorSession(selection, cols, rows) as Promise<StartSessionResult>,
      ),
    }),
    startSSHDInitSession: builder.mutation<StartSessionResult, StartUnslottedArgs>({
      queryFn: wailsQueryFn<StartUnslottedArgs, StartSessionResult>(
        ({ selection, cols, rows }) =>
          StartSSHDInitSession(selection, cols, rows) as Promise<StartSessionResult>,
      ),
    }),
    startCloudInitAWSSession: builder.mutation<StartSessionResult, StartCloudInitArgs>({
      queryFn: wailsQueryFn<StartCloudInitArgs, StartSessionResult>(
        ({ cols, rows }) => StartCloudInitAWSSession(cols, rows) as Promise<StartSessionResult>,
      ),
    }),
    resizeSession: builder.mutation<void, ResizeArgs>({
      queryFn: wailsQueryFn<ResizeArgs, void>(({ sessionId, cols, rows }) =>
        ResizeSession(sessionId, cols, rows),
      ),
    }),
    sendSessionInput: builder.mutation<void, InputArgs>({
      queryFn: wailsQueryFn<InputArgs, void>(({ sessionId, data }) =>
        SendSessionInput(sessionId, data),
      ),
    }),
    closeSession: builder.mutation<void, number>({
      queryFn: wailsQueryFn<number, void>((sessionId) => CloseSession(sessionId)),
    }),
    openIDE: builder.mutation<void, OpenIDEArgs>({
      queryFn: wailsQueryFn<OpenIDEArgs, void>(({ selection, ide }) => OpenIDE(selection, ide)),
    }),
    reconnectMCP: builder.mutation<void, UISelection>({
      queryFn: wailsQueryFn<UISelection, void>((selection) => ReconnectMCP(selection)),
    }),
    savePastedImage: builder.mutation<PastedImageResult, SavePastedImageArgs>({
      queryFn: wailsQueryFn<SavePastedImageArgs, PastedImageResult>(({ sessionId, payload }) =>
        SavePastedImage(sessionId, payload),
      ),
    }),
  }),
});

export const {
  useStartSessionMutation,
  useStartLocalSessionMutation,
  useStartAISessionMutation,
  useStartInitSessionMutation,
  useStartDeploySessionMutation,
  useStartForceDeploySessionMutation,
  useStartDoctorSessionMutation,
  useStartSSHDInitSessionMutation,
  useStartCloudInitAWSSessionMutation,
  useResizeSessionMutation,
  useSendSessionInputMutation,
  useCloseSessionMutation,
  useOpenIDEMutation,
  useReconnectMCPMutation,
  useSavePastedImageMutation,
} = sessionApi;
