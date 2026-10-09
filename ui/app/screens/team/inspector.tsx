import type { Handle } from 'remix/component';

// PLACEHOLDER during the Remix rebuild: replaced by the real panel.
export function AgentInspector(_handle: Handle<{ agentId: string | undefined }>) {
  return () => <p style={{ padding: '16px', color: 'var(--text-muted)' }}>This panel is being rebuilt.</p>;
}
