// The MCP connector cards (decision P43), drawn as rows (decision P98): a known server's logo, name, publisher and state on one
// line with its one action (Connect, Try again, Disconnect), then its facts as settings rows: what it gives agents, how it signs
// in, its license and documentation, its tools (closed), what the server tells clients and how many projects have it. Connecting is
// done once for the workspace and needs no project; a connected server is added to a project and granted to its agents on that
// project's Integrations page. The cards are on the Integrations page, in the detail of their vendor (decision P47); a service's
// detail shows its vendor's server compactly (one row, the facts under Details). AddMcpServer is the "Add an MCP server" dialog's
// content: a server of your own (a web address or a program on this PC), or the servers another app's configuration file lists.

import { css, on, type Handle } from 'remix/component';
import { ExternalLink, FileUp, Plug, Plus, RefreshCw, Unplug } from 'lucide';
import type { McpGalleryCard, McpLibraryServer, McpServerIdentity } from '../../api/mcpClient.ts';
import { plural } from '../../lib/format.ts';
import { action, services, watch } from '../../state/services.tsx';
import { BrandLogo } from '../../ui/brand-logo.tsx';
import { Button } from '../../ui/button.tsx';
import { ClippedText } from '../../ui/clipped-text.tsx';
import { faint, mono, muted } from '../../ui/data.tsx';
import { Disclosure } from '../../ui/disclosure.tsx';
import { ErrorNote } from '../../ui/feedback.tsx';
import { Icon } from '../../ui/icon.tsx';
import { safeHref } from '../../ui/markdown.tsx';
import { SettingRow, StatusWord } from '../../ui/master-detail.tsx';
import { t, type Tone } from '../../ui/tokens.ts';
import { toolsFact } from '../integrations/integrations.ts';
import { ImportServers } from './import-panel.tsx';
import { OAuthPanel } from './oauth-panel.tsx';
import { McpServerForm } from './server-form.tsx';

