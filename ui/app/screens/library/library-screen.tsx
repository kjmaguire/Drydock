// The Library: MCP servers and team members (agent templates) defined once for the whole workspace, and the prebuilt agents.
// Nothing of a single project is shown here (decision P51): a connected server is added to a project, and granted to that
// project's agents, on the project's Integrations page; a team member is made from a template on a project's Team page. Connect
// an MCP server lists the servers built into Drydock first, then a custom server. The skill library has a page of its own
// (screens/skills/skill-library-screen.tsx, /library/skills).

import { css, on, type Handle } from 'remix/component';
import { Cpu, FolderOpen, Globe, KeyRound, Library, Pencil, Plug, Plus, RefreshCw, Search, Trash2, Unplug, Users } from 'lucide';
import type { AgentTemplate } from '../../api/libraryClient.ts';
import { AgentGallery } from '../team/agent-gallery.tsx';
import type { McpLibraryServer } from '../../api/mcpClient.ts';
import type { AdapterInfo } from '../../api/types.ts';
import { skipsApprovals } from '../../lib/permission.ts';
import { routes } from '../../routes.ts';
import { action, query, services, watch } from '../../state/services.tsx';
import { Button, buttonStyle } from '../../ui/button.tsx';
import { Avatar, Badge, faint, mono, muted } from '../../ui/data.tsx';
import { Dialog } from '../../ui/dialog.tsx';
import { ErrorNote, Loading, Notice } from '../../ui/feedback.tsx';
import { Icon } from '../../ui/icon.tsx';
import { card, Page, Section } from '../../ui/layout.tsx';
import { emptyLine, Tile, tileGrid } from '../../ui/tiles.tsx';
import { ClippedText } from '../../ui/clipped-text.tsx';
import { t } from '../../ui/tokens.ts';
import { ReachNote } from '../mcp/command-line.tsx';
import { AddMcpServer } from '../mcp/gallery.tsx';
import { importedNote } from '../mcp/import.ts';
import { actionsRow, cardHead, confirmCss, serverLogo, ServerMark, subhead, whereCss } from '../mcp/mcp-screen.tsx';
import { OAuthPanel } from '../mcp/oauth-panel.tsx';
import { McpServerForm } from '../mcp/server-form.tsx';
import { inputOf, whereOf } from '../mcp/server-input.ts';
import { SkipsBadge } from '../shared/skip-approvals.tsx';
import { TemplateForm } from '../team/template-form.tsx';
import { serverBlurb, serverState, templateBlurb, templateState } from './tiles.ts';
import { plural } from '../../lib/format.ts';

type Say = (message: string) => void;
type Fail = (e: Error | undefined) => void;

const sectionsCss = css({ display: 'grid', gap: t.s6, minWidth: '0' });
const noteCss = css({ '&:empty': { display: 'none' } });

// The selected entry's panel below its grid.
const panelCard = css({ display: 'grid', gap: t.s3, '& p': { margin: '0' } });
const metaRow = css({ display: 'flex', alignItems: 'center', gap: t.s2, flexWrap: 'wrap', fontSize: t.textSm, color: t.textMuted, '& svg': { color: t.textFaint } });
const toolsTable = css({
  width: '100%',
  borderCollapse: 'collapse',
  fontSize: t.textMd,
  '& th': { textAlign: 'left', fontSize: t.textXs, fontWeight: '600', color: t.textMuted, padding: '6px 10px', borderBottom: `1px solid ${t.border}` },
  '& td': { padding: '8px 10px', borderBottom: `1px solid ${t.border}`, verticalAlign: 'top' },
  '& tr:last-child td': { borderBottom: '0' },
  '& td > div': { color: t.textMuted, fontSize: t.textSm, marginTop: '2px' },
  '& label': { display: 'inline-flex', alignItems: 'center', gap: t.s2, cursor: 'pointer' },
});
const tableWrap = css({ border: `1px solid ${t.border}`, borderRadius: t.rMd, overflow: 'hidden' });
const chips = css({
  display: 'flex',
  flexWrap: 'wrap',
  gap: '6px',
  margin: '0',
  padding: '0',
  listStyle: 'none',
  '& > li': {
    display: 'inline-flex',
    alignItems: 'center',
    gap: '4px',
    height: '24px',
    padding: '0 8px',
    borderRadius: t.rFull,
    border: `1px solid ${t.border}`,
    background: t.bgApp,
    fontSize: t.textSm,
  },
});
function ServerTile(handle: Handle<{ s: McpLibraryServer; selected: boolean; onSelect: () => void }>) {
  return () => {
    const { s, selected, onSelect } = handle.props;
    const st = serverState(s);
    return (
      <Tile
        title={s.display}
        logo={serverLogo(s) || undefined}
        icon={s.transport === 'http' ? Globe : Plug}
        blurb={serverBlurb(s)}
        state={st.text}
        tone={st.tone}
        selected={selected}
        onSelect={onSelect}
        testId="lib-mcp-tile"
        data={{ server: s.id }}
      />
    );
  };
}

