import type { Handle } from 'remix/component';
import { Page } from '../../ui/layout.tsx';
import { Empty } from '../../ui/feedback.tsx';

// PLACEHOLDER during the Remix rebuild: replaced by the real screen.
export function ProjectCreateScreen(_handle: Handle<{ first?: boolean }>) {
  return () => (
    <Page title="New project">
      <Empty title="New project">
        <p>This screen is being rebuilt.</p>
      </Empty>
    </Page>
  );
}
