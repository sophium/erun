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
import { type NoValue, wailsQueryFn } from './wailsBaseQuery';

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
        ({ selection, slot, cols, rows }) => StartSession(selection, slot, cols, rows),
      ),
    }),
    startLocalSession: builder.mutation<StartSessionResult, StartSessionArgs>({
      queryFn: wailsQueryFn<StartSessionArgs, StartSessionResult>(
        ({ selection, slot, cols, rows }) => StartLocalSession(selection, slot, cols, rows),
      ),
    }),
    startAISession: builder.mutation<StartSessionResult, StartSessionArgs>({
      queryFn: wailsQueryFn<StartSessionArgs, StartSessionResult>(
        ({ selection, slot, cols, rows }) => StartAISession(selection, slot, cols, rows),
      ),
    }),
    startInitSession: builder.mutation<StartSessionResult, StartUnslottedArgs>({
      queryFn: wailsQueryFn<StartUnslottedArgs, StartSessionResult>(({ selection, cols, rows }) =>
        StartInitSession(selection, cols, rows),
      ),
    }),
    startDeploySession: builder.mutation<StartSessionResult, StartUnslottedArgs>({
      queryFn: wailsQueryFn<StartUnslottedArgs, StartSessionResult>(({ selection, cols, rows }) =>
        StartDeploySession(selection, cols, rows),
      ),
    }),
    startForceDeploySession: builder.mutation<StartSessionResult, StartUnslottedArgs>({
      queryFn: wailsQueryFn<StartUnslottedArgs, StartSessionResult>(({ selection, cols, rows }) =>
        StartForceDeploySession(selection, cols, rows),
      ),
    }),
    startDoctorSession: builder.mutation<StartSessionResult, StartUnslottedArgs>({
      queryFn: wailsQueryFn<StartUnslottedArgs, StartSessionResult>(({ selection, cols, rows }) =>
        StartDoctorSession(selection, cols, rows),
      ),
    }),
    startSSHDInitSession: builder.mutation<StartSessionResult, StartUnslottedArgs>({
      queryFn: wailsQueryFn<StartUnslottedArgs, StartSessionResult>(({ selection, cols, rows }) =>
        StartSSHDInitSession(selection, cols, rows),
      ),
    }),
    startCloudInitAWSSession: builder.mutation<StartSessionResult, StartCloudInitArgs>({
      queryFn: wailsQueryFn<StartCloudInitArgs, StartSessionResult>(({ cols, rows }) =>
        StartCloudInitAWSSession(cols, rows),
      ),
    }),
    resizeSession: builder.mutation<NoValue, ResizeArgs>({
      queryFn: wailsQueryFn<ResizeArgs, NoValue>(({ sessionId, cols, rows }) =>
        ResizeSession(sessionId, cols, rows),
      ),
    }),
    sendSessionInput: builder.mutation<NoValue, InputArgs>({
      queryFn: wailsQueryFn<InputArgs, NoValue>(({ sessionId, data }) =>
        SendSessionInput(sessionId, data),
      ),
    }),
    closeSession: builder.mutation<NoValue, number>({
      queryFn: wailsQueryFn<number, NoValue>((sessionId) => CloseSession(sessionId)),
    }),
    openIDE: builder.mutation<NoValue, OpenIDEArgs>({
      queryFn: wailsQueryFn<OpenIDEArgs, NoValue>(({ selection, ide }) => OpenIDE(selection, ide)),
    }),
    reconnectMCP: builder.mutation<NoValue, UISelection>({
      queryFn: wailsQueryFn<UISelection, NoValue>((selection) => ReconnectMCP(selection)),
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
