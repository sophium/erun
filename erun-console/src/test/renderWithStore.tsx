import { render, type RenderResult } from '@testing-library/react';
import type * as React from 'react';
import { Provider } from 'react-redux';

import { createAppStore } from '../app/store';

// Every panel now reads through the Redux store (RTK Query hooks), so a test
// needs a Provider in the tree. Each call builds a fresh store — reusing one
// across tests would let RTK Query's cache carry a prior test's mocked-fetch
// response into the next.
export function renderWithStore(element: React.ReactElement): RenderResult {
  return render(<Provider store={createAppStore()}>{element}</Provider>);
}
