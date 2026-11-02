// Agents (decision P140; once the Library, P51): one page with the tabs My agents (the agent templates: a team member defined once
// for the whole workspace, shown as cards), Starters (the prebuilt agents, read-only; Use saves one as my agent) and Skills (its
// own screen, screens/skills/skill-library-screen.tsx, /library/skills). The tabs are the hub's pills (shell/hubnav.tsx), with
// every project's crew (/crew) set apart at the end. Nothing of a single project is shown here: a team member is made from a
// template on a project's Crew page. The MCP servers you add yourself are in Settings > Integrations (decision P98).

import { css, navigate, on, type Handle } from 'remix/component';
import { Bot, Cpu, Pencil, Plug, Plus, Trash2, Users } from 'lucide';
import type { AgentTemplate } from '../../api/libraryClient.ts';
import { AgentGallery } from '../team/agent-gallery.tsx';
import type { McpLibraryServer } from '../../api/mcpClient.ts';
import type { PrebuiltAgent } from '../../api/types.ts';
import { skipsApprovals } from '../../lib/permission.ts';
import { routes } from '../../routes.ts';
import { action, query, services, watch } from '../../state/services.tsx';
import { Button, buttonStyle } from '../../ui/button.tsx';
import { Avatar, Badge, faint, muted } from '../../ui/data.tsx';
import { Dialog } from '../../ui/dialog.tsx';
import { ErrorNote, Loading, Notice } from '../../ui/feedback.tsx';
import { Icon } from '../../ui/icon.tsx';
import { card, Page } from '../../ui/layout.tsx';
import { MoreMenu } from '../../ui/menu.tsx';
import { emptyLine } from '../../ui/tiles.tsx';
import { t } from '../../ui/tokens.ts';
import { actionsRow, cardHead } from '../mcp/mcp-screen.tsx';
import { SkipsBadge } from '../shared/skip-approvals.tsx';
import { TemplateForm } from '../team/template-form.tsx';
import { modelChip, purposeLine, starterDraft, templateBlurb, templateState } from './tiles.ts';

const sectionsCss = css({ display: 'grid', gap: t.s4, minWidth: '0' });
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

