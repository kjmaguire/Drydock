import type { Handle } from 'remix/component';
import { Page } from '../../ui/layout.tsx';
import { Empty } from '../../ui/feedback.tsx';

// PLACEHOLDER during the Remix rebuild: replaced by the real screen.
export function ProvidersScreen(_handle: Handle) {
  return () => (
    <Page title="Providers">
      <Empty title="Providers">
        <p>This screen is being rebuilt.</p>
      </Empty>
    </Page>
  );
}
