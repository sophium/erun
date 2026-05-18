import '@xterm/xterm/css/xterm.css';
import './styles/index.css';

import { createRoot } from 'react-dom/client';
import { Provider } from 'react-redux';

import { App } from './App';
import { startWailsEventsListening } from './app/middleware/wailsEventsMiddleware';
import { store } from './app/store';

store.dispatch(startWailsEventsListening());

const shell = document.querySelector<HTMLDivElement>('#app');
if (!shell) {
  throw new Error('app root not found');
}

createRoot(shell).render(
  <Provider store={store}>
    <App />
  </Provider>,
);
