import type { Handle } from 'remix/component';
import { Page } from '../../ui/layout.tsx';
import { Empty } from '../../ui/feedback.tsx';

// PLACEHOLDER during the Remix rebuild: replaced by the real screen.
export function DataScreen(_handle: Handle) {
  return () => (
    <Page title="Export and import">
      <Empty title="Export and import">
        <p>This screen is being rebuilt.</p>
      </Empty>
    </Page>
  );
}
