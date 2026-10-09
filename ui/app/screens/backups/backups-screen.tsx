import type { Handle } from 'remix/component';
import { Page } from '../../ui/layout.tsx';
import { Empty } from '../../ui/feedback.tsx';

// PLACEHOLDER during the Remix rebuild: replaced by the real screen.
export function BackupsScreen(_handle: Handle) {
  return () => (
    <Page title="Backups">
      <Empty title="Backups">
        <p>This screen is being rebuilt.</p>
      </Empty>
    </Page>
  );
}
