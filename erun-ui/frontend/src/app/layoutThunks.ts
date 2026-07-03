import type * as React from 'react';

import { WindowToggleMaximise } from '../../wailsjs/runtime/runtime';
import {
  setDebugOpen as applyDebugOpen,
  setFilesOpen as applyFilesOpen,
  startDebugResize as startDebugPanelResize,
  startFilesResize as startFilesPanelResize,
  startReviewResize as startReviewPanelResize,
  startSidebarResize as startSidebarPanelResize,
  toggleReview as toggleReviewPanel,
  toggleSidebar as toggleSidebarPanel,
} from './layoutActions';
import { loadReviewDiff } from './reviewThunks';
import type { AppThunk } from './store';
import { requireController } from './thunkExtra';

export const toggleSidebar = (): AppThunk => (dispatch, getState, extra) => {
  const controller = requireController(extra);
  toggleSidebarPanel(dispatch, getState, controller.layoutCallbacks());
};

export const toggleReview = (): AppThunk => (dispatch, getState, extra) => {
  const controller = requireController(extra);
  toggleReviewPanel(dispatch, getState, {
    ...controller.layoutCallbacks(),
    loadReviewDiff: () => {
      void dispatch(loadReviewDiff());
    },
  });
  if (!getState().layout.reviewOpen) {
    controller.stopReviewDiffRefresh();
  }
};

export const setFilesOpen =
  (open: boolean, persist = true): AppThunk =>
  (dispatch, getState, extra) => {
    const controller = requireController(extra);
    applyFilesOpen(dispatch, getState, open, persist, () => {
      controller.applyLayoutVars();
    });
  };

export const setDebugOpen =
  (open: boolean): AppThunk =>
  (dispatch, _getState, extra) => {
    const controller = requireController(extra);
    applyDebugOpen(dispatch, open, controller.flushTerminalResize);
  };

export const startSidebarResize =
  (event: React.MouseEvent<HTMLElement>): AppThunk =>
  (dispatch, getState, extra) => {
    const controller = requireController(extra);
    startSidebarPanelResize(dispatch, getState, event, () => {
      controller.applyLayoutVars();
    });
  };

export const startReviewResize =
  (event: React.MouseEvent<HTMLElement>): AppThunk =>
  (dispatch, getState, extra) => {
    const controller = requireController(extra);
    startReviewPanelResize(
      dispatch,
      getState,
      event,
      controller.terminalPane,
      controller.layoutCallbacks(),
    );
  };

export const startFilesResize =
  (event: React.MouseEvent<HTMLElement>): AppThunk =>
  (dispatch, getState, extra) => {
    const controller = requireController(extra);
    startFilesPanelResize(dispatch, getState, event, controller.reviewView, () => {
      controller.applyLayoutVars();
    });
  };

export const startDebugResize =
  (event: React.MouseEvent<HTMLElement>): AppThunk =>
  (dispatch, getState, extra) => {
    const controller = requireController(extra);
    startDebugPanelResize(
      dispatch,
      getState,
      event,
      controller.terminalPane,
      controller.layoutCallbacks(),
    );
  };

export const titlebarDoubleClick =
  (event: React.MouseEvent<HTMLElement>): AppThunk =>
  () => {
    const target = event.target;
    if (target instanceof HTMLElement && target.closest('button')) {
      return;
    }
    WindowToggleMaximise();
  };
