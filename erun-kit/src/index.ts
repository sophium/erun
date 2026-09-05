// The kit's public surface. Everything exported here is transport-agnostic:
// no Wails bindings, no `fetch`, no concrete base query, no store. Widgets
// take props and emit callbacks; the shared platform-config model and RTK
// Query endpoint definitions take a `BaseQuery` as a parameter rather than
// embedding one, so nothing here reaches into an app's transport.

export * from './api/platformConfigEndpoints';
export * from './components/EditableComboField';
export * from './components/EditableComboField.helpers';
export * from './components/EmptyState';
export * from './components/ErrorBoundary';
export * from './components/FieldLabel';
export * from './components/FileIcon';
export * from './components/IconTooltip';
export * from './components/ResizeHandle';
export * from './components/SelectField';
export * from './components/StatusBadge';
export * from './components/StatusBadge.helpers';
export * from './components/ui/button';
export * from './components/ui/card';
export * from './components/ui/checkbox';
export * from './components/ui/command';
export * from './components/ui/dialog';
export * from './components/ui/input';
export * from './components/ui/label';
export * from './components/ui/popover';
export * from './components/ui/select';
export * from './components/ui/table';
export * from './components/ui/tabs';
export * from './components/ui/textarea';
export * from './components/ui/tooltip';
export { cn, noop } from './lib/utils';
export * from './models/platformConfig';
