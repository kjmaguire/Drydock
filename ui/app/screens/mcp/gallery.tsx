// The MCP connector cards (decision P43), the way other apps present connectors: a card per known server with its logo, name,
// publisher and what it gives agents; one Connect button (the browser sign-in, a token, or the program a template runs); then
// Connected, as whom, with the tools it offers, and Add to project, Grant to agents and Disconnect. Connecting needs no project.
// Since decision P47 the cards sit on the Integrations page, in the Agent tools section of their vendor's tile; below the tiles,
// Your own servers shows the servers you described yourself, by what each says about itself.

import { css, on, type Handle } from 'remix/component';
import { BookOpen, ExternalLink, FileUp, FolderPlus, Plug, Plus, RefreshCw, Unplug, Users } from 'lucide';
import type { McpGalleryCard, McpLibraryServer, McpServer, McpServerIdentity } from '../../api/mcpClient.ts';
import type { Agent, Project } from '../../api/types.ts';
import { routes } from '../../routes.ts';
import { action, query, services, watch } from '../../state/services.tsx';
import { BrandLogo } from '../../ui/brand-logo.tsx';
import { Button, buttonStyle } from '../../ui/button.tsx';
import { Badge, faint, mono, muted } from '../../ui/data.tsx';
import { Dialog } from '../../ui/dialog.tsx';
import { ErrorNote, Loading, Notice } from '../../ui/feedback.tsx';
import { Icon } from '../../ui/icon.tsx';
import { safeHref } from '../../ui/markdown.tsx';
import { Section } from '../../ui/layout.tsx';
import { t, type Tone } from '../../ui/tokens.ts';
import { ImportServers } from './import-panel.tsx';
import { OAuthPanel } from './oauth-panel.tsx';
import { McpServerForm } from './server-form.tsx';

/** The grid the connector cards and the own-server cards sit in. */
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
  '& .sum': { color: t.textMuted, fontSize: t.textSm, lineHeight: '1.45' },
  '& .meta': { display: 'flex', alignItems: 'center', gap: '6px', flexWrap: 'wrap', fontSize: t.textXs, color: t.textMuted },
  '& .row': { display: 'flex', alignItems: 'center', gap: t.s2, flexWrap: 'wrap' },
  '& details summary': { cursor: 'pointer', fontSize: t.textSm, color: t.textMuted },
  '& details ul': { margin: '6px 0 0', paddingLeft: '18px', fontSize: t.textSm },
  '& code.cmd': { display: 'block', padding: '4px 8px', borderRadius: t.rMd, background: t.bgApp, border: `1px solid ${t.border}`, overflowWrap: 'anywhere' },
  '& select': { width: 'auto', minWidth: '120px', maxWidth: '100%' },
});
const errCss = css({ padding: '6px 10px', borderRadius: t.rMd, background: t.dangerSoft, color: t.text, fontSize: t.textSm, overflowWrap: 'anywhere' });
const tokenCss = css({ display: 'grid', gap: '6px', '& label': { fontSize: t.textSm, fontWeight: '500' }, '& .hint': { fontSize: t.textXs, color: t.textMuted } });
const grantCss = css({ display: 'grid', gap: '4px', margin: '0', padding: '0', listStyle: 'none', '& label': { display: 'flex', alignItems: 'center', gap: t.s2, cursor: 'pointer', fontSize: t.textSm } });
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
        <input id={id} type="password" autocomplete="off" spellcheck={false} required={!card.authOptional} value={value} mix={on('input', (e) => void (value = (e.currentTarget as HTMLInputElement).value))} />
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

function GrantAgents(handle: Handle<{ card: McpGalleryCard; projectId: string; agents: readonly Agent[] }>) {
  const s = services(handle);
  const act = action(handle);
  const view = query(handle, {
    load: () => s.client.mcpServerList(handle.props.projectId),
    key: () => handle.props.projectId,
    when: (e) => e.type.startsWith('mcp.'),
  });
  const want = new Map<string, boolean>();
  return () => {
    const { card, projectId, agents } = handle.props;
    const mine: McpServer | undefined = (view.data ?? []).find((x) => x.id === card.libraryId);
    const has = (a: string) => !!mine?.agents.some((g) => g.agentId === a);
    for (const [a, v] of want) if (has(a) === v) want.delete(a); // the server agrees now
    if (agents.length === 0) return <p mix={faint}>This project has no agents yet.</p>;
    return (
      <div>
        <ErrorNote error={act.error ?? view.error} what="Not granted" />
        <ul mix={grantCss} aria-label={`Agents allowed to use ${card.display}`}>
          {agents.map((a) => (
            <li key={a.id}>
              <label>
                <input
                  type="checkbox"
                  aria-label={`${a.name} may use ${card.display}`}
                  checked={want.get(a.id) ?? has(a.id)}
                  disabled={act.busy !== ''}
                  mix={on('change', async (e) => {
                    const yes = (e.currentTarget as HTMLInputElement).checked;
                    want.set(a.id, yes); // shown at once; the server's answer replaces it
                    const ok = await act.run('grant', () => s.client.mcpServerGrant(projectId, card.libraryId!, a.id, yes));
                    if (!ok) want.delete(a.id);
                    view.reload();
                  })}
                />
                {a.name} {a.role ? <span mix={faint}>{a.role}</span> : null}
              </label>
            </li>
          ))}
        </ul>
      </div>
    );
  };
}

