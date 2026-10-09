import type { Handle } from 'remix/component';

// PLACEHOLDER during the Remix rebuild: replaced by the real panel.
export function TerminalPanel(_handle: Handle<{ projectId: string | undefined; taskId: string | undefined }>) {
  return () => <p style={{ padding: '16px', color: 'var(--text-muted)' }}>This panel is being rebuilt.</p>;
}
