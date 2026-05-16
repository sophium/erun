import type { ERunUIController } from './ERunUIController';
import { selectAppState } from './appStateSelector';
import { useAppSelector } from './hooks';
import type { AppState } from './state';

// useControllerState used to wrap a hand-rolled subscribe/emit hook on the
// controller. The controller now mirrors its state into the Redux store from
// emit(), so this hook just reads the reassembled AppState shape via the
// memoized appState selector. The controller argument is accepted for source
// compatibility with existing call sites while the rest of the codebase
// migrates to useAppSelector directly.
export function useControllerState(_controller?: ERunUIController): AppState {
  return useAppSelector(selectAppState);
}
