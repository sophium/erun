import '../src/styles/theme.css';

import { createRoot } from 'react-dom/client';

import { Harness } from './Harness';

const root = document.querySelector<HTMLDivElement>('#app');
if (!root) {
  throw new Error('harness root not found');
}

createRoot(root).render(<Harness />);
