import type { Handle } from 'remix/component';
import { Page } from '../../ui/layout.tsx';
import { Empty } from '../../ui/feedback.tsx';

// PLACEHOLDER during the Remix rebuild: replaced by the real screen.
export function McpScreen(_handle: Handle) {
  return () => (
    <Page title="MCP servers">
      <Empty title="MCP servers">
        <p>This screen is being rebuilt.</p>
      </Empty>
    </Page>
  );
}
