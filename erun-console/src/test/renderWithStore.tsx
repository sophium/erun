import { render, type RenderResult } from '@testing-library/react';
import type * as React from 'react';
import { Provider } from 'react-redux';

import { createAppStore } from '../app/store';

// A fresh store per call keeps RTK Query's cache from leaking across tests.
export function renderWithStore(element: React.ReactElement): RenderResult {
  return render(<Provider store={createAppStore()}>{element}</Provider>);
}
