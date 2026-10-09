import type { Handle } from 'remix/component';
import { Page } from '../../ui/layout.tsx';
import { Empty } from '../../ui/feedback.tsx';

// PLACEHOLDER during the Remix rebuild: replaced by the real screen.
export function RunScreen(_handle: Handle<{ taskId: string; runId?: string }>) {
  return () => (
    <Page title="Workflow">
      <Empty title="Workflow">
        <p>This screen is being rebuilt.</p>
      </Empty>
    </Page>
  );
}
