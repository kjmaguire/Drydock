// Crew (Crew HQ, decision P64): a dashboard of the project's crew. The figures first (who is at work, what the crew is on,
// what waits for you, its skills), then one compact row per agent (who it is, what it is doing now, what it runs, how many
// skills it has); a row opens the agent's own page with the rest (the owner's request of 2026-10-20: no agent filling the
// window). Below: the team lead's stand-up and interventions, adding an agent, and the adapters this machine has.

import { css, navigate, on, ref, type Handle } from 'remix/component';
import { Cpu, MessageSquare, Pencil, ScanSearch, Sparkles, Trash2, UserPlus, Users } from 'lucide';
import type { AdapterInfo, Agent } from '../../api/types.ts';
import { plural, presenceFor } from '../../lib/format.ts';
import { routes } from '../../routes.ts';
import { action, query, services, watch } from '../../state/services.tsx';
import { Button, IconButton } from '../../ui/button.tsx';
import { Avatar, Badge, presenceColor } from '../../ui/data.tsx';
import { Dialog } from '../../ui/dialog.tsx';
import { Disclosure } from '../../ui/disclosure.tsx';
import { Empty, ErrorNote, Loading } from '../../ui/feedback.tsx';
import { Icon } from '../../ui/icon.tsx';
import { Page, Section, card, tableCard } from '../../ui/layout.tsx';
import { dataTable } from '../../ui/table.tsx';
import { t } from '../../ui/tokens.ts';
import { SkipsBadge } from '../shared/skip-approvals.tsx';
import { AddPrebuilt } from './add-prebuilt.tsx';
import { Interventions, leadOf, RunStandup } from './lead.tsx';
import { AgentEditor, AgentForm, FromTemplate, RemoveAgent } from './agent-forms.tsx';
import { CapabilityList } from './fields.tsx';
import { CAP_LABEL, crewFigures, currentTask, doing, presenceWords, purposeExcerpt, runsWhere, skillCount, sortAdapters } from './team.ts';

const figuresCss = css({
  display: 'grid',
  gridTemplateColumns: 'repeat(auto-fit, minmax(170px, 1fr))',
  gap: t.s3,
  margin: '0',
  padding: '0',
  listStyle: 'none',
  '& li': { display: 'grid', minWidth: '0' },
  // a figure is a tile; the ones with a page of their own are a link as a whole (the target is the tile, not its words)
  '& .fig': { display: 'grid', gap: '2px', padding: `${t.s3} ${t.s4}`, border: `1px solid ${t.border}`, borderRadius: t.rLg, background: t.bgSurface, color: t.text, textDecoration: 'none', minWidth: '0' },
  '& a.fig:hover': { borderColor: t.borderStrong, boxShadow: t.shadowSm },
  '& b': { fontSize: t.text2xl, fontWeight: '700', letterSpacing: '-0.02em', lineHeight: '1.15', fontVariantNumeric: 'tabular-nums' },
  '& span': { color: t.textMuted, fontSize: t.textSm },
});

const rowsCss = css({ display: 'grid', gap: t.s2, margin: '0', padding: '0', listStyle: 'none' });

