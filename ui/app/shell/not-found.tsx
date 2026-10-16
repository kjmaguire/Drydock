import type { Handle } from 'remix/component';
import { routes } from '../routes.ts';
import { Empty } from '../ui/feedback.tsx';
import { Page } from '../ui/layout.tsx';

export function NotFound(handle: Handle<{ path: string }>) {
  return () => (
    <Page title="Not found">
      <Empty title="Nothing lives here">
        <p>
          <code>{handle.props.path}</code> is not a Drydock screen. <a href={routes.home.href()}>Back to the project</a>
        </p>
      </Empty>
    </Page>
  );
}
