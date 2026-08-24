import { AlertTriangle } from 'lucide-react';
import * as React from 'react';

import { Button } from './ui/button';

interface ErrorBoundaryProps {
  children: React.ReactNode;
}

interface ErrorBoundaryState {
  error: Error | null;
}

// ErrorBoundary shows a recoverable surface instead of the blank white screen a
// bare React tree leaves when a render throws and unmounts the whole root. Must
// stay a class: React only runs error-boundary lifecycle on class components.
// Scoped to its children so the titlebar chrome stays interactive while the user
// recovers.
export class ErrorBoundary extends React.Component<ErrorBoundaryProps, ErrorBoundaryState> {
  constructor(props: ErrorBoundaryProps) {
    super(props);
    this.state = { error: null };
  }

  static getDerivedStateFromError(error: Error): ErrorBoundaryState {
    return { error };
  }

  override componentDidCatch(error: Error, info: React.ErrorInfo): void {
    console.error('Unhandled error in app content:', error, info.componentStack);
  }

  private readonly handleRetry = (): void => {
    this.setState({ error: null });
  };

  private readonly handleReload = (): void => {
    window.location.reload();
  };

  override render(): React.ReactNode {
    const { error } = this.state;
    if (!error) {
      return this.props.children;
    }
    return (
      <div
        role="alert"
        className="flex h-full w-full flex-col items-center justify-center gap-4 bg-background p-8 text-center text-foreground"
      >
        <AlertTriangle className="size-10 text-destructive" aria-hidden="true" />
        <div className="space-y-1">
          <p className="text-base font-medium">Something went wrong</p>
          <p className="max-w-md text-sm text-muted-foreground">
            The app hit an unexpected error and couldn’t finish rendering. Try again to recover, or
            reload the app if the problem persists.
          </p>
        </div>
        {error.message ? (
          <pre className="max-w-md overflow-x-auto rounded-md bg-muted px-3 py-2 text-left text-xs text-muted-foreground">
            {error.message}
          </pre>
        ) : null}
        <div className="flex items-center gap-2">
          <Button variant="default" onClick={this.handleRetry}>
            Try again
          </Button>
          <Button variant="outline" onClick={this.handleReload}>
            Reload app
          </Button>
        </div>
      </div>
    );
  }
}
