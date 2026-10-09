import type { Handle } from 'remix/component';
import { Page } from '../../ui/layout.tsx';
import { Empty } from '../../ui/feedback.tsx';

// PLACEHOLDER during the Remix rebuild: replaced by the real screen.
export function SupportScreen(_handle: Handle) {
  return () => (
    <Page title="Support bundle">
      <Empty title="Support bundle">
        <p>This screen is being rebuilt.</p>
      </Empty>
    </Page>
  );
}