// one agent: the dot, who it is, what it is doing now, what it runs, its skills, and the actions at the end. The whole row
// opens the agent's page (its name is the link, for the keyboard and for reading).
const agentRow = css({
  display: 'grid',
  gridTemplateColumns: 'auto minmax(170px, 1.2fr) minmax(180px, 1.5fr) minmax(150px, 1fr) auto auto',
  alignItems: 'center',
  gap: `${t.s2} ${t.s4}`,
  padding: `${t.s3} ${t.s4}`,
  border: `1px solid ${t.border}`,
  borderRadius: t.rLg,
  background: t.bgSurface,
  minWidth: '0',
  cursor: 'pointer',
  transition: `border-color ${t.fast} ${t.ease}, box-shadow ${t.fast} ${t.ease}`,
  '&:hover': { borderColor: t.borderStrong, boxShadow: t.shadowSm },
  '@media (max-width: 1100px)': { gridTemplateColumns: 'auto minmax(0, 1fr) minmax(0, 1fr)', '& .spec, & .skills': { gridColumn: '2 / -1' }, '& .actions': { gridColumn: '1 / -1' } },
  '& .who': { display: 'grid', gap: '2px', minWidth: '0' },
  '& h3': { margin: '0', fontSize: t.textLg, fontWeight: '600', letterSpacing: '-0.01em', whiteSpace: 'nowrap', overflow: 'hidden', textOverflow: 'ellipsis' },
  '& h3 a': { display: 'inline-flex', alignItems: 'center', minHeight: '28px', maxWidth: '100%', color: t.text, textDecoration: 'none' },
  '& h3 a:hover': { textDecoration: 'underline' },
  '& .role': { display: 'flex', alignItems: 'center', gap: t.s2, color: t.textFaint, fontSize: t.textSm },
  '& .purpose': { margin: '0', fontSize: t.textSm, color: t.textMuted, whiteSpace: 'nowrap', overflow: 'hidden', textOverflow: 'ellipsis' },
  '& .doing': { display: 'grid', gap: '2px', minWidth: '0' },
  '& .now': { display: 'flex', alignItems: 'flex-start', gap: '7px', color: t.textMuted, fontSize: t.textSm, lineHeight: '1.4', overflowWrap: 'anywhere' },
  '& .now > i': { flex: 'none', width: '7px', height: '7px', marginTop: '6px', borderRadius: '50%', display: 'block' },
  '& .on': { display: 'inline-flex', alignItems: 'center', minHeight: '28px', maxWidth: '100%', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap', fontSize: t.textSm, color: t.accentText },
  '& .spec': { display: 'flex', flexWrap: 'wrap', alignItems: 'center', gap: '2px 8px', fontSize: t.textSm, color: t.textMuted, minWidth: '0' },
  '& .spec b': { fontWeight: '500', color: t.text },
  '& .spec code': { fontSize: t.textSm },
  '& .skills': { fontSize: t.textSm, color: t.textMuted, whiteSpace: 'nowrap' },
  '& .skills[data-none="true"]': { color: t.warning },
  '& .actions': { display: 'flex', alignItems: 'center', gap: '2px', justifyContent: 'flex-end' },
});

const addCard = css({ display: 'grid', gap: t.s4, scrollMarginTop: t.s4, borderRadius: t.rXl });
const divider = css({ height: '1px', background: t.border, margin: '0' });

const adapterName = css({ whiteSpace: 'nowrap', '& svg': { verticalAlign: '-2px', marginRight: '6px' } });
const adaptersCss = css({
  '& .note': { margin: `${t.s1} 0 0`, fontSize: t.textSm, color: t.textMuted, overflowWrap: 'anywhere' },
  '& .err': { margin: `${t.s1} 0 0`, fontSize: t.textSm, color: t.danger, overflowWrap: 'anywhere' },
});

/** One row of the adapters table: what was found, and how much of what Drydock can use of it was verified (the list is one click away). */
function AdapterRow(handle: Handle<{ a: AdapterInfo }>) {
  return () => {
    const a = handle.props.a;
    const d = a.detection;
    const verified = CAP_LABEL.filter(([k]) => a.capabilities?.[k] === true).length;
    const problems = [a.error, ...(d.problems ?? [])].filter((x): x is string => !!x);
    return (
      <tr data-testid="adapter" data-adapter={a.id}>
        <th scope="row" mix={adapterName}>
          <Icon icon={Cpu} size={14} />
          {a.id}
        </th>
        <td>
          <Badge tone={d.status === 'ready' ? 'ok' : 'warn'}>{d.status}</Badge>
        </td>
        <td class="nowrap">{d.installed ? (d.version ?? 'installed') : 'not installed'}</td>
        <td class="nowrap">{d.authenticated === true ? 'yes' : d.authenticated === false ? 'no' : 'not reported'}</td>
        <td>
          <Disclosure summary={`${verified} of ${CAP_LABEL.length} verified`}>
            <CapabilityList caps={a.capabilities} />
          </Disclosure>
          {d.path ? (
            <p class="note">
              <code>{d.path}</code>
            </p>
          ) : null}
          {d.authDetail ? <p class="note">{d.authDetail}</p> : null}
          {problems.map((p, i) => (
            <p key={i} class="err">
              {p}
            </p>
          ))}
          {d.installHint ? (
            <p class="note">
              Install: <code>{d.installHint}</code>
            </p>
          ) : null}
        </td>
      </tr>
    );
  };
}

/** `onNavigate` replaces the router's navigate (tests). */
export function TeamScreen(handle: Handle<{ onNavigate?: (href: string) => void }>) {
  const s = services(handle);
  watch(handle, s.project);
  watch(handle, s.workspace);
  const adapters = query(handle, { load: () => s.client.adapterList() });
  const skills = query(handle, {
    load: () => s.client.skillList(s.workspace.projectId ?? ''),
    key: () => s.workspace.projectId ?? '',
    enabled: () => !!s.workspace.projectId,
    when: (e) => e.type.startsWith('skill.') || e.type.startsWith('agent.'),
  });
  const go = (href: string) => {
    if (handle.props.onNavigate) handle.props.onNavigate(href);
    else void navigate(href);
  };
  const act = action(handle);
  let editing: string | undefined;
  let removing: string | undefined;
  let prebuiltOpen = false;
  let addSection: HTMLElement | undefined;

  const reload = () => s.project.agents.reload();

  const message = (agent: Agent) =>
    act.run('dm', async () => {
      const room = await s.client.roomCreateDM(agent.projectId || s.workspace.projectId || '', agent.id);
      go(routes.room.href({ id: room.id }));
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
    const tasks = s.project.taskList;
    const sorted = sortAdapters(adapters.data ?? []);
    const editAgent = agents.find((a) => a.id === editing);
    const removeAgent = agents.find((a) => a.id === removing);
    const count = agentsQ.data ? `${agents.length} ${agents.length === 1 ? 'agent' : 'agents'}` : undefined;
    const fig = crewFigures(agents, presence, tasks, skills.data ?? []);

    return (
      <Page
        title="Crew"
        icon={Users}
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
        {agents.length > 0 ? (
          <ul mix={figuresCss} aria-label="The crew at a glance" data-testid="crew-figures">
            <li data-testid="figure-working">
              <div class="fig">
                <b>
                  {fig.working} of {fig.agents}
                </b>
                <span>at work now{fig.stopped > 0 ? `, ${fig.stopped} stopped` : ''}</span>
              </div>
            </li>
            <li data-testid="figure-in-flight">
              <a class="fig" href={routes.tasks.href()}>
                <b>{fig.inFlight}</b>
                <span>{fig.inFlight === 1 ? 'task' : 'tasks'} in progress or review</span>
              </a>
            </li>
            <li data-testid="figure-waiting">
              <a class="fig" href={routes.needsYou.href()}>
                <b>{fig.waiting}</b>
                <span>{fig.waiting === 1 ? 'task waits' : 'tasks wait'} for you</span>
              </a>
            </li>
            <li data-testid="figure-skills">
              <div class="fig">
                <b>{skills.data ? fig.skillsGiven : ''}</b>
                <span>{skills.data ? `skills given${fig.withoutSkills > 0 ? `, ${plural(fig.withoutSkills, 'agent')} with none` : ''}` : 'skills'}</span>
              </div>
            </li>
          </ul>
        ) : null}

        <Section title="Agents" description="Each agent is a real coding CLI and a specialist in its role. Click one to see its page.">
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
            <ul mix={rowsCss} aria-label="Agents">
              {agents.map((a) => {
                const p = presenceFor(a.id, presence);
                const where = runsWhere(a);
                const n = skillCount(skills.data ?? [], a.id);
                const on_ = currentTask(a.id, p, [], tasks);
                return (
                  <li
                    key={a.id}
                    mix={[
                      agentRow,
                      on('click', (e) => {
                        // the row opens the agent's page; its own links and buttons do what they say
                        if ((e.target as Element).closest('a, button, input, [role="dialog"]')) return;
                        go(routes.agent.href({ id: a.id }));
                      }),
                    ]}
                    data-testid="agent"
                    data-name={a.name}
                  >
                    <Avatar id={a.id} name={a.name} size={40} status={presenceColor(p)} kind="agent" />
                    <div class="who">
                      <h3 title={a.name}>
                        <a href={routes.agent.href({ id: a.id })} data-testid="agent-link">
                          {a.name}
                        </a>
                      </h3>
                      <span class="role">
                        {a.role}
                        <SkipsBadge mode={a.permissionMode} />
                      </span>
                      {a.purpose ? (
                        <p class="purpose" data-testid="agent-purpose" title={a.purpose}>
                          Purpose: {purposeExcerpt(a.purpose)}
                        </p>
                      ) : null}
                    </div>
                    <div class="doing">
                      <span class="now">
                        <i style={{ background: presenceColor(p) }} aria-hidden="true" />
                        <span>{doing(p)}</span>
                        {/* the presence in words for assistive tech (the dot is colour, the text is the activity) */}
                        <span class="sr-only" data-testid="presence">
                          {presenceWords(p)}
                        </span>
                      </span>
                      {on_ ? (
                        <a class="on" href={routes.task.href({ id: on_.id })} data-testid="agent-task" title={on_.title}>
                          Task #{on_.seq} {on_.title}
                        </a>
                      ) : null}
                    </div>
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
                    <span class="skills" data-testid="agent-skill-count" data-none={skills.data && n === 0 ? 'true' : undefined}>
                      {skills.data ? (n === 0 ? 'No skill yet' : plural(n, 'skill')) : ''}
                    </span>
                    <div class="actions">
                      <IconButton icon={ScanSearch} label="Inspect" title={`Inspect ${a.name}`} mix={on('click', () => s.ui.openInspector(a.id))} />
                      <IconButton icon={MessageSquare} label="Message" title={`Message ${a.name}`} disabled={act.busy === 'dm'} mix={on('click', () => void message(a))} />
                      <IconButton
                        icon={Pencil}
                        label={`Edit ${a.name}`}
                        aria-haspopup="dialog"
                        aria-expanded={editing === a.id}
                        mix={on('click', () => ((editing = a.id), void handle.update()))}
                      />
                      <IconButton icon={Trash2} label={`Remove ${a.name}`} aria-haspopup="dialog" mix={on('click', () => ((removing = a.id), void handle.update()))} />
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

        <Section title="Adapters" description="The agent CLIs Drydock found on this machine. A capability that was not checked is never claimed. Drydock never installs a CLI for you.">
          <ErrorNote error={adapters.error} what="Could not list adapters" />
          {adapters.loading && !adapters.data ? <Loading label="Detecting..." /> : null}
          <div mix={tableCard}>
            <table mix={[dataTable, adaptersCss]} aria-label="Adapters">
              <thead>
                <tr>
                  <th scope="col">Adapter</th>
                  <th scope="col">Status</th>
                  <th scope="col">Version</th>
                  <th scope="col">Signed in</th>
                  <th scope="col">Capabilities</th>
                </tr>
              </thead>
              <tbody>
                {sorted.map((a) => (
                  <AdapterRow key={a.id} a={a} />
                ))}
              </tbody>
            </table>
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
