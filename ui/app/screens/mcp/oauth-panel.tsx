// Sign-in for a server that uses OAuth. The core runs the flow; this panel shows the address of the server's own sign-in page
// (it comes from the server, so it is only linked when it is a safe address to open) and what happened. Not signed in, it is
// one button, Sign in (its title says the browser opens the server's own sign-in page); signed in, one compact line with
// Sign out and Sign in again. After a failed sign-in it also takes an OAuth app the person registered with a provider that does not
// let Drydock register itself (decision P122): its client id and secret go to the credential store once.

import { css, on, type Handle } from 'remix/component';
import { ExternalLink, LogIn, LogOut, ShieldCheck } from 'lucide';
import type { McpLibraryServer } from '../../api/mcpClient.ts';
import { formatDateTime } from '../../lib/format.ts';
import { action, query, services, watch } from '../../state/services.tsx';
import { Button, buttonStyle } from '../../ui/button.tsx';
import { ErrorNote, Spinner } from '../../ui/feedback.tsx';
import { Icon } from '../../ui/icon.tsx';
import { safeHref } from '../../ui/markdown.tsx';
import { t } from '../../ui/tokens.ts';

/** What the Sign in button's title says: the person's browser opens the server's own page, not Drydock's. */
export const SIGN_IN_HINT = "Your browser opens the server's own sign-in page.";

const panelCss = css({ display: 'grid', gap: t.s2, '& p': { margin: '0' } });
const actionsCss = css({ display: 'flex', alignItems: 'center', gap: t.s2, flexWrap: 'wrap' });
const signedCss = css({ display: 'inline-flex', alignItems: 'center', gap: '6px', fontSize: t.textMd, color: t.textMuted, '& svg': { color: t.success } });
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
  // an OAuth app the person registered, for a provider that does not let Drydock register itself (decision P122)
  let appOpen = false;
  let appId = '';
  let appSecret = '';
  const setApp = async (id: string, secret: string) => {
    if (await act.run('client', () => s.client.mcpOAuthClient(handle.props.server.id, id, secret))) {
      appOpen = false;
      appSecret = '';
      st.reload();
      handle.props.onChanged();
    }
    void handle.update();
  };
  const field = (set: (v: string) => void) => on<HTMLInputElement, 'input'>('input', (e) => (set(e.currentTarget.value), void handle.update()));

  return () => {
    const { server } = handle.props;
    const ro = s.workspace.license;
    const d = st.data;
    const href = d?.url ? safeHref(d.url) : undefined;
    const signedIn = d?.signedIn ?? (server.oauthAt ?? 0) > 0;
    const at = d?.at ?? server.oauthAt;
    const begin = (label: string, variant: 'primary' | 'ghost') => (
      <Button
        size="sm"
        variant={variant}
        icon={LogIn}
        disabled={act.busy !== '' || ro.readOnly}
        title={ro.readOnly ? ro.reason : SIGN_IN_HINT}
        mix={[on('click', () => run('begin', () => s.client.mcpOAuthBegin(server.id)))]}
      >
        {label}
      </Button>
    );
    return (
      <div mix={panelCss} data-testid="oauth-panel" data-state={d?.state ?? 'idle'} data-signed-in={String(signedIn)}>
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
        ) : signedIn ? (
          <div mix={actionsCss}>
            <span mix={signedCss} data-testid="oauth-signed-in">
              <Icon icon={ShieldCheck} size={14} />
              Signed in{at ? ` ${formatDateTime(at)}` : ''}
            </span>
            <Button size="sm" variant="ghost" icon={LogOut} disabled={act.busy !== ''} mix={[on('click', () => run('signout', () => s.client.mcpOAuthSignOut(server.id)))]}>
              Sign out
            </Button>
            {begin('Sign in again', 'ghost')}
          </div>
        ) : (
          <div mix={actionsCss}>{begin('Sign in', 'primary')}</div>
        )}
        {server.oauthClient ? (
          <p mix={actionsCss} data-testid="oauth-app">
            <span mix={signedCss}>Signs in through your own OAuth app.</span>
            <Button size="sm" variant="ghost" disabled={act.busy !== '' || ro.readOnly} mix={[on('click', () => void setApp('', ''))]}>
              Remove the app
            </Button>
          </p>
        ) : appOpen ? (
          <form
            mix={[
              panelCss,
              on<HTMLFormElement, 'submit'>('submit', (e) => {
                e.preventDefault();
                void setApp(appId.trim(), appSecret.trim());
              }),
            ]}
            data-testid="oauth-app-form"
          >
            <label for={`oa-id-${server.id}`}>Client id of your OAuth app</label>
            <input id={`oa-id-${server.id}`} autocomplete="off" spellcheck={false} required value={appId} mix={field((v) => (appId = v))} />
            <label for={`oa-secret-${server.id}`}>Client secret (if the app has one)</label>
            <input id={`oa-secret-${server.id}`} type="password" autocomplete="off" spellcheck={false} value={appSecret} mix={field((v) => (appSecret = v))} />
            <div mix={actionsCss}>
              <Button type="submit" size="sm" variant="primary" disabled={act.busy !== ''}>
                Save the app
              </Button>
              <Button size="sm" variant="ghost" mix={[on('click', () => ((appOpen = false), void handle.update()))]}>
                Cancel
              </Button>
            </div>
          </form>
        ) : d?.state !== 'failed' ? null : (
          <div mix={actionsCss}>
            <Button size="sm" variant="ghost" disabled={ro.readOnly} title="For a provider that does not let Drydock register itself: its redirect address is http://127.0.0.1/callback" mix={[on('click', () => ((appOpen = true), void handle.update()))]}>
              Use your own OAuth app
            </Button>
          </div>
        )}
      </div>
    );
  };
}
