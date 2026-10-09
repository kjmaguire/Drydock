import type { Handle } from 'remix/component';
import { Page } from '../../ui/layout.tsx';
import { Empty } from '../../ui/feedback.tsx';

// PLACEHOLDER during the Remix rebuild: replaced by the real screen.
export function LibraryScreen(_handle: Handle) {
  return () => (
    <Page title="Library">
      <Empty title="Library">
        <p>This screen is being rebuilt.</p>
      </Empty>
    </Page>
  );
}