// My agents: a card per agent (mockup 6): avatar and name, one line of purpose, the model and effort chip, its state
const cardGrid = css({ display: 'grid', gridTemplateColumns: 'repeat(auto-fill, minmax(260px, 1fr))', gap: t.s3, margin: '0', padding: '0', listStyle: 'none' });
// the card is the <li>: the button (select) and the "..." menu sit side by side in it, so no control lies over another
const cardItem = css({
  display: 'grid',
  gridTemplateColumns: 'minmax(0, 1fr) auto',
  alignItems: 'start',
  minWidth: '0',
  border: `1px solid ${t.border}`,
  borderRadius: t.rLg,
  background: t.bgSurface,
  '&:hover': { borderColor: t.borderStrong },
  '&:has(> button[aria-pressed="true"])': { borderColor: t.accent, boxShadow: `0 0 0 1px ${t.accent}` },
  '& > [data-part="more"]': { padding: '8px 8px 0 0' },
});
const cardButton = css({
  display: 'grid',
  gap: t.s2,
  width: '100%',
  minWidth: '0',
  padding: '14px 4px 14px 14px',
  border: 'none',
  borderRadius: t.rLg,
  background: 'transparent',
  color: t.text,
  textAlign: 'left',
  fontWeight: '400',
  cursor: 'pointer',
  '&:hover': { background: 'transparent' },
  '&:focus-visible': { boxShadow: t.focus, outline: 'none' },
  '& > .head': { display: 'flex', alignItems: 'center', gap: t.s2, minWidth: '0' },
  '& > .head > strong': { minWidth: '0', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap', fontSize: t.textMd },
  '& > .purpose': { color: t.textMuted, fontSize: t.textMd, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' },
  '& > .foot': { display: 'flex', alignItems: 'center', gap: t.s2, minWidth: '0', flexWrap: 'wrap' },
  '& > .foot > .state': { marginLeft: 'auto', color: t.textFaint, fontSize: t.textSm },
  '& > .foot > .state[data-tone="warn"]': { color: t.warning },
});
const chipCss = css({ display: 'inline-flex', alignItems: 'center', height: '22px', padding: '0 8px', border: `1px solid ${t.border}`, borderRadius: t.rFull, color: t.textMuted, fontSize: t.textSm, whiteSpace: 'nowrap' });

function TemplateCard(handle: Handle<{ t: AgentTemplate; libraryIds: ReadonlySet<string>; selected: boolean; onSelect: () => void; onAction: (what: 'edit' | 'delete') => void }>) {
  return () => {
    const { t: tp, libraryIds, selected, onSelect, onAction } = handle.props;
    const st = templateState(tp, libraryIds);
    return (
      <li mix={cardItem} data-testid="lib-template-card" data-template={tp.name}>
        <button type="button" mix={[cardButton, on('click', onSelect)]} aria-pressed={selected ? 'true' : 'false'} data-testid="lib-template-tile" data-template={tp.name}>
          <span class="head">
            <Avatar id={tp.id} name={tp.name} size={26} kind="agent" />
            <strong>{tp.name}</strong>
            {skipsApprovals(tp.permissionMode) ? (
              <Badge tone="err" testId="tile-badge">
                skips approvals
              </Badge>
            ) : null}
          </span>
          <span class="purpose" data-testid="lib-template-purpose">
            {purposeLine(tp) || templateBlurb(tp)}
          </span>
          <span class="foot">
            <span mix={chipCss} data-testid="lib-template-model">
              {modelChip(tp)}
            </span>
            <small class="state" data-tone={st.tone || undefined}>
              {st.text}
            </small>
          </span>
        </button>
        <span data-part="more">
          <MoreMenu
            label={`More for ${tp.name}`}
            testId="lib-template-more"
            items={[
              { name: 'edit', label: 'Edit', icon: Pencil },
              { name: 'delete', label: 'Delete', icon: Trash2, danger: true },
            ]}
            onSelect={(n) => onAction(n === 'delete' ? 'delete' : 'edit')}
          />
        </span>
      </li>
    );
  };
}

/** The selected template's full panel: its purpose, the servers and skills it gives, and what can be done with it. */
function TemplatePanel(handle: Handle<{ t: AgentTemplate; servers: readonly McpLibraryServer[]; onAction: (what: 'edit' | 'delete') => void }>) {
  return () => {
    const { t: tp, servers, onAction } = handle.props;
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
            <Button size="sm" variant="ghost" icon={Pencil} mix={[on('click', () => onAction('edit'))]}>
              Edit
            </Button>
            <Button size="sm" variant="ghost" icon={Trash2} mix={[on('click', () => onAction('delete'))]}>
              Delete
            </Button>
          </div>
        </article>
      </>
    );
  };
}

/** Which tab of Agents the screen shows: My agents (/library) or Starters (/library/starters). Skills is its own screen. */
export type LibraryTab = 'mine' | 'starters';

export function LibraryScreen(handle: Handle<{ tab?: LibraryTab; onNavigate?: (href: string) => void }>) {
  const s = services(handle);
  watch(handle, s.workspace);
  const adapters = query(handle, { load: () => s.client.adapterList() });
  const servers = query(handle, { load: () => s.client.mcpLibList(), when: (e) => e.type.startsWith('mcp.server') });
  const templates = query(handle, { load: () => s.client.agentTemplateList(), when: (e) => e.type.startsWith('agenttemplate.') });
  const prebuilt = query(handle, { load: () => s.client.agentCatalogList() });
  const act = action(handle);
  let note = '';
  // the form open: a new agent, one to edit, or a starter being saved as my agent (undefined: none)
  let form: { kind: 'new' } | { kind: 'edit'; t: AgentTemplate } | { kind: 'starter'; e: PrebuiltAgent } | undefined;
  let deleting: AgentTemplate | undefined;
  // The selected card ('' for none); its panel shows below the grid. Clicking the selected card again closes it.
  let template = '';
  const set = (f: () => void) => {
    f();
    void handle.update();
  };
  const reload = () => {
    servers.reload();
    templates.reload();
  };
  const go = (href: string) => (handle.props.onNavigate ? handle.props.onNavigate(href) : void navigate(href));

  /**
   * "New agent" from the library: the one place that says what it opens (today the agent template form), so a new way to make an
   * agent (describing it in a sentence) takes it over by changing this function alone.
   */
  const openNewAgent = () => set(() => (form = { kind: 'new' }));
  const onAction = (tp: AgentTemplate, what: 'edit' | 'delete') => set(() => (what === 'edit' ? (form = { kind: 'edit', t: tp }) : (deleting = tp)));
  const remove = async (tp: AgentTemplate) => {
    const done = await act.run('delete', async () => {
      await s.client.agentTemplateDelete(tp.id);
      return true;
    });
    if (!done) return;
    set(() => {
      deleting = undefined;
      if (template === tp.id) template = '';
      note = `The agent ${tp.name} was deleted. Agents already made from it stay.`;
    });
    reload();
  };

  return () => {
    const tab = handle.props.tab ?? 'mine';
    const libServers = servers.data ?? [];
    const tpls = templates.data ?? [];
    const adapterList = adapters.data ?? [];
    const libraryIds = new Set(libServers.map((x) => x.id));
    const openTemplate = tpls.find((x) => x.id === template);
    const formTitle = !form ? '' : form.kind === 'new' ? 'New agent' : form.kind === 'edit' ? `Edit ${form.t.name}` : `Save ${form.e.name} as my agent`;
    return (
      <Page
        title={tab === 'starters' ? 'Starters' : 'My agents'}
        icon={tab === 'starters' ? Bot : Users}
        wide
        guide="crew"
        subtitle={tab === 'mine' && templates.data ? `${tpls.length} agent${tpls.length === 1 ? '' : 's'}` : undefined}
        actions={
          <>
            <a href={routes.integrations.href()} mix={buttonStyle('ghost', 'sm')} data-testid="library-servers-link">
              <Icon icon={Plug} size={14} />
              MCP servers
            </a>
            <Button variant="primary" size="sm" icon={Plus} data-testid="library-new-agent" mix={[on('click', openNewAgent)]}>
              New agent
            </Button>
          </>
        }
      >
        <div mix={sectionsCss}>
          <div role="status" aria-live="polite" data-testid="library-note" mix={noteCss}>
            {note ? <Notice tone="ok">{note}</Notice> : null}
          </div>
          <ErrorNote error={servers.error ?? templates.error} what="Agents" />
          {tab === 'mine' ? (
            <section aria-label="My agents" data-testid="library-mine" mix={sectionsCss}>
              {templates.loading && !templates.data ? <Loading /> : null}
              {templates.data && tpls.length === 0 ? (
                <p mix={emptyLine} data-testid="lib-template-empty">
                  <Icon icon={Users} size={14} />
                  <strong>No agents of your own yet.</strong> Make one, use a <a href={routes.libraryStarters.href()}>starter</a>, or save an agent of a project's <a href={routes.team.href()}>Crew</a>.
                </p>
              ) : null}
              {tpls.length > 0 ? (
                <ul mix={cardGrid} aria-label="My agents">
                  {tpls.map((x) => (
                    <TemplateCard
                      key={x.id}
                      t={x}
                      libraryIds={libraryIds}
                      selected={x.id === template}
                      onSelect={() => set(() => (template = template === x.id ? '' : x.id))}
                      onAction={(w) => onAction(x, w)}
                    />
                  ))}
                </ul>
              ) : null}
              {openTemplate ? <TemplatePanel key={openTemplate.id} t={openTemplate} servers={libServers} onAction={(w) => onAction(openTemplate, w)} /> : null}
            </section>
          ) : (
            <section aria-label="Starters" data-testid="library-prebuilt" mix={sectionsCss}>
              <ErrorNote error={prebuilt.error} what="Could not load the starters" />
              {prebuilt.loading && !prebuilt.data ? <Loading /> : null}
              <AgentGallery entries={prebuilt.data ?? []} label="Starters" pickLabel="Use" onPick={(e) => set(() => (form = { kind: 'starter', e }))} />
            </section>
          )}
        </div>

        <Dialog open={!!form} title={formTitle} wide onClose={() => set(() => (form = undefined))}>
          {form ? (
            <TemplateForm
              key={form.kind === 'edit' ? form.t.id : form.kind === 'starter' ? `starter:${form.e.id}` : 'new'}
              initial={form.kind === 'edit' ? form.t : undefined}
              draft={form.kind === 'starter' ? starterDraft(form.e) : undefined}
              adapters={adapterList}
              servers={libServers}
              onCancel={() => set(() => (form = undefined))}
              onDone={(saved) => {
                const was = form;
                set(() => {
                  form = undefined;
                  template = saved.id;
                  note = was?.kind === 'edit' ? `${saved.name} was saved.` : `${saved.name} is one of your agents now.`;
                });
                reload();
                if (tab !== 'mine') go(routes.library.href());
              }}
            />
          ) : null}
        </Dialog>
        <Dialog open={!!deleting} title={deleting ? `Delete ${deleting.name}?` : ''} onClose={() => set(() => (deleting = undefined))}>
          {deleting ? (
            <div mix={panelCard} data-testid="lib-template-delete">
              <ErrorNote error={act.error} what="Not deleted" />
              <p>Agents already made from it stay in their projects.</p>
              <div mix={actionsRow}>
                <Button variant="danger" size="sm" disabled={act.busy !== ''} mix={[on('click', () => void remove(deleting!))]}>
                  Yes, delete
                </Button>
                <Button size="sm" mix={[on('click', () => set(() => (deleting = undefined)))]}>
                  Keep
                </Button>
              </div>
            </div>
          ) : null}
        </Dialog>
      </Page>
    );
  };
}
