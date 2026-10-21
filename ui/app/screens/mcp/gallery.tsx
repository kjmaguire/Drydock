// The MCP connector cards (decision P43), the way other apps present connectors: a card per known server with its logo, name,
// publisher and what it gives agents; one Connect button (the browser sign-in, a token, or the program a template runs); then
// Connected, as whom, with the tools it offers, and Disconnect. Connecting is done once for the workspace and needs no project;
// a connected server is added to a project and granted to its agents on that project's Integrations page. The cards sit on the
// Integrations page, in the Agent tools section of their vendor's tile (decision P47), and first in the Library's "Connect an
// MCP server" dialog (AddMcpServer, before the custom server form). "MCP servers you added" shows the servers you added yourself
// (not from the catalog), by what each says about itself.

import { css, on, type Handle } from 'remix/component';
import { ExternalLink, FileUp, Plug, Plus, RefreshCw, Unplug } from 'lucide';
import type { McpGalleryCard, McpLibraryServer, McpServerIdentity } from '../../api/mcpClient.ts';
import { action, query, services, watch } from '../../state/services.tsx';
import { BrandLogo } from '../../ui/brand-logo.tsx';
import { Button } from '../../ui/button.tsx';
import { ClippedText } from '../../ui/clipped-text.tsx';
import { Badge, faint, mono, muted } from '../../ui/data.tsx';
import { ErrorNote, Loading } from '../../ui/feedback.tsx';
import { Icon } from '../../ui/icon.tsx';
import { safeHref } from '../../ui/markdown.tsx';
import { Section } from '../../ui/layout.tsx';
import { t, type Tone } from '../../ui/tokens.ts';
import { ImportServers } from './import-panel.tsx';
import { OAuthPanel } from './oauth-panel.tsx';
import { McpServerForm } from './server-form.tsx';

