// Crew (Crew HQ, decision P64): the project's agents as large dots (who they are, what each is doing right now, what it
// runs), each a link to its own page; adding one from a template, a prebuilt agent or from scratch; editing and removing
// them; the team lead's stand-up and interventions; and the adapters this machine has.

import { css, navigate, on, ref, type Handle } from 'remix/component';
import { Cpu, MessageSquare, Pencil, ScanSearch, Sparkles, Trash2, UserPlus, Users } from 'lucide';
import type { AdapterInfo, Agent } from '../../api/types.ts';
import { presenceFor } from '../../lib/format.ts';
import { routes } from '../../routes.ts';
import { action, query, services, watch } from '../../state/services.tsx';
import { Button, IconButton } from '../../ui/button.tsx';
import { Avatar, Badge, kv, muted, presenceColor } from '../../ui/data.tsx';
import { Dialog } from '../../ui/dialog.tsx';
import { Empty, ErrorNote, Loading } from '../../ui/feedback.tsx';
import { Icon } from '../../ui/icon.tsx';
import { Page, Section, card, cardGrid } from '../../ui/layout.tsx';
import { t } from '../../ui/tokens.ts';
import { AddPrebuilt } from './add-prebuilt.tsx';
import { Interventions, leadOf, RunStandup } from './lead.tsx';
import { AgentEditor, AgentForm, FromTemplate, RemoveAgent } from './agent-forms.tsx';
import { CapabilityList } from './fields.tsx';
import { doing, presenceWords, purposeExcerpt, runsWhere, sortAdapters } from './team.ts';

const gridCss = css({ display: 'grid', gridTemplateColumns: 'repeat(auto-fill, minmax(272px, 1fr))', gap: t.s3, margin: '0', padding: '0', listStyle: 'none' });

// a crew card: the dot, the name (a link to the agent's page), the role, what it is doing now, what it runs, its purpose, and
// the actions along the bottom
const agentCard = css({
  display: 'flex',
  flexDirection: 'column',
  alignItems: 'center',
  gap: '4px',
  padding: `${t.s5} 14px ${t.s3}`,
  border: `1px solid ${t.border}`,
  borderRadius: t.rXl,
  background: t.bgSurface,
  minWidth: '0',
  textAlign: 'center',
  transition: `border-color ${t.fast} ${t.ease}, box-shadow ${t.fast} ${t.ease}`,
  '&:hover': { borderColor: t.borderStrong, boxShadow: t.shadowSm },
  '& h3': { marginTop: t.s2, fontSize: t.textLg, fontWeight: '650', letterSpacing: '-0.01em', maxWidth: '100%', whiteSpace: 'nowrap', overflow: 'hidden', textOverflow: 'ellipsis' },
  '& h3 a': { color: t.text, textDecoration: 'none' },
  '& h3 a:hover': { textDecoration: 'underline' },
  '& .role': { color: t.textFaint, fontSize: t.textSm },
  '& .now': {
    display: 'flex',
    alignItems: 'flex-start',
    gap: '7px',
    width: '100%',
    marginTop: t.s2,
    padding: `${t.s2} 10px`,
    borderRadius: t.rMd,
    background: t.bgSidebar,
    color: t.textMuted,
    fontSize: t.textSm,
    lineHeight: '1.4',
    textAlign: 'left',
    overflowWrap: 'anywhere',
  },
  '& .now > i': { flex: 'none', width: '7px', height: '7px', marginTop: '6px', borderRadius: '50%', display: 'block' },
  '& .spec': { display: 'flex', flexWrap: 'wrap', justifyContent: 'center', alignItems: 'center', gap: '2px 8px', marginTop: t.s2, fontSize: t.textSm, color: t.textMuted },
  '& .spec b': { fontWeight: '550', color: t.text },
  '& .spec code': { fontSize: t.textSm },
  '& .purpose': {
    margin: `${t.s2} 0 0`,
    fontSize: t.textSm,
    color: t.textMuted,
    lineHeight: '1.5',
    display: '-webkit-box',
    WebkitLineClamp: '3',
    WebkitBoxOrient: 'vertical',
    overflow: 'hidden',
    textAlign: 'left',
    width: '100%',
  },
  '& .nopurpose': { margin: `${t.s2} 0 0`, fontSize: t.textSm, color: t.textFaint, fontStyle: 'italic' },
  '& .actions': { display: 'flex', alignItems: 'center', gap: '2px', width: '100%', marginTop: 'auto', paddingTop: t.s3 },
  '& .actions .end': { marginLeft: 'auto', display: 'flex', gap: '2px' },
});

const addCard = css({ display: 'grid', gap: t.s4, scrollMarginTop: t.s4, borderRadius: t.rXl });
const divider = css({ height: '1px', background: t.border, margin: '0' });