const cardCss = css({
  display: 'grid',
  minWidth: '0',
  borderTop: `1px solid ${t.border}`,
  '&:first-child': { borderTop: '0' },
  '& p': { margin: '0' },
  '& > .head': { display: 'flex', alignItems: 'center', gap: t.s3, minHeight: '52px', padding: `${t.s2} ${t.s4}`, minWidth: '0', flexWrap: 'wrap' },
  '& .who': { display: 'grid', minWidth: '0', flex: '1 1 200px' },
  '& h4': { margin: '0', fontSize: t.textMd, fontWeight: '600', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' },
  '& .pub': { fontSize: t.textSm, color: t.textMuted, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' },
  // the base layer sets code a little smaller than its text: at the smallest size that is under 12 px (the layout guard)
  '& .pub code': { fontSize: 'inherit' },
  '& .ctl': { display: 'flex', alignItems: 'center', gap: t.s2, flexWrap: 'wrap' },
  '& > .inline': { display: 'grid', gap: t.s2, padding: `0 ${t.s4} ${t.s3}`, minWidth: '0' },
  '& > .inline:empty': { display: 'none' },
  '& > .rows': { borderTop: `1px solid ${t.border}` },
  '& > .more': { padding: `0 ${t.s4} ${t.s2}` },
  '& > .more .rows': { border: `1px solid ${t.border}`, borderRadius: t.rMd, overflow: 'hidden' },
  '& a.docs': { display: 'inline-flex', alignItems: 'center', gap: '4px', minHeight: '28px' },
  '& ul.tools': { display: 'grid', gap: '4px', margin: '0', padding: '0 0 0 18px', fontSize: t.textSm },
  '& code.cmd': { display: 'block', padding: '4px 8px', borderRadius: t.rMd, background: t.bgApp, border: `1px solid ${t.border}`, overflowWrap: 'anywhere' },
});
const errCss = css({ padding: '6px 10px', borderRadius: t.rMd, background: t.dangerSoft, color: t.text, fontSize: t.textSm, overflowWrap: 'anywhere' });
const confirmCss = css({ display: 'flex', alignItems: 'center', gap: t.s2, flexWrap: 'wrap', '& > span': { flex: '1 1 240px' } });
const tokenCss = css({ display: 'grid', gap: '6px', '& label': { fontSize: t.textSm, fontWeight: '500' }, '& .hint': { fontSize: t.textSm, color: t.textMuted }, '& .row': { display: 'flex', gap: t.s2, flexWrap: 'wrap' } });
const instrCss = css({ margin: '0', maxHeight: '160px', overflow: 'auto', whiteSpace: 'pre-wrap', overflowWrap: 'anywhere', fontSize: t.textSm });

const STATUS: Record<string, { label: string; tone: Tone }> = {
  not_connected: { label: 'Not connected', tone: 'muted' },
  connected: { label: 'Connected', tone: 'ok' },
  needs_sign_in: { label: 'Needs sign-in', tone: 'warn' },
  needs_token: { label: 'Needs a token', tone: 'warn' },
  error: { label: 'Error', tone: 'err' },
};

const AUTH: Record<string, string> = {
  none: 'No account needed',
  bearer: 'Token',
  oauth: 'Sign in (OAuth)',
  env: 'Program on this computer',
};

/** "Server says it is <title> (<name> <version>)" as plain text. */
export function identityLine(i: McpServerIdentity | undefined): string {
  if (!i || !i.name) return '';
  const tech = [i.name, i.version].filter(Boolean).join(' ');
  return i.title && i.title !== i.name ? `${i.title} (${tech})` : tech;
}

function TokenForm(handle: Handle<{ card: McpGalleryCard; busy: boolean; onSubmit: (token: string) => void; onCancel?: () => void }>) {
  let value = '';
  return () => {
    const { card, busy, onSubmit, onCancel } = handle.props;
    const what = card.auth === 'env' ? card.secretEnv! : card.authOptional ? 'API key (optional)' : 'Token';
    const id = `tok-${card.id}`;
    return (
      <form
        mix={[
          tokenCss,
          on<HTMLFormElement, 'submit'>('submit', (e) => {
            e.preventDefault();
            onSubmit(value);
            value = '';
          }),
        ]}
        data-testid="connector-token"
      >
        <label for={id}>
          {what} for {card.display}
        </label>
        <input id={id} type="password" autocomplete="off" spellcheck={false} required={!card.authOptional} value={value} mix={on('input', (e) => ((value = (e.currentTarget as HTMLInputElement).value), void handle.update()))} />
        {card.tokenHelp ? <span class="hint">{card.tokenHelp}</span> : null}
        <span class="hint">Kept in the Windows credential store, never shown again.</span>
        <div class="row">
          <Button type="submit" size="sm" variant="primary" disabled={busy}>
            {busy ? 'Connecting...' : 'Connect'}
          </Button>
          {onCancel ? (
            <Button size="sm" variant="ghost" mix={[on('click', onCancel)]}>
              Cancel
            </Button>
          ) : null}
        </div>
      </form>
    );
  };
}

/**
 * The OAuth app a person registered with a provider that has no dynamic client registration (decision P122): its client id and
 * secret, sent once to the credential store, never shown again. The help says what to enter when registering it.
 */
function AppForm(handle: Handle<{ card: McpGalleryCard; busy: boolean; onSubmit: (clientId: string, clientSecret: string) => void; onCancel: () => void }>) {
  let id = '';
  let secret = '';
  return () => {
    const { card, busy, onSubmit, onCancel } = handle.props;
    const app = card.oauthApp!;
    const reg = safeHref(app.registerUrl);
    const input = (set: (v: string) => void) => on<HTMLInputElement, 'input'>('input', (e) => (set(e.currentTarget.value), void handle.update()));
    return (
      <form
        mix={[
          tokenCss,
          on<HTMLFormElement, 'submit'>('submit', (e) => {
            e.preventDefault();
            onSubmit(id.trim(), secret.trim());
            secret = '';
          }),
        ]}
        data-testid="connector-app"
      >
        <span class="hint">
          {app.help ?? 'Register an OAuth app with the provider, then enter its client id and secret.'}{' '}
          {reg ? (
            <a href={reg} target="_blank" rel="noopener noreferrer" data-rmx-document>
              Register an app <Icon icon={ExternalLink} size={12} />
            </a>
          ) : null}
        </span>
        <label for={`app-id-${card.id}`}>Client id</label>
        <input id={`app-id-${card.id}`} autocomplete="off" spellcheck={false} required value={id} mix={input((v) => (id = v))} />
        <label for={`app-secret-${card.id}`}>Client secret</label>
        <input id={`app-secret-${card.id}`} type="password" autocomplete="off" spellcheck={false} value={secret} mix={input((v) => (secret = v))} />
        <span class="hint">Kept in the Windows credential store, never shown again.</span>
        <div class="row">
          <Button type="submit" size="sm" variant="primary" disabled={busy}>
            {busy ? 'Connecting...' : 'Sign in'}
          </Button>
          <Button size="sm" variant="ghost" mix={[on('click', onCancel)]}>
            Cancel
          </Button>
        </div>
      </form>
    );
  };
}

/**
 * One catalog server: its state and one action on a line, what it needs right now under it (its sign-in, a token, an error, a
 * confirmation), then its facts as rows. `compact` puts the facts under a closed Details (a service's own detail, where the server
 * is one row among the connection's). `settingsHref` links the connected server's own settings (which tools only read, Edit).
 * Nothing of a project: a connected server is put into a project, and granted to that project's agents, on the project's page.
 */
export function ConnectorCard(handle: Handle<{ card: McpGalleryCard; onChanged: () => void; compact?: boolean; settingsHref?: string }>) {
  const s = services(handle);
  watch(handle, s.workspace);
  const act = action(handle);
  let asking = false; // the token form is open
  let withApp = false; // the OAuth app form is open (decision P122)
  let confirm = false;
  const set = (f: () => void) => {
    f();
    void handle.update();
  };
  const connect = async (token?: string) => {
    const r = await act.run('connect', () => s.client.mcpConnect(handle.props.card.id, token || undefined));
    if (r) set(() => (asking = false));
    handle.props.onChanged();
  };
  const connectWithApp = async (clientId: string, clientSecret: string) => {
    const r = await act.run('connect', () => s.client.mcpConnectWithApp(handle.props.card.id, clientId, clientSecret));
    if (r) set(() => (withApp = false));
    handle.props.onChanged();
  };

  return () => {
    const { card, onChanged, compact, settingsHref } = handle.props;
    const ro = s.workspace.license;
    const st = STATUS[card.status] ?? STATUS.error!;
    const connected = card.status === 'connected';
    const needsSecret = card.auth === 'env' || (card.auth === 'bearer' && !card.authOptional);
    const docs = card.docsUrl ? safeHref(card.docsUrl) : undefined;
    const n = card.projects.length;
    const showToken = asking || card.status === 'needs_token';
    const pub = [card.publisher + (card.official ? ' · official' : ''), compact ? toolsFact([card]) : '', compact && n > 0 ? `in ${plural(n, 'project')}` : ''].filter(Boolean).join(' · ');
    const rows = (
      <div class="rows">
        <SettingRow label="What it does">
          <span mix={muted}>{card.summary}</span>
        </SettingRow>
        <SettingRow label="Sign-in">
          {AUTH[card.auth] ?? card.auth}
          {card.oauthApp ? ', or sign in through an OAuth app you register' : ''}
        </SettingRow>
        <SettingRow
          label="License"
          control={
            docs ? (
              <a href={docs} target="_blank" rel="noopener noreferrer" data-rmx-document class="docs">
                Docs <Icon icon={ExternalLink} size={12} />
              </a>
            ) : undefined
          }
        >
          <span title={card.licenseNotes}>{card.license}</span>
        </SettingRow>
        {card.template && !card.libraryId && card.command ? (
          <SettingRow label="Starts on your PC">
            <code class="cmd" mix={mono}>
              {card.command.join(' ')}
            </code>
          </SettingRow>
        ) : null}
        {card.tools.length > 0 ? (
          <SettingRow label="Tools">
            <Disclosure summary={`${plural(card.tools.length, 'tool')}${card.toolsListed ? '' : ' (as documented; the server lists its own when it connects)'}`}>
              <ul class="tools" aria-label={`Tools of ${card.display}`}>
                {card.tools.map((tool) => (
                  <li key={tool.name} data-testid="connector-tool" data-tool={tool.name}>
                    <code>{tool.name}</code> {card.readOnlyTools.includes(tool.name) ? <span mix={faint}>reads only</span> : <span mix={faint}>asks you first</span>}
                    {tool.description ? <ClippedText text={tool.description} mix={[faint]} data-testid="connector-tool-description" /> : null}
                  </li>
                ))}
              </ul>
            </Disclosure>
          </SettingRow>
        ) : null}
        {card.serverInfo?.instructions ? (
          <SettingRow label="It tells clients">
            <Disclosure summary="What the server tells clients">
              <p mix={instrCss} data-testid="connector-instructions">
                {card.serverInfo.instructions}
              </p>
            </Disclosure>
          </SettingRow>
        ) : null}
        {card.libraryId ? (
          <SettingRow label="Projects" control={settingsHref ? <a href={settingsHref} data-testid="connector-settings">Tools and settings</a> : undefined}>
            <span mix={faint} data-testid="connector-projects">
              {n > 0 ? `In ${n} project${n === 1 ? '' : 's'}. ` : ''}Add it to a project, and grant it to agents, on that project's Integrations page.
            </span>
          </SettingRow>
        ) : null}
      </div>
    );
    return (
      <div mix={cardCss} data-testid="connector" data-id={card.id} data-status={card.status} role="group" aria-label={card.display}>
        <div class="head">
          <BrandLogo id={card.brand || card.id} size={compact ? 24 : 28} />
          <div class="who">
            <h4 title={card.display}>{card.display}</h4>
            <span class="pub">{pub}</span>
          </div>
          <StatusWord text={st.label} tone={st.tone} testId="connector-status" />
          <div class="ctl">
            {!card.libraryId && card.auth !== '' ? (
              <Button
                size="sm"
                variant="primary"
                icon={Plug}
                disabled={act.busy !== '' || ro.readOnly}
                title={ro.readOnly ? ro.reason : undefined}
                aria-label={`Connect ${card.display}`}
                mix={[on('click', () => (needsSecret && !asking ? set(() => (asking = true)) : void connect()))]}
              >
                {act.busy === 'connect' ? 'Connecting...' : 'Connect'}
              </Button>
            ) : null}
            {card.oauthApp && !withApp && (!card.libraryId || (card.oauth && !card.oauthClient)) ? (
              <Button size="sm" variant="ghost" disabled={act.busy !== '' || ro.readOnly} mix={[on('click', () => set(() => ((withApp = true), (asking = false))))]}>
                Sign in with your own app
              </Button>
            ) : null}
            {!card.libraryId && card.authOptional && !asking ? (
              <Button size="sm" variant="ghost" mix={[on('click', () => set(() => (asking = true)))]}>
                Add a key
              </Button>
            ) : null}
            {card.status === 'error' && card.libraryId ? (
              <Button
                size="sm"
                icon={RefreshCw}
                disabled={act.busy !== ''}
                mix={[
                  on('click', async () => {
                    await act.run('retry', () => s.client.mcpLibDiscover(card.libraryId!));
                    onChanged();
                  }),
                ]}
              >
                Try again
              </Button>
            ) : null}
            {card.libraryId && !confirm ? (
              <Button size="sm" variant="ghost" icon={Unplug} mix={[on('click', () => set(() => (confirm = true)))]}>
                Disconnect
              </Button>
            ) : null}
          </div>
        </div>
        <div class="inline">
          <ErrorNote error={act.error} what="Not connected" />
          {connected ? (
            <p data-testid="connector-account" mix={muted} style={{ fontSize: 'var(--text-sm)' }}>
              {card.account ? (
                <>
                  Connected as <strong>{card.account}</strong>
                </>
              ) : (
                'Connected'
              )}
              {card.serverInfo?.name ? <span mix={faint}> · the server says it is {identityLine(card.serverInfo)}</span> : null}
            </p>
          ) : null}
          {card.status === 'error' && card.detail ? (
            <p mix={errCss} role="alert" data-testid="connector-error">
              {card.detail}
            </p>
          ) : null}
          {(card.auth === 'oauth' || card.oauth) && card.libraryId && !withApp ? (
            <OAuthPanel server={{ id: card.libraryId, oauthAt: connected ? 1 : 0, oauthClient: card.oauthClient } as McpLibraryServer} onChanged={onChanged} />
          ) : null}
          {withApp ? <AppForm card={card} busy={act.busy === 'connect'} onSubmit={(id, secret) => void connectWithApp(id, secret)} onCancel={() => set(() => (withApp = false))} /> : null}
          {showToken && needsSecret && !withApp && !card.oauth ? <TokenForm card={card} busy={act.busy === 'connect'} onSubmit={(v) => void connect(v)} onCancel={card.libraryId ? undefined : () => set(() => (asking = false))} /> : null}
          {asking && card.authOptional ? <TokenForm card={card} busy={act.busy === 'connect'} onSubmit={(v) => void connect(v)} onCancel={() => set(() => (asking = false))} /> : null}
          {confirm && card.libraryId ? (
            <div mix={[confirmCss, errCss]}>
              <span>Disconnect {card.display}? It leaves every project and its stored sign-in or token is deleted.</span>
              <Button
                size="sm"
                variant="danger"
                disabled={act.busy !== ''}
                mix={[
                  on('click', async () => {
                    await act.run('delete', () => s.client.mcpLibDelete(card.libraryId!));
                    set(() => (confirm = false));
                    onChanged();
                  }),
                ]}
              >
                Yes, disconnect
              </Button>
              <Button size="sm" mix={[on('click', () => set(() => (confirm = false)))]}>
                Keep
              </Button>
            </div>
          ) : null}
        </div>
        {compact ? (
          <div class="more">
            <Disclosure summary="Details">{rows}</Disclosure>
          </div>
        ) : (
          rows
        )}
      </div>
    );
  };
}

const addCss = css({ display: 'grid', gap: t.s4, minWidth: '0', '& > p': { margin: '0' } });
const modeCss = css({
  display: 'inline-flex',
  justifySelf: 'start',
  gap: '2px',
  padding: '2px',
  borderRadius: t.rMd,
  background: t.bgInput,
  '& > button': { height: '28px', padding: `0 ${t.s3}`, border: '0', borderRadius: t.rSm, background: 'transparent', color: t.textMuted, fontSize: t.textSm, fontWeight: '500' },
  '& > button:hover': { color: t.text, background: 'transparent' },
  '& > button[aria-pressed="true"]': { background: t.bgActive, color: t.text, boxShadow: t.shadowSm },
});

/**
 * The "Add an MCP server" dialog: a server of your own, described in the server form (a web address or a program on this PC), or
 * the servers another app's claude_desktop_config.json or mcp.json lists. The servers built into Drydock are not here: each is a
 * row under Agent tools, connected from its own detail.
 */
export function AddMcpServer(
  handle: Handle<{
    existingIds: readonly string[];
    /** A server was saved from the form. */
    onSaved: (saved: McpLibraryServer) => void;
    /** Servers were imported from a file. */
    onImported: (n: number) => void;
    onCancel: () => void;
  }>,
) {
  let mode: 'form' | 'import' = 'form';
  const set = (m: 'form' | 'import') => {
    mode = m;
    void handle.update();
  };
  return () => {
    const { existingIds, onSaved, onImported, onCancel } = handle.props;
    return (
      <div mix={addCss} data-testid="mcp-add">
        <p mix={muted}>A web address or a program on this PC. The servers built into Drydock are under Agent tools.</p>
        <div mix={modeCss} role="group" aria-label="How to add it" data-testid="mcp-add-custom">
          <button type="button" aria-pressed={mode === 'form' ? 'true' : 'false'} mix={on('click', () => set('form'))}>
            <Icon icon={Plus} size={13} /> Describe it
          </button>
          <button type="button" aria-pressed={mode === 'import' ? 'true' : 'false'} mix={on('click', () => set('import'))}>
            <Icon icon={FileUp} size={13} /> Import from a file
          </button>
        </div>
        {mode === 'form' ? <McpServerForm onCancel={onCancel} onDone={onSaved} /> : <ImportServers existingIds={existingIds} onCancel={() => set('form')} onDone={onImported} />}
      </div>
    );
  };
}