/** The grid the connector cards sit in. */
export const connectorGrid = css({ display: 'grid', gridTemplateColumns: 'repeat(auto-fill, minmax(300px, 1fr))', gap: t.s3, margin: '0', padding: '0', listStyle: 'none' });
const cardCss = css({
  display: 'grid',
  alignContent: 'start',
  gap: t.s2,
  padding: t.s3,
  border: `1px solid ${t.border}`,
  borderRadius: t.rLg,
  background: t.bgSurface,
  minWidth: '0',
  '&[data-status="connected"]': { borderColor: `color-mix(in srgb, ${t.success} 40%, ${t.border})` },
  '& p': { margin: '0' },
  '& .head': { display: 'flex', alignItems: 'center', gap: t.s2, minWidth: '0' },
  '& .who': { display: 'grid', minWidth: '0', flex: '1' },
  '& h4': { margin: '0', fontSize: t.textMd, fontWeight: '600', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' },
  '& .pub': { fontSize: t.textXs, color: t.textMuted },
  // the base layer sets code a little smaller than its text: at the smallest size that is under 12 px (the layout guard)
  '& .pub code': { fontSize: 'inherit' },
  '& .sum': { color: t.textMuted, fontSize: t.textSm, lineHeight: '1.45' },
  '& .meta a': { display: 'inline-flex', alignItems: 'center', minHeight: '28px' },
  '& .meta': { display: 'flex', alignItems: 'center', gap: `${t.s1} ${t.s3}`, flexWrap: 'wrap', fontSize: t.textXs, color: t.textMuted },
  '& .row': { display: 'flex', alignItems: 'center', gap: t.s2, flexWrap: 'wrap' },
  '& details summary': { cursor: 'pointer', fontSize: t.textSm, color: t.textMuted },
  '& details ul': { margin: '6px 0 0', paddingLeft: '18px', fontSize: t.textSm },
  '& code.cmd': { display: 'block', padding: '4px 8px', borderRadius: t.rMd, background: t.bgApp, border: `1px solid ${t.border}`, overflowWrap: 'anywhere' },
  '& select': { width: 'auto', minWidth: '120px', maxWidth: '100%' },
});
const errCss = css({ padding: '6px 10px', borderRadius: t.rMd, background: t.dangerSoft, color: t.text, fontSize: t.textSm, overflowWrap: 'anywhere' });
const tokenCss = css({ display: 'grid', gap: '6px', '& label': { fontSize: t.textSm, fontWeight: '500' }, '& .hint': { fontSize: t.textXs, color: t.textMuted } });
const instrCss = css({ margin: '6px 0 0', maxHeight: '160px', overflow: 'auto', whiteSpace: 'pre-wrap', overflowWrap: 'anywhere', fontSize: t.textSm });

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
        <span class="hint">It goes to the Windows credential store. Drydock never shows it again, logs it or stores it anywhere else.</span>
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
 * One catalog server: what it is, its state, Connect (or its sign-in or token), its tools, and Disconnect. Nothing of a project:
 * a connected server is put into a project, and granted to that project's agents, on the project's Integrations page.
 */
export function ConnectorCard(handle: Handle<{ card: McpGalleryCard; onChanged: () => void }>) {
  const s = services(handle);
  watch(handle, s.workspace);
  const act = action(handle);
  let asking = false; // the token form is open
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

  return () => {
    const { card, onChanged } = handle.props;
    const ro = s.workspace.license;
    const st = STATUS[card.status] ?? STATUS.error!;
    const connected = card.status === 'connected';
    const needsSecret = card.auth === 'env' || (card.auth === 'bearer' && !card.authOptional);
    const docs = card.docsUrl ? safeHref(card.docsUrl) : undefined;
    const n = card.projects.length;
    const showToken = asking || card.status === 'needs_token';
    return (
      <li mix={cardCss} data-testid="connector" data-id={card.id} data-status={card.status} aria-label={card.display}>
        <div class="head">
          <BrandLogo id={card.brand || card.id} size={32} />
          <div class="who">
            <h4 title={card.display}>{card.display}</h4>
            <span class="pub">
              {card.publisher}
              {card.official ? ' · official' : ''}
            </span>
          </div>
          <Badge tone={st.tone} testId="connector-status">
            {st.label}
          </Badge>
        </div>
        <p class="sum">{card.summary}</p>
        <div class="meta">
          <Badge>{AUTH[card.auth] ?? card.auth}</Badge>
          <span title={card.licenseNotes}>License {card.license}</span>
          {docs ? (
            <a href={docs} target="_blank" rel="noopener noreferrer" data-rmx-document>
              Docs <Icon icon={ExternalLink} size={11} />
            </a>
          ) : null}
        </div>
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
        {card.template && !card.libraryId && card.command ? (
          <div>
            <span mix={faint}>Starts on your computer:</span>
            <code class="cmd" mix={mono}>
              {card.command.join(' ')}
            </code>
          </div>
        ) : null}
        {card.auth === 'oauth' && card.libraryId ? (
          <OAuthPanel server={{ id: card.libraryId, oauthAt: connected ? 1 : 0 } as McpLibraryServer} onChanged={onChanged} />
        ) : null}
        {showToken && needsSecret ? <TokenForm card={card} busy={act.busy === 'connect'} onSubmit={(v) => void connect(v)} onCancel={card.libraryId ? undefined : () => set(() => (asking = false))} /> : null}
        {asking && card.authOptional ? <TokenForm card={card} busy={act.busy === 'connect'} onSubmit={(v) => void connect(v)} onCancel={() => set(() => (asking = false))} /> : null}
        {card.tools.length > 0 ? (
          <details>
            <summary>
              {card.tools.length} {card.tools.length === 1 ? 'tool' : 'tools'}
              {card.toolsListed ? '' : ' (as documented; the server lists its own when it connects)'}
            </summary>
            <ul aria-label={`Tools of ${card.display}`}>
              {card.tools.map((tool) => (
                <li key={tool.name} data-testid="connector-tool" data-tool={tool.name}>
                  <code>{tool.name}</code> {card.readOnlyTools.includes(tool.name) ? <span mix={faint}>reads only</span> : <span mix={faint}>asks you first</span>}
                  {tool.description ? <ClippedText text={tool.description} mix={[faint]} data-testid="connector-tool-description" /> : null}
                </li>
              ))}
            </ul>
          </details>
        ) : null}
        {card.serverInfo?.instructions ? (
          <details>
            <summary>What the server tells clients</summary>
            <p mix={instrCss} data-testid="connector-instructions">
              {card.serverInfo.instructions}
            </p>
          </details>
        ) : null}
        {card.libraryId ? (
          <p mix={faint} data-testid="connector-projects">
            {n > 0 ? `In ${n} project${n === 1 ? '' : 's'}. ` : ''}Add it to a project, and grant it to agents, on that project's Integrations page.
          </p>
        ) : null}

        <div class="row">
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
        {confirm && card.libraryId ? (
          <div class="row" mix={errCss}>
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
      </li>
    );
  };
}

const addCss = css({ display: 'grid', gap: t.s6, minWidth: '0' });
const addRow = css({ display: 'flex', alignItems: 'center', gap: t.s2, flexWrap: 'wrap' });

/**
 * Adding an MCP server, prebuilt first: the servers built into Drydock (the connector catalog, mcplib.gallery, as the same
 * cards the Integrations page shows; one already connected says Connected), then a custom server (the server form) or the
 * servers another app's claude_desktop_config.json or mcp.json lists. The Library's "Connect an MCP server" dialog.
 */
export function AddMcpServer(
  handle: Handle<{
    existingIds: readonly string[];
    /** A built-in server was connected, signed in or disconnected from its card. */
    onChanged: () => void;
    /** A custom server was saved. */
    onSaved: (saved: McpLibraryServer) => void;
    /** Servers were imported from a file. */
    onImported: (n: number) => void;
  }>,
) {
  const s = services(handle);
  const gallery = query(handle, { load: () => s.client.mcpGallery(), when: (e) => e.type.startsWith('mcp.server') });
  let custom: '' | 'form' | 'import' = '';
  const set = (f: () => void) => {
    f();
    void handle.update();
  };
  const changed = () => {
    gallery.reload();
    handle.props.onChanged();
  };

  return () => {
    const { existingIds, onSaved, onImported } = handle.props;
    const cards = gallery.data ?? [];
    return (
      <div mix={addCss} data-testid="mcp-add">
        <Section
          label="MCP servers built into Drydock"
          testId="mcp-add-prebuilt"
          title="Built into Drydock"
          description="Servers Drydock knows, each checked against its vendor's documentation. Connect one here: it is connected once for the whole workspace."
        >
          <ErrorNote error={gallery.error} what="Could not load the built-in servers" />
          {gallery.loading && !gallery.data ? <Loading label="Loading the built-in servers..." /> : null}
          {gallery.data && cards.length === 0 ? <p mix={faint}>No server is built into this build.</p> : null}
          <ul mix={connectorGrid} aria-label="MCP servers built into Drydock">
            {cards.map((c) => (
              <ConnectorCard key={c.id} card={c} onChanged={changed} />
            ))}
          </ul>
        </Section>
        <Section
          label="Add a custom MCP server"
          testId="mcp-add-custom"
          title="Add a custom MCP server"
          description="A server that is not built in: a web address or a program on this computer, or the servers another app's configuration file lists."
        >
          {custom === 'form' ? (
            <McpServerForm onCancel={() => set(() => (custom = ''))} onDone={(saved) => (set(() => (custom = '')), onSaved(saved))} />
          ) : custom === 'import' ? (
            <ImportServers existingIds={existingIds} onCancel={() => set(() => (custom = ''))} onDone={(n) => (set(() => (custom = '')), onImported(n))} />
          ) : (
            <div mix={addRow}>
              <Button size="sm" variant="primary" icon={Plus} mix={[on('click', () => set(() => (custom = 'form')))]}>
                Add a custom server
              </Button>
              <Button size="sm" variant="ghost" icon={FileUp} mix={[on('click', () => set(() => (custom = 'import')))]}>
                Import from a file
              </Button>
              <span mix={faint}>Claude Desktop's claude_desktop_config.json, or the mcp.json of Cursor or VS Code.</span>
            </div>
          )}
        </Section>
      </div>
    );
  };
}
