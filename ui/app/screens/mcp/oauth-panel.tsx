// Sign-in for a server that uses OAuth. The core runs the flow; this panel shows the address of the server's own sign-in page
// (it comes from the server, so it is only linked when it is a safe address to open) and what happened.

import { css, on, type Handle } from 'remix/component';
import { ExternalLink, LogIn, LogOut, ShieldCheck } from 'lucide';
import type { McpLibraryServer } from '../../api/mcpClient.ts';
import { formatDateTime } from '../../lib/format.ts';
import { action, query, services, watch } from '../../state/services.tsx';
import { Button, buttonStyle } from '../../ui/button.tsx';
import { Badge, muted } from '../../ui/data.tsx';
import { ErrorNote, Spinner } from '../../ui/feedback.tsx';
import { Icon } from '../../ui/icon.tsx';
import { safeHref } from '../../ui/markdown.tsx';
import { t } from '../../ui/tokens.ts';

const panelCss = css({
  display: 'grid',
  gap: t.s2,
  padding: t.s3,
  border: `1px solid ${t.border}`,
  borderRadius: t.rMd,
  background: t.bgApp,
  '& p': { margin: '0' },
});
const headCss = css({ display: 'flex', alignItems: 'center', gap: t.s2, fontSize: t.textMd, fontWeight: '500', '& svg': { color: t.textMuted } });
const actionsCss = css({ display: 'flex', alignItems: 'center', gap: t.s2, flexWrap: 'wrap' });
const failCss = css({ padding: '6px 10px', borderRadius: t.rMd, background: t.dangerSoft, color: t.text, fontSize: t.textSm });
const waitCss = css({ display: 'grid', gap: t.s2, padding: t.s3, borderRadius: t.rMd, background: t.infoSoft, fontSize: t.textMd });

export function OAuthPanel(handle: Handle<{ server: McpLibraryServer; onChanged: () => void }>) {
  const s = services(handle);
  watch(handle, s.workspace);
  const act = action(handle);
  const st = query(handle, {
    load: () => s.client.mcpOAuthStatus(handle.props.server.id),
    key: () => [handle.props.server.id, handle.props.server.oauthAt ?? 0],
    when: (e) => e.type.startsWith('mcp.server'),
  });
  const run = async (what: string, f: () => Promise<unknown>) => {
    if (await act.run(what, f)) {
      st.reload();
      handle.props.onChanged();
    }
  };

  return () => {
    const { server } = handle.props;
    const ro = s.workspace.license;
    const d = st.data;
    const href = d?.url ? safeHref(d.url) : undefined;
    const signedIn = d?.signedIn ?? (server.oauthAt ?? 0) > 0;
    const at = d?.at ?? server.oauthAt;
    return (
      <div mix={panelCss} data-testid="oauth-panel" data-state={d?.state ?? 'idle'}>
        <p mix={headCss}>
          <Icon icon={ShieldCheck} size={15} />
          Sign-in{' '}
          {signedIn ? <Badge tone="ok">signed in{at ? ` ${formatDateTime(at)}` : ''}</Badge> : <Badge tone="warn">not signed in</Badge>}
        </p>
        <ErrorNote error={act.error ?? st.error} what="Sign-in" />
        {d?.state === 'failed' && d.error ? (
          <p mix={failCss} role="alert" data-testid="oauth-error">
            The sign-in did not finish: {d.error}
          </p>
        ) : null}
        {d?.state === 'waiting' ? (
          <div mix={waitCss} role="status" data-testid="oauth-waiting">
            <p mix={actionsCss}>
              <Spinner label="Waiting for the sign-in" />
              Finish signing in in your browser. Drydock is waiting.
            </p>
            <div mix={actionsCss}>
              {href ? (
                <a href={href} target="_blank" rel="noopener noreferrer" data-rmx-document data-testid="oauth-link" mix={buttonStyle('primary', 'sm')}>
                  <Icon icon={ExternalLink} size={14} />
                  Open the sign-in page
                </a>
              ) : (
                <p mix={failCss}>The server gave a sign-in address that is not safe to open, so it is not shown.</p>
              )}
              <Button size="sm" disabled={act.busy !== ''} mix={[on('click', () => run('cancel', () => s.client.mcpOAuthCancel(server.id)))]}>
                Cancel
              </Button>
            </div>
          </div>
        ) : (
          <div mix={actionsCss}>
            <Button
              size="sm"
              variant="primary"
              icon={LogIn}
              disabled={act.busy !== '' || ro.readOnly}
              title={ro.readOnly ? ro.reason : undefined}
              mix={[on('click', () => run('begin', () => s.client.mcpOAuthBegin(server.id)))]}
            >
              {signedIn ? 'Sign in again' : 'Sign in'}
            </Button>
            {signedIn ? (
              <Button size="sm" variant="ghost" icon={LogOut} disabled={act.busy !== ''} mix={[on('click', () => run('signout', () => s.client.mcpOAuthSignOut(server.id)))]}>
                Sign out
              </Button>
            ) : null}
            {!signedIn ? <span mix={[muted]} style={{ fontSize: 'var(--text-sm)' }}>Your browser opens the server's own sign-in page.</span> : null}
          </div>
        )}
      </div>
    );
  };
}
