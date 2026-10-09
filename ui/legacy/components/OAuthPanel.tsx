import type { McpLibraryServer } from '../api/mcpClient';
import { safeHref } from '../lib/Markdown';
import { formatDateTime } from '../lib/format';
import { useAction } from '../lib/useAction';
import { useClient } from '../state/context';
import { useLive } from '../state/hooks';
import { useReadOnly } from '../state/license';
import { Badge, ErrorNote } from './common';

/**
 * Sign-in for a server that uses OAuth. The core runs the flow; this panel shows the address of the server's own sign-in page
 * (it comes from the server, so it is only linked when it is an https address or one on this computer) and what happened.
 */
export function OAuthPanel({ server, onChanged }: { server: McpLibraryServer; onChanged: () => void }) {
  const client = useClient();
  const ro = useReadOnly();
  const act = useAction();
  const st = useLive(() => client.mcpOAuthStatus(server.id), [server.id, server.oauthAt ?? 0], (e) => e.type.startsWith('mcp.server'));
  const s = st.data;
  const href = s?.url ? safeHref(s.url) : undefined;
  const run = async (what: string, f: () => Promise<unknown>) => {
    if (await act.run(what, f)) {
      st.reload();
      onChanged();
    }
  };
  const signedIn = s?.signedIn ?? (server.oauthAt ?? 0) > 0;
  return (
    <div className="oauth" data-testid="oauth-panel" data-state={s?.state ?? 'idle'}>
      <p>
        Sign-in{' '}
        {signedIn ? (
          <Badge tone="ok">signed in{s?.at || server.oauthAt ? ` ${formatDateTime((s?.at ?? server.oauthAt) as number)}` : ''}</Badge>
        ) : (
          <Badge tone="warn">not signed in</Badge>
        )}
      </p>
      <ErrorNote error={act.error ?? st.error} what="Sign-in" />
      {s?.state === 'failed' && s.error ? (
        <p className="error-note" role="alert" data-testid="oauth-error">
          The sign-in did not finish: {s.error}
        </p>
      ) : null}
      {s?.state === 'waiting' ? (
        <div role="status" data-testid="oauth-waiting">
          <p>Finish signing in in your browser. Drydock is waiting.</p>
          {href ? (
            <p>
              <a href={href} target="_blank" rel="noopener noreferrer" data-testid="oauth-link">
                Open the sign-in page
              </a>
            </p>
          ) : (
            <p className="error-note">The server gave a sign-in address that is not safe to open, so it is not shown.</p>
          )}
          <button disabled={act.busy !== ''} onClick={() => run('cancel', () => client.mcpOAuthCancel(server.id))}>
            Cancel
          </button>
        </div>
      ) : (
        <div className="form-actions">
          <button className="primary" disabled={act.busy !== '' || ro.readOnly} title={ro.readOnly ? ro.reason : undefined} onClick={() => run('begin', () => client.mcpOAuthBegin(server.id))}>
            {signedIn ? 'Sign in again' : 'Sign in'}
          </button>
          {signedIn ? (
            <button disabled={act.busy !== ''} onClick={() => run('signout', () => client.mcpOAuthSignOut(server.id))}>
              Sign out
            </button>
          ) : null}
        </div>
      )}
    </div>
  );
}
