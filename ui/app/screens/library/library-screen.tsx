// The Library: team members (agent templates) defined once for the whole workspace, and the prebuilt agents. Nothing of a
// single project is shown here (decision P51): a team member is made from a template on a project's Team page. The MCP servers
// you add yourself moved to the Integrations page, Your servers (decision P98). The skill library has a page of its own
// (screens/skills/skill-library-screen.tsx, /library/skills).

import { css, on, type Handle } from 'remix/component';
import { Cpu, Library, Pencil, Plug, Plus, Trash2, Users } from 'lucide';
import type { AgentTemplate } from '../../api/libraryClient.ts';
import { AgentGallery } from '../team/agent-gallery.tsx';
import type { McpLibraryServer } from '../../api/mcpClient.ts';
import type { AdapterInfo } from '../../api/types.ts';
import { skipsApprovals } from '../../lib/permission.ts';
import { routes } from '../../routes.ts';
import { action, query, services, watch } from '../../state/services.tsx';
import { Button, buttonStyle } from '../../ui/button.tsx';
import { Avatar, Badge, faint, muted } from '../../ui/data.tsx';
import { Dialog } from '../../ui/dialog.tsx';
import { ErrorNote, Loading, Notice } from '../../ui/feedback.tsx';
import { Icon } from '../../ui/icon.tsx';
import { card, Page, Section } from '../../ui/layout.tsx';
import { emptyLine, Tile, tileGrid } from '../../ui/tiles.tsx';
import { t } from '../../ui/tokens.ts';
import { actionsRow, cardHead, confirmCss } from '../mcp/mcp-screen.tsx';
import { SkipsBadge } from '../shared/skip-approvals.tsx';
import { TemplateForm } from '../team/template-form.tsx';
import { templateBlurb, templateState } from './tiles.ts';

const sectionsCss = css({ display: 'grid', gap: t.s6, minWidth: '0' });
const noteCss = css({ '&:empty': { display: 'none' } });

// The selected entry's panel below its grid.
const panelCard = css({ display: 'grid', gap: t.s3, '& p': { margin: '0' } });
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
            A project gets a team member from this template on its <a href={routes.team.href()}>Crew</a> page.
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
  let newTemplate = false;
  // The selected template tile ('' for none); its panel shows below the grid. Clicking the selected tile again closes it.
  let template = '';
  const set = (f: () => void) => {
    f();
    void handle.update();
  };
  const reload = () => {
    servers.reload();
    templates.reload();
  };

  return () => {
    const libServers = servers.data ?? [];
    const tpls = templates.data ?? [];
    const adapterList = adapters.data ?? [];
    const libraryIds = new Set(libServers.map((x) => x.id));
    const openTemplate = tpls.find((x) => x.id === template);
    return (
      <Page
        title="Library"
        icon={Library}
        wide
        guide="crew"
        subtitle={templates.data ? `${tpls.length} template${tpls.length === 1 ? '' : 's'}` : undefined}
        actions={
          <a href={routes.integrations.href()} mix={buttonStyle('ghost', 'sm')} data-testid="library-servers-link">
            <Icon icon={Plug} size={14} />
            MCP servers are in Integrations
          </a>
        }
      >
        <div mix={sectionsCss}>
          <div role="status" aria-live="polite" data-testid="library-note" mix={noteCss}>
            {note ? <Notice tone="ok">{note}</Notice> : null}
          </div>
          <ErrorNote error={servers.error ?? templates.error} what="Library" />
          <Section
            title="Agent templates"
            description="A team member defined once. Applying it on a project's Crew page makes an ordinary agent in that project."
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
                <strong>No templates yet.</strong> Make one here, or save an agent as a template on a project's <a href={routes.team.href()}>Crew</a> page.
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
                Built into Drydock, with their skills and connectors pinned. Add them to a project on its <a href={routes.team.href()}>Crew</a> page or when you create a project.
              </>
            }
            testId="library-prebuilt"
          >
            <ErrorNote error={prebuilt.error} what="Could not load the prebuilt agents" />
            {prebuilt.loading && !prebuilt.data ? <Loading /> : null}
            <AgentGallery entries={prebuilt.data ?? []} label="Prebuilt agents" />
          </Section>
        </div>

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
