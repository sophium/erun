import './styles.css';

import { createRoot } from 'react-dom/client';
import { Provider } from 'react-redux';

import { App } from './App';
import { store } from './app/store';

const root = document.querySelector<HTMLDivElement>('#root');
if (!root) {
  throw new Error('app root not found');
}

createRoot(root).render(
  <Provider store={store}>
    <App />
  </Provider>,
);
