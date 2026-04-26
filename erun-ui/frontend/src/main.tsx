import '@xterm/xterm/css/xterm.css';
import './styles/index.css';

import { createRoot } from 'react-dom/client';

import { App } from './App';

const shell = document.querySelector<HTMLDivElement>('#app');
if (!shell) {
  throw new Error('app root not found');
}

createRoot(shell).render(<App />);
