import './styles.css';

import { createRoot } from 'react-dom/client';

import { App } from './App';

const root = document.querySelector<HTMLDivElement>('#root');
if (!root) {
  throw new Error('app root not found');
}

createRoot(root).render(<App />);