const adapterCss = css({
  display: 'grid',
  gap: t.s3,
  alignContent: 'start',
  borderRadius: t.rXl,
  '& header': { display: 'flex', alignItems: 'center', gap: t.s2, flexWrap: 'wrap' },
  '& h3': { fontSize: t.textMd, fontWeight: '600', margin: '0', display: 'inline-flex', alignItems: 'center', gap: '6px' },
  '& .note': { margin: '0', fontSize: t.textSm, color: t.textMuted, overflowWrap: 'anywhere' },
  '& .err': { margin: '0', fontSize: t.textSm, color: t.danger, overflowWrap: 'anywhere' },
  '& summary': { fontSize: t.textSm },
});

function AdapterCard(handle: Handle<{ a: AdapterInfo }>) {
  return () => {
    const a = handle.props.a;
    const d = a.detection;
    return (
      <article mix={[card, adapterCss]} data-testid="adapter" data-adapter={a.id}>
        <header>
          <h3>
            <Icon icon={Cpu} size={14} />
            {a.id}
          </h3>
          <Badge tone={d.status === 'ready' ? 'ok' : 'warn'}>{d.status}</Badge>
          <span mix={muted} style={{ fontSize: 'var(--text-sm)', marginLeft: 'auto' }}>
            {d.installed ? 'installed' : 'not installed'}
          </span>
        </header>
        <dl mix={kv}>
          {d.version ? (
            <>
              <dt>Version</dt>
              <dd>{d.version}</dd>
            </>
          ) : null}
          {d.path ? (
            <>
              <dt>Path</dt>
              <dd>
                <code>{d.path}</code>
              </dd>
            </>
          ) : null}
          <dt>Signed in</dt>
          <dd>{d.authenticated === true ? 'yes' : d.authenticated === false ? 'no' : 'not reported'}</dd>
        </dl>
        {d.authDetail ? <p class="note">{d.authDetail}</p> : null}
        {a.error ? <p class="err">{a.error}</p> : null}
        {(d.problems ?? []).map((p, i) => (
          <p key={i} class="err">
            {p}
          </p>
        ))}
        {d.installHint ? (
          <p class="note">
            Install guidance (Drydock never runs this for you): <code>{d.installHint}</code>
          </p>
        ) : null}
        <details>
          <summary>Capabilities (absent means not verified)</summary>
          <CapabilityList caps={a.capabilities} />
        </details>
      </article>
    );
  };
}