function TemplateTile(handle: Handle<{ t: AgentTemplate; libraryIds: ReadonlySet<string>; selected: boolean; onSelect: () => void }>) {
  return () => {
    const { t: tp, libraryIds, selected, onSelect } = handle.props;
    const st = templateState(tp, libraryIds);
    return (
      <Tile
        title={tp.name}
        logo={tp.adapter}
        icon={Users}
        blurb={templateBlurb(tp)}
        state={st.text}
        tone={st.tone}
        badge={skipsApprovals(tp.permissionMode) ? { text: 'skips approvals', tone: 'err' } : undefined}
        selected={selected}
        onSelect={onSelect}
        testId="lib-template-tile"
        data={{ template: tp.name }}
      />
    );
  };
}

/** The selected server's full panel: where it runs, its secrets, its sign-in, its tools, and what can be done with it. */
function McpPanel(handle: Handle<{ s: McpLibraryServer; say: Say; fail: Fail; onChanged: () => void }>) {
  const svc = services(handle);
  watch(handle, svc.workspace);
  const act = action(handle);
  let editing = false;
  let confirm = false;
  // the note beside the button says which program Find tools starts (M24): the button is described by it
  const reachId = `reach-${handle.id}`;
  const set = (f: () => void) => {
    f();
    void handle.update();
  };

  const setReadOnly = async (tool: string, ticked: boolean) => {
    const { s, onChanged } = handle.props;
    const next = new Set(s.readOnlyTools);
    if (ticked) next.add(tool);
    else next.delete(tool);
    await act.run('readonly', () => svc.client.mcpLibSave(inputOf(s, { readOnlyTools: [...next] })));
    onChanged();
  };

  return () => {
    const { s, say, fail, onChanged } = handle.props;
    const ro = svc.workspace.license;
    const n = s.projects.length;
    return (
      <>
        <article mix={[card, panelCard]} data-testid="lib-mcp" data-server={s.id} aria-label={s.display}>
          <header mix={cardHead}>
            <ServerMark s={s} />
            <h3>{s.display}</h3>
            <code mix={faint}>{s.id}</code>
            <Badge>{s.transport === 'http' ? 'web address' : 'program'}</Badge>
            {s.guard ? (
              <Badge tone="info" title="Agents cannot send code, files, secrets or long text to it">
                send guard on
              </Badge>
            ) : (
              <Badge tone="warn">send guard off</Badge>
            )}
          </header>
          <ErrorNote error={act.error} what="Not changed" />
          <code mix={[mono, whereCss]}>{whereOf(s)}</code>
          {s.oauth ? <OAuthPanel server={s} onChanged={onChanged} /> : null}
          {s.secretEnv.length > 0 || s.hasToken ? (
            <p mix={metaRow} data-testid="mcp-secrets">
              <Icon icon={KeyRound} size={14} />
              Secrets kept in the credential store: {s.hasToken ? 'bearer token' : s.secretEnv.join(', ')} (values are never shown)
            </p>
          ) : null}
          <p mix={metaRow} data-testid="lib-mcp-projects">
            <Icon icon={FolderOpen} size={14} />
            {n === 0 ? 'Not in any project yet.' : `In ${n} project${n === 1 ? '' : 's'}.`} Add it to a project, and choose which of its agents may use it, on that project's
            Integrations page.
          </p>
          <h4 mix={subhead}>Tools</h4>
          {s.tools.length === 0 ? (
            <p mix={muted}>Tools are not known yet. Press Find tools to start the server and list them.</p>
          ) : (
            <div mix={tableWrap}>
              <table mix={toolsTable} aria-label={`Tools of ${s.display}`}>
                <thead>
                  <tr>
                    <th scope="col">Tool</th>
                    <th scope="col">Reads only (no approval)</th>
                  </tr>
                </thead>
                <tbody>
                  {s.tools.map((tool) => (
                    <tr key={tool.name} data-testid="mcp-tool" data-tool={tool.name}>
                      <td>
                        <code>{tool.name}</code>
                        {tool.flags?.length ? (
                          <Badge tone="warn" title={`Instruction-like text found: ${tool.flags.join(', ')}`} testId="mcp-tool-flagged">
                            held back
                          </Badge>
                        ) : null}
                        {tool.flags?.length ? (
                          <div mix={faint}>
                            Its description reads like instructions to the agent ({tool.flags.join(', ')}). Drydock does not grant it to agents; the description is
                            shown here as data.
                          </div>
                        ) : null}
                        {tool.description ? <ClippedText text={tool.description} data-testid="mcp-tool-description" /> : null}
                      </td>
                      <td>
                        <label>
                          <input
                            type="checkbox"
                            aria-label={`${tool.name} only reads`}
                            checked={s.readOnlyTools.includes(tool.name)}
                            disabled={act.busy !== '' || ro.readOnly}
                            mix={on('change', (e) => void setReadOnly(tool.name, e.currentTarget.checked))}
                          />
                          {tool.readOnlyHint ? <span mix={faint}>the server says it only reads</span> : null}
                        </label>
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}
          <p mix={faint}>Every tool you do not tick needs your approval for each call. Ticking is your statement that the tool cannot change anything.</p>
          <ReachNote server={s} lead={`Pressing ${s.tools.length > 0 ? 'Test and refresh tools' : 'Find tools'}`} id={reachId} />
          <div mix={actionsRow}>
            <Button
              size="sm"
              icon={s.tools.length > 0 ? RefreshCw : Search}
              disabled={act.busy !== ''}
              aria-describedby={reachId}
              mix={[
                on('click', async () => {
                  const r = await act.run('discover', () => svc.client.mcpLibDiscover(s.id));
                  if (r) onChanged();
                }),
              ]}
            >
              {act.busy === 'discover' ? 'Connecting...' : s.tools.length > 0 ? 'Test and refresh tools' : 'Find tools'}
            </Button>
            <Button size="sm" variant="ghost" icon={Pencil} aria-expanded={editing} mix={[on('click', () => set(() => (editing = !editing)))]}>
              Edit
            </Button>
            {confirm ? null : (
              <Button size="sm" variant="ghost" icon={Unplug} mix={[on('click', () => set(() => (confirm = true)))]}>
                Disconnect
              </Button>
            )}
          </div>
          {confirm ? (
            <div mix={confirmCss}>
              <span>
                Disconnect {s.display}? It leaves {plural(n, 'project')} and its stored secrets are deleted.
              </span>
              <Button
                size="sm"
                variant="danger"
                disabled={act.busy !== ''}
                mix={[
                  on('click', async () => {
                    const done = await act.run('delete', async () => {
                      await svc.client.mcpLibDelete(s.id);
                      return true;
                    });
                    if (done) {
                      fail(undefined);
                      say(`${s.display} was disconnected.`);
                      onChanged();
                    }
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
        </article>
        <Dialog open={editing} title={`Edit ${s.display}`} wide onClose={() => set(() => (editing = false))}>
          {editing ? (
            <McpServerForm
              initial={s}
              onCancel={() => set(() => (editing = false))}
              onDone={() => {
                set(() => (editing = false));
                onChanged();
              }}
            />
          ) : null}
        </Dialog>
      </>
    );
  };
}

/** The selected template's full panel: its purpose, the servers and skills it gives, and what can be done with it. */
function TemplatePanel(handle: Handle<{ t: AgentTemplate; servers: readonly McpLibraryServer[]; adapters: readonly AdapterInfo[]; onChanged: () => void }>) {
  const svc = services(handle);
  const act = action(handle);
  let editing = false;
  let confirm = false;
  const set = (f: () => void) => {
    f();
    void handle.update();
  };

  return () => {
    const { t: tp, servers, adapters, onChanged } = handle.props;
    const nameOf = (id: string) => servers.find((x) => x.id === id)?.display ?? id;
    return (
      <>
        <article mix={[card, panelCard]} data-testid="lib-template" data-template={tp.name} aria-label={tp.name}>
          <header mix={cardHead}>
            <Avatar id={tp.id} name={tp.name} size={30} kind="agent" />
            <h3>{tp.name}</h3>
            {tp.role ? <span mix={muted}>{tp.role}</span> : null}
            <code mix={faint}>{tp.adapter}</code>
            {tp.model ? <Badge>model {tp.model}</Badge> : null}
            {tp.effort ? <Badge>effort {tp.effort}</Badge> : null}
            <SkipsBadge mode={tp.permissionMode} />
          </header>
          <ErrorNote error={act.error} what="Not changed" />
          {tp.purpose ? <p>{tp.purpose.length > 200 ? `${tp.purpose.slice(0, 200)}...` : tp.purpose}</p> : <p mix={muted}>No purpose written.</p>}
          {tp.mcpServers.length > 0 ? (
            <ul mix={chips} aria-label="MCP servers it gets">
              {tp.mcpServers.map((id) => (
                <li key={id}>
                  <Icon icon={Plug} size={12} />
                  {nameOf(id)}
                </li>
              ))}
            </ul>
          ) : null}
          {tp.skills.length > 0 ? (
            <ul mix={chips} aria-label="Skills it brings">
              {tp.skills.map((k) => (
                <li key={k.name} title={`${k.registry}${k.ref ? ` at ${k.ref.slice(0, 12)}` : ''}`}>
                  <Icon icon={Cpu} size={12} />
                  {k.name}
                </li>
              ))}
            </ul>
          ) : null}
          <p mix={faint} data-testid="lib-template-where">
            A project gets a team member from this template on its <a href={routes.team.href()}>Team</a> page.
          </p>
          <div mix={actionsRow}>
            <Button size="sm" variant="ghost" icon={Pencil} aria-expanded={editing} mix={[on('click', () => set(() => (editing = !editing)))]}>
              Edit
            </Button>
            {confirm ? null : (
              <Button size="sm" variant="ghost" icon={Trash2} mix={[on('click', () => set(() => (confirm = true)))]}>
                Delete
              </Button>
            )}
          </div>
          {confirm ? (
            <div mix={confirmCss}>
              <span>Delete the template {tp.name}? Agents already made from it stay.</span>
              <Button
                size="sm"
                variant="danger"
                disabled={act.busy !== ''}
                mix={[
                  on('click', async () => {
                    const done = await act.run('delete', async () => {
                      await svc.client.agentTemplateDelete(tp.id);
                      return true;
                    });
                    if (done) onChanged();
                  }),
                ]}
              >
                Yes, delete
              </Button>
              <Button size="sm" mix={[on('click', () => set(() => (confirm = false)))]}>
                Keep
              </Button>
            </div>
          ) : null}
        </article>
        <Dialog open={editing} title={`Edit the template ${tp.name}`} wide onClose={() => set(() => (editing = false))}>
          {editing ? (
            <TemplateForm
              initial={tp}
              adapters={adapters}
              servers={servers}
              onCancel={() => set(() => (editing = false))}
              onDone={() => {
                set(() => (editing = false));
                onChanged();
              }}
            />
          ) : null}
        </Dialog>
      </>
    );
  };
}

export function LibraryScreen(handle: Handle) {
  const s = services(handle);
  watch(handle, s.workspace);
  const adapters = query(handle, { load: () => s.client.adapterList() });
  const servers = query(handle, { load: () => s.client.mcpLibList(), when: (e) => e.type.startsWith('mcp.server') });
  const templates = query(handle, { load: () => s.client.agentTemplateList(), when: (e) => e.type.startsWith('agenttemplate.') });
  const prebuilt = query(handle, { load: () => s.client.agentCatalogList() });
  let note = '';
  let error: Error | undefined;
  let connecting = false;
  let newTemplate = false;
  // The selected tile of each grid ('' for none); its panel shows below that grid. Clicking the selected tile again closes it.
  let server = '';
  let template = '';
  const set = (f: () => void) => {
    f();
    void handle.update();
  };
  const say: Say = (m) => set(() => (note = m));
  const fail: Fail = (e) => set(() => (error = e));
  const reload = () => {
    servers.reload();
    templates.reload();
  };

  return () => {
    const libServers = servers.data ?? [];
    const tpls = templates.data ?? [];
    const adapterList = adapters.data ?? [];
    const libraryIds = new Set(libServers.map((x) => x.id));
    const openServer = libServers.find((x) => x.id === server);
    const openTemplate = tpls.find((x) => x.id === template);
    return (
      <Page
        title="Library"
        icon={Library}
        wide
        guide="integrations"
        actions={
          <a href={routes.librarySkills.href()} mix={buttonStyle('ghost', 'sm')} data-testid="library-skills-link">
            Skills
          </a>
        }
        subtitle={servers.data && templates.data ? `${libServers.length} server${libServers.length === 1 ? '' : 's'} · ${tpls.length} template${tpls.length === 1 ? '' : 's'}` : undefined}
      >
        <div mix={sectionsCss}>
          <div role="status" aria-live="polite" data-testid="library-note" mix={noteCss}>
            {note ? <Notice tone="ok">{note}</Notice> : null}
          </div>
          <ErrorNote error={error ?? servers.error ?? templates.error} what="Library" />
          <Section
            title="MCP servers"
            description="Connected once for the whole workspace: one built into Drydock, or your own. Secrets stay in the Windows credential store."
            actions={
              <Button variant="primary" size="sm" icon={Plus} mix={[on('click', () => set(() => (connecting = true)))]}>
                Connect an MCP server
              </Button>
            }
          >
            {servers.loading && !servers.data ? <Loading /> : null}
            {servers.data && libServers.length === 0 ? (
              <p mix={emptyLine} data-testid="lib-mcp-empty">
                <Icon icon={Plug} size={14} />
                <strong>No servers are connected yet.</strong> Connect one built into Drydock, or a program on this computer or a web address of your own. Then press Find tools.
              </p>
            ) : null}
            {libServers.length > 0 ? (
              <ul mix={tileGrid} aria-label="MCP servers">
                {libServers.map((x) => (
                  <ServerTile key={x.id} s={x} selected={x.id === server} onSelect={() => set(() => (server = server === x.id ? '' : x.id))} />
                ))}
              </ul>
            ) : null}
            {openServer ? <McpPanel key={openServer.id} s={openServer} say={say} fail={fail} onChanged={reload} /> : null}
          </Section>

          <Section
            title="Agent templates"
            description="A team member defined once. Applying it on a project's Team page makes an ordinary agent in that project."
            actions={
              <Button variant="primary" size="sm" icon={Plus} mix={[on('click', () => set(() => (newTemplate = true)))]}>
                New agent template
              </Button>
            }
          >
            {templates.loading && !templates.data ? <Loading /> : null}
            {templates.data && tpls.length === 0 ? (
              <p mix={emptyLine} data-testid="lib-template-empty">
                <Icon icon={Users} size={14} />
                <strong>No templates yet.</strong> Make one here, or save an agent as a template on a project's <a href={routes.team.href()}>Team</a> page.
              </p>
            ) : null}
            {tpls.length > 0 ? (
              <ul mix={tileGrid} aria-label="Agent templates">
                {tpls.map((x) => (
                  <TemplateTile key={x.id} t={x} libraryIds={libraryIds} selected={x.id === template} onSelect={() => set(() => (template = template === x.id ? '' : x.id))} />
                ))}
              </ul>
            ) : null}
            {openTemplate ? <TemplatePanel key={openTemplate.id} t={openTemplate} servers={libServers} adapters={adapterList} onChanged={reload} /> : null}
          </Section>

          <Section
            title="Prebuilt agents"
            description={
              <>
                Built into Drydock, with their skills and connectors pinned. Add them to a project on its <a href={routes.team.href()}>Team</a> page or when you create a project.
              </>
            }
            testId="library-prebuilt"
          >
            <ErrorNote error={prebuilt.error} what="Could not load the prebuilt agents" />
            {prebuilt.loading && !prebuilt.data ? <Loading /> : null}
            <AgentGallery entries={prebuilt.data ?? []} label="Prebuilt agents" />
          </Section>
        </div>

        <Dialog open={connecting} title="Connect an MCP server" wide onClose={() => set(() => (connecting = false))}>
          {connecting ? (
            <AddMcpServer
              existingIds={libServers.map((x) => x.id)}
              onChanged={reload}
              onSaved={(saved) => {
                set(() => {
                  connecting = false;
                  server = saved.id; // open the new server's panel: Find tools is the next step
                  note = `${saved.display} is connected. Press Find tools, then add it to a project on that project's Integrations page.`;
                });
                reload();
              }}
              onImported={(n) => {
                set(() => {
                  connecting = false;
                  note = importedNote(n, 'open its tile below and press Find tools');
                });
                reload();
              }}
            />
          ) : null}
        </Dialog>
        <Dialog open={newTemplate} title="New agent template" wide onClose={() => set(() => (newTemplate = false))}>
          {newTemplate ? (
            <TemplateForm
              adapters={adapterList}
              servers={libServers}
              onCancel={() => set(() => (newTemplate = false))}
              onDone={(saved) => {
                set(() => {
                  newTemplate = false;
                  template = saved.id;
                  note = `The template ${saved.name} was saved.`;
                });
                reload();
              }}
            />
          ) : null}
        </Dialog>
      </Page>
    );
  };
}
