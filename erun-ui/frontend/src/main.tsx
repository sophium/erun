import '@xterm/xterm/css/xterm.css';
import './styles/index.css';

import { createRoot } from 'react-dom/client';
import { Provider } from 'react-redux';

import { App } from './App';
import { store } from './app/store';
import { applyTheme, initialTheme } from './app/theme';
import { attachWailsEventForwarders } from './app/wailsEventForwarders';

attachWailsEventForwarders(store.dispatch);

// Applied synchronously before the first render, not from an effect, so a
// dark-preferring operator never sees a light flash between launch and mount.
applyTheme(initialTheme());

const shell = document.querySelector<HTMLDivElement>('#app');
if (!shell) {
  throw new Error('app root not found');
}

createRoot(shell).render(
  <Provider store={store}>
    <App />
  </Provider>,
);