export function ConnectorCard(handle: Handle<{ card: McpGalleryCard; projects: readonly Project[]; projectId?: string; agents: readonly Agent[]; onChanged: () => void }>) {
  const s = services(handle);
  watch(handle, s.workspace);
  const act = action(handle);
  let asking = false; // the token form is open
  let confirm = false;
  let granting = false;
  let pick = '';
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
    const { card, projects, projectId, agents, onChanged } = handle.props;
    const ro = s.workspace.license;
    const st = STATUS[card.status] ?? STATUS.error!;
    const connected = card.status === 'connected';
    const needsSecret = card.auth === 'env' || (card.auth === 'bearer' && !card.authOptional);
    const docs = card.docsUrl ? safeHref(card.docsUrl) : undefined;
    const inProjects = card.projects.map((id) => projects.find((p) => p.id === id)?.name ?? id);
    const options = projects.filter((p) => !card.projects.includes(p.id));
    const chosen = options.some((p) => p.id === pick) ? pick : (options[0]?.id ?? '');
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
                  {tool.description ? <div mix={faint}>{tool.description}</div> : null}
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
        {card.libraryId && card.projects.length > 0 ? <p mix={faint}>In: {inProjects.join(', ')}.</p> : null}

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
          {card.libraryId && options.length > 0 ? (
            <span class="row">
              <select aria-label={`Project to add ${card.display} to`} value={chosen} mix={on('change', (e) => set(() => (pick = (e.currentTarget as HTMLSelectElement).value)))}>
                {options.map((p) => (
                  <option key={p.id} value={p.id} selected={p.id === chosen}>
                    {p.name}
                  </option>
                ))}
              </select>
              <Button
                size="sm"
                icon={FolderPlus}
                disabled={act.busy !== '' || ro.readOnly}
                mix={[
                  on('click', async () => {
                    await act.run('add', () => s.client.mcpServerAdd(chosen, card.libraryId!));
                    onChanged();
                  }),
                ]}
              >
                Add to project
              </Button>
            </span>
          ) : null}
          {card.libraryId && projectId ? (
            <Button size="sm" variant="ghost" icon={Users} aria-expanded={granting} mix={[on('click', () => set(() => (granting = !granting)))]}>
              Grant to agents
            </Button>
          ) : null}
          {card.libraryId && !confirm ? (
            <Button size="sm" variant="ghost" icon={Unplug} mix={[on('click', () => set(() => (confirm = true)))]}>
              Disconnect
            </Button>
          ) : null}
        </div>
        {granting && card.libraryId && projectId ? <GrantAgents card={card} projectId={projectId} agents={agents} /> : null}
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

/** A server the human described, shown by what it says about itself. */
function OwnServerCard(handle: Handle<{ server: McpLibraryServer; onChanged: () => void }>) {
  const s = services(handle);
  const act = action(handle);
  return () => {
    const { server: x, onChanged } = handle.props;
    const id = identityLine(x.serverInfo);
    const status = x.lastError ? STATUS.error! : x.discoveredAt > 0 ? STATUS.connected! : { label: 'Not reached yet', tone: 'muted' as Tone };
    return (
      <li mix={cardCss} data-testid="own-server" data-id={x.id} data-status={x.lastError ? 'error' : x.discoveredAt > 0 ? 'connected' : 'not_connected'} aria-label={x.display}>
        <div class="head">
          <div class="who">
            <h4 title={x.display}>{x.display}</h4>
            <span class="pub">
              <code>{x.id}</code> · {x.transport === 'http' ? 'web address' : 'program'}
            </span>
          </div>
          <Badge tone={status.tone}>{status.label}</Badge>
        </div>
        <p class="sum" data-testid="own-server-identity">
          {id ? (
            <>
              Identifies itself as <strong>{id}</strong>
            </>
          ) : (
            'Its identity is learned when Drydock first reaches it.'
          )}
        </p>
        {x.serverInfo?.description ? <p mix={faint}>{x.serverInfo.description}</p> : null}
        <ErrorNote error={act.error} what="Not reached" />
        {x.lastError ? (
          <p mix={errCss} role="alert">
            {x.lastError}
          </p>
        ) : null}
        {x.tools.length > 0 ? (
          <details>
            <summary>
              {x.tools.length} {x.tools.length === 1 ? 'tool' : 'tools'}
            </summary>
            <ul aria-label={`Tools of ${x.display}`}>
              {x.tools.map((tool) => (
                <li key={tool.name}>
                  <code>{tool.name}</code> {x.readOnlyTools.includes(tool.name) ? <span mix={faint}>reads only</span> : <span mix={faint}>asks you first</span>}
                </li>
              ))}
            </ul>
          </details>
        ) : null}
        {x.serverInfo?.instructions ? (
          <details>
            <summary>What the server tells clients</summary>
            <p mix={instrCss}>{x.serverInfo.instructions}</p>
          </details>
        ) : null}
        <div class="row">
          <Button
            size="sm"
            icon={RefreshCw}
            disabled={act.busy !== ''}
            mix={[
              on('click', async () => {
                await act.run('discover', () => s.client.mcpLibDiscover(x.id));
                onChanged();
              }),
            ]}
          >
            {act.busy === 'discover' ? 'Connecting...' : 'Test connection'}
          </Button>
          <a href={routes.library.href()} mix={buttonStyle('ghost', 'sm')}>
            Edit in the Library
          </a>
        </div>
      </li>
    );
  };
}

/**
 * The servers the human described (not from the catalog), by what each says about itself, with Add your own server and Import
 * from a file. `linked` are the library ids the catalog cards already show, so a server is on one card only.
 */
export function OwnServers(handle: Handle<{ linked: readonly (string | undefined)[]; onChanged: () => void }>) {
  const s = services(handle);
  const lib = query(handle, { load: () => s.client.mcpLibList(), when: (e) => e.type.startsWith('mcp.server') });
  let adding = false;
  let importing = false;
  let note = '';
  const set = (f: () => void) => {
    f();
    void handle.update();
  };
  const reload = () => {
    lib.reload();
    handle.props.onChanged();
  };

  return () => {
    const linked = new Set(handle.props.linked.filter(Boolean));
    const own = (lib.data ?? []).filter((x) => !x.catalogId && !linked.has(x.id));
    return (
      <>
        <Section
          label="Your own servers"
          testId="own-servers"
          title="Your own servers"
          description="A server that is not in the gallery: a web address or a program on this computer. Drydock shows it by what the server says about itself."
          actions={
            <>
              <Button size="sm" variant="ghost" icon={FileUp} mix={[on('click', () => set(() => (importing = true)))]}>
                Import from a file
              </Button>
              <Button size="sm" variant="primary" icon={Plus} mix={[on('click', () => set(() => (adding = true)))]}>
                Add your own server
              </Button>
            </>
          }
        >
          <div role="status" aria-live="polite">
            {note ? <Notice tone="ok">{note}</Notice> : null}
          </div>
          <ErrorNote error={lib.error} what="Could not load your servers" />
          {lib.loading && !lib.data ? <Loading label="Loading your servers..." /> : null}
          {lib.data && own.length === 0 ? (
            <p mix={faint}>
              <Icon icon={BookOpen} size={12} /> None yet. Add one by its address or command, or import the mcp.json or claude_desktop_config.json another app uses.
            </p>
          ) : null}
          <ul mix={connectorGrid}>
            {own.map((x) => (
              <OwnServerCard key={x.id} server={x} onChanged={reload} />
            ))}
          </ul>
        </Section>
        <Dialog open={adding} title="Add your own server" wide onClose={() => set(() => (adding = false))}>
          {adding ? (
            <McpServerForm
              onCancel={() => set(() => (adding = false))}
              onDone={async (saved) => {
                set(() => {
                  adding = false;
                  note = `${saved.display} was added. Drydock is reaching it to learn who it is and what it offers...`;
                });
                try {
                  const r = await s.client.mcpLibDiscover(saved.id);
                  set(() => (note = `${saved.display} identifies itself as ${identityLine(r.serverInfo) || 'nothing'} and offers ${r.tools.length} tools.`));
                } catch (e) {
                  set(() => (note = `${saved.display} was added, but Drydock could not reach it: ${(e as Error).message}`));
                }
                reload();
              }}
            />
          ) : null}
        </Dialog>
        <Dialog open={importing} title="Import MCP servers from a file" wide onClose={() => set(() => (importing = false))}>
          {importing ? (
            <ImportServers
              existingIds={(lib.data ?? []).map((x) => x.id)}
              onCancel={() => set(() => (importing = false))}
              onDone={(n) => {
                set(() => {
                  importing = false;
                  note = `${n} server${n === 1 ? '' : 's'} imported.`;
                });
                reload();
              }}
            />
          ) : null}
        </Dialog>
      </>
    );
  };
}
