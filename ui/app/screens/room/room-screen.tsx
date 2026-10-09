import type { Handle } from 'remix/component';
import { Page } from '../../ui/layout.tsx';
import { Empty } from '../../ui/feedback.tsx';

// PLACEHOLDER during the Remix rebuild: replaced by the real screen.
export function RoomScreen(_handle: Handle<{ id: string }>) {
  return () => (
    <Page title="Channel">
      <Empty title="Channel">
        <p>This screen is being rebuilt.</p>
      </Empty>
    </Page>
  );
}