/** `onNavigate` replaces the router's navigate (tests). */
export function TeamScreen(handle: Handle<{ onNavigate?: (href: string) => void }>) {
  const s = services(handle);
  watch(handle, s.project);
  watch(handle, s.workspace);
  const adapters = query(handle, { load: () => s.client.adapterList() });
  const act = action(handle);
  let editing: string | undefined;
  let removing: string | undefined;
  let prebuiltOpen = false;
  let addSection: HTMLElement | undefined;

  const reload = () => s.project.agents.reload();

  const message = (agent: Agent) =>
    act.run('dm', async () => {
      const room = await s.client.roomCreateDM(agent.projectId || s.workspace.projectId || '', agent.id);
      const href = routes.room.href({ id: room.id });
      if (handle.props.onNavigate) handle.props.onNavigate(href);
      else void navigate(href);
    });

  const focusAdd = () => {
    addSection?.scrollIntoView({ behavior: 'smooth', block: 'start' });
    addSection?.querySelector<HTMLInputElement>('[data-autofocus]')?.focus({ preventScroll: true });
  };

  return () => {
    const projectId = s.workspace.projectId ?? '';
    const agentsQ = s.project.agents;
    const agents = s.project.agentList;
    const presence = s.project.presence.data ?? [];
    const sorted = sortAdapters(adapters.data ?? []);
    const editAgent = agents.find((a) => a.id === editing);
    const removeAgent = agents.find((a) => a.id === removing);
    const count = agentsQ.data ? `${agents.length} ${agents.length === 1 ? 'agent' : 'agents'}` : undefined;

    return (
      <Page
        title="Crew"
        subtitle={count}
        testId="crew"
        actions={
          <>
            <Button variant="secondary" icon={Sparkles} disabled={!projectId} mix={on('click', () => ((prebuiltOpen = true), void handle.update()))}>
              Add prebuilt agent
            </Button>
            <Button variant="primary" icon={UserPlus} mix={on('click', focusAdd)}>
              Add agent
            </Button>
          </>
        }
      >
        <Section title="Your crew" description="Each agent is a real coding CLI with a role. A task needs a worker and a different agent as reviewer.">
          <ErrorNote error={agentsQ.error} what="Could not list agents" />
          <ErrorNote error={act.error} what="Could not open a direct message" />
          {agentsQ.loading && !agentsQ.data ? <Loading /> : null}
          {agentsQ.data && agents.length === 0 ? (
            <div mix={card}>
              <Empty title="No agents yet" icon={Users}>
                <p>No agents yet. A task needs a worker and a different agent as reviewer.</p>
              </Empty>
            </div>
          ) : null}
          {agents.length > 0 ? (
            <ul mix={gridCss} aria-label="Agents">
              {agents.map((a) => {
                const p = presenceFor(a.id, presence);
                const where = runsWhere(a);
                return (
                  <li key={a.id} mix={agentCard} data-testid="agent" data-name={a.name}>
                    <Avatar id={a.id} name={a.name} size={64} status={presenceColor(p)} kind="agent" />
                    <h3 title={a.name}>
                      <a href={routes.agent.href({ id: a.id })} data-testid="agent-link">
                        {a.name}
                      </a>
                    </h3>
                    <span class="role">{a.role}</span>
                    <span class="now">
                      <i style={{ background: presenceColor(p) }} aria-hidden="true" />
                      <span>{doing(p)}</span>
                      {/* the presence in words for assistive tech (the dot is colour, the text is the activity) */}
                      <span class="sr-only" data-testid="presence">
                        {presenceWords(p)}
                      </span>
                    </span>
                    <div class="spec">
                      <code>{a.adapter}</code>
                      {a.model ? (
                        <span>
                          model <b>{a.model}</b>
                        </span>
                      ) : (
                        <span>default model</span>
                      )}
                      {a.effort ? (
                        <span>
                          effort <b>{a.effort}</b>
                        </span>
                      ) : null}
                      {where ? <span data-testid="agent-runs">{where}</span> : null}
                    </div>
                    {a.purpose ? (
                      <p class="purpose" data-testid="agent-purpose" title={a.purpose}>
                        Purpose: {purposeExcerpt(a.purpose)}
                      </p>
                    ) : (
                      <p class="nopurpose">No standing instructions.</p>
                    )}
                    <div class="actions">
                      <Button size="sm" variant="ghost" icon={ScanSearch} mix={on('click', () => s.ui.openInspector(a.id))}>
                        Inspect
                      </Button>
                      <Button size="sm" variant="ghost" icon={MessageSquare} disabled={act.busy === 'dm'} mix={on('click', () => void message(a))}>
                        Message
                      </Button>
                      <span class="end">
                        <IconButton
                          icon={Pencil}
                          label={`Edit ${a.name}`}
                          aria-haspopup="dialog"
                          aria-expanded={editing === a.id}
                          mix={on('click', () => ((editing = a.id), void handle.update()))}
                        />
                        <IconButton icon={Trash2} label={`Remove ${a.name}`} aria-haspopup="dialog" mix={on('click', () => ((removing = a.id), void handle.update()))} />
                      </span>
                    </div>
                  </li>
                );
              })}
            </ul>
          ) : null}
        </Section>

        {leadOf(agents) && projectId ? (
          <Section
            title="Team lead"
            label="Team lead"
            description={`${leadOf(agents)!.name} supervises the team: it steps in when a task loops, stalls or trips the watchdog, and can post a stand-up.`}
            actions={<RunStandup projectId={projectId} lead={leadOf(agents)!} />}
          >
            <Interventions projectId={projectId} limit={20} />
          </Section>
        ) : null}

        <Section title="Add an agent" description="Start from a saved template, or describe a new team member.">
          <div
            mix={[
              card,
              addCard,
              ref((n) => {
                addSection = n as HTMLElement;
              }),
            ]}
          >
            <FromTemplate projectId={projectId} onCreated={reload} />
            <div mix={divider} role="presentation" />
            <AgentForm projectId={projectId} adapters={sorted} onCreated={reload} />
          </div>
        </Section>

        <Section title="Adapters" description="The agent CLIs Drydock found on this machine. Capabilities that were not checked are never claimed.">
          <ErrorNote error={adapters.error} what="Could not list adapters" />
          {adapters.loading && !adapters.data ? <Loading label="Detecting..." /> : null}
          <div mix={cardGrid}>
            {sorted.map((a) => (
              <AdapterCard key={a.id} a={a} />
            ))}
          </div>
        </Section>

        <Dialog
          open={!!editAgent}
          title={editAgent ? `Edit ${editAgent.name}` : ''}
          label={editAgent ? `Edit ${editAgent.name}` : undefined}
          wide
          onClose={() => ((editing = undefined), void handle.update())}
        >
          {editAgent ? (
            <AgentEditor
              key={editAgent.id}
              agent={editAgent}
              adapter={sorted.find((x) => x.id === editAgent.adapter)}
              onDone={() => {
                editing = undefined;
                void handle.update();
                reload();
              }}
              onCancel={() => ((editing = undefined), void handle.update())}
            />
          ) : null}
        </Dialog>
        <Dialog open={prebuiltOpen} title="Add a prebuilt agent" label="Add a prebuilt agent" wide onClose={() => ((prebuiltOpen = false), void handle.update())}>
          {prebuiltOpen ? <AddPrebuilt projectId={projectId} onAdded={() => reload()} /> : null}
        </Dialog>
        <Dialog open={!!removeAgent} title={removeAgent ? `Remove ${removeAgent.name}?` : ''} onClose={() => ((removing = undefined), void handle.update())}>
          {removeAgent ? (
            <RemoveAgent
              key={removeAgent.id}
              agent={removeAgent}
              onDone={() => {
                removing = undefined;
                void handle.update();
                reload();
              }}
              onCancel={() => ((removing = undefined), void handle.update())}
            />
          ) : null}
        </Dialog>
      </Page>
    );
  };
}
