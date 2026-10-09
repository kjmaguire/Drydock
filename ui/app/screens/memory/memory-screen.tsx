import type { Handle } from 'remix/component';
import { Page } from '../../ui/layout.tsx';
import { Empty } from '../../ui/feedback.tsx';

// PLACEHOLDER during the Remix rebuild: replaced by the real screen.
export function MemoryScreen(_handle: Handle) {
  return () => (
    <Page title="Memory">
      <Empty title="Memory">
        <p>This screen is being rebuilt.</p>
      </Empty>
    </Page>
  );
}
