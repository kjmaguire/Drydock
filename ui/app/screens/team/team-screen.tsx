import type { Handle } from 'remix/component';
import { Page } from '../../ui/layout.tsx';
import { Empty } from '../../ui/feedback.tsx';

// PLACEHOLDER during the Remix rebuild: replaced by the real screen.
export function TeamScreen(_handle: Handle) {
  return () => (
    <Page title="Team">
      <Empty title="Team">
        <p>This screen is being rebuilt.</p>
      </Empty>
    </Page>
  );
}
