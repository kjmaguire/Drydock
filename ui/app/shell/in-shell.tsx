// What every route's content is drawn inside (router.tsx): the services, then the shell. The shell is in a boundary of its own, so
// that a shell that throws while it draws (a malformed answer of the core reaching it) shows what failed, with Try again and
// Reload, and not a blank window; the page, the dock and the details panel have boundaries inside the shell (shell.tsx).

import type { RemixNode } from 'remix/component';
import { Services, type AppServices } from '../state/services.tsx';
import { ErrorBoundary } from '../ui/error-boundary.tsx';
import { Shell } from './shell.tsx';

export function inShell(services: AppServices, content: RemixNode, url: URL): RemixNode {
  return (
    <Services value={services}>
      <ErrorBoundary label="Drydock" resetKey={url.pathname}>
        <Shell url={url}>{content}</Shell>
      </ErrorBoundary>
    </Services>
  );
}
