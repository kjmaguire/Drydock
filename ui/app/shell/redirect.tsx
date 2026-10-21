import { navigate, type Handle } from 'remix/component';
import { Loading } from '../ui/feedback.tsx';

/**
 * Sends the window to another page of the app, replacing this one in the history so Back does not return to the address that only
 * redirects. Used for an old address that has a new home (/mcp, now /integrations): one page, one address (the UI audit, C3).
 * `go` replaces the navigation in tests.
 */
export function Redirect(handle: Handle<{ to: string; go?: (href: string) => void }>) {
  let went = false;
  handle.queueTask(() => {
    if (went) return;
    went = true;
    const { to, go } = handle.props;
    if (go) go(to);
    else void navigate(to, { history: 'replace' });
  });
  return () => <Loading label="Opening the page..." />;
}
