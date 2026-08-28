import { cleanup, fireEvent, render, screen } from '@testing-library/react';
import { afterEach, describe, expect, it } from 'vitest';

import { BrandMark } from './BrandMark';

afterEach(() => {
  cleanup();
});

// The logo is decorative (the brand name is always rendered as text beside
// it), so it carries alt="" and has no accessible name to query by role.
function logo(): HTMLElement | null {
  return document.querySelector('img');
}

function requireLogo(): HTMLElement {
  const img = logo();
  if (img === null) {
    throw new Error('expected a logo <img> to be rendered');
  }
  return img;
}

describe('BrandMark', () => {
  it("renders the instance's own logo when platform discovery carries one", () => {
    render(<BrandMark brand="Acme" logoUrl="https://cdn.acme.example/logo.svg" />);

    expect(logo()).toHaveAttribute('src', 'https://cdn.acme.example/logo.svg');
    expect(screen.queryByText('A')).not.toBeInTheDocument();
  });

  it('falls back to the brand initial when no logo is configured', () => {
    render(<BrandMark brand="Acme" logoUrl={undefined} />);

    expect(logo()).toBeNull();
    expect(screen.getByText('A')).toBeInTheDocument();
  });

  it('treats an empty logoUrl the same as an unset one', () => {
    render(<BrandMark brand="Acme" logoUrl="" />);

    expect(logo()).toBeNull();
    expect(screen.getByText('A')).toBeInTheDocument();
  });

  it('degrades to the fallback mark when a configured logo fails to load', () => {
    // An operator's logo lives on their own host, so a moved asset or a typo
    // must not leave a broken-image icon on the front door with nothing on
    // the page to explain it.
    render(<BrandMark brand="Acme" logoUrl="https://cdn.acme.example/missing.svg" />);

    fireEvent.error(requireLogo());

    expect(logo()).toBeNull();
    expect(screen.getByText('A')).toBeInTheDocument();
  });

  it('falls back to the product initial when the instance sets no brand either', () => {
    render(<BrandMark brand={undefined} logoUrl={undefined} />);

    expect(screen.getByText('E')).toBeInTheDocument();
  });
});
