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

// layoutThunks own the user-facing layout commands (toggle panels, drag
// resizers, double-click the titlebar to maximize). They use
// requireController() because the resize handlers need live DOM rect refs
// (terminalPane, reviewView) that the controller exposes and because every
// resize must re-fit the xterm viewport.

export const toggleSidebar = (): AppThunk => (_dispatch, _getState, extra) => {
  const controller = requireController(extra);
  toggleSidebarPanel(controller.state, controller.layoutCallbacks());
};

export const toggleReview = (): AppThunk => (dispatch, _getState, extra) => {
  const controller = requireController(extra);
  toggleReviewPanel(controller.state, {
    ...controller.layoutCallbacks(),
    loadReviewDiff: () => { void dispatch(loadReviewDiff()); },
  });
  if (!controller.state.reviewOpen) {
    controller.stopReviewDiffRefresh();
  }
};

export const setFilesOpen = (open: boolean, persist = true): AppThunk =>
  (_dispatch, _getState, extra) => {
    const controller = requireController(extra);
    applyFilesOpen(controller.state, open, persist, () => controller.applyLayoutVars());
  };

export const setDebugOpen = (open: boolean): AppThunk =>
  (_dispatch, _getState, extra) => {
    const controller = requireController(extra);
    applyDebugOpen(controller.state, open, controller.queueTerminalResize);
  };

export const clearDebugOutput = (): AppThunk =>
  (_dispatch, _getState, extra) => {
    const controller = requireController(extra);
    controller.state.debugOutput = '';
    controller.sessions.clearSessionDebug(controller.state.sessionId);
  };

export const startSidebarResize = (event: React.MouseEvent<HTMLElement>): AppThunk =>
  (_dispatch, _getState, extra) => {
    const controller = requireController(extra);
    startSidebarPanelResize(controller.state, event, () => controller.applyLayoutVars());
  };

export const startReviewResize = (event: React.MouseEvent<HTMLElement>): AppThunk =>
  (_dispatch, _getState, extra) => {
    const controller = requireController(extra);
    startReviewPanelResize(controller.state, event, controller.terminalPane, controller.layoutCallbacks());
  };

export const startFilesResize = (event: React.MouseEvent<HTMLElement>): AppThunk =>
  (_dispatch, _getState, extra) => {
    const controller = requireController(extra);
    startFilesPanelResize(controller.state, event, controller.reviewView, () => controller.applyLayoutVars());
  };

export const startDebugResize = (event: React.MouseEvent<HTMLElement>): AppThunk =>
  (_dispatch, _getState, extra) => {
    const controller = requireController(extra);
    startDebugPanelResize(controller.state, event, controller.terminalPane, controller.layoutCallbacks());
  };

export const titlebarDoubleClick = (event: React.MouseEvent<HTMLElement>): AppThunk =>
  () => {
    const target = event.target;
    if (target instanceof HTMLElement && target.closest('button')) {
      return;
    }
    WindowToggleMaximise();
  };
