// Team: the project's agents as cards (who they are, what they run, what they are doing), adding one from a template or
// from scratch, editing and removing them, and the adapters this machine has.

import { css, navigate, on, ref, type Handle } from 'remix/component';
import { Cpu, MessageSquare, Pencil, ScanSearch, Sparkles, Trash2, UserPlus, Users } from 'lucide';
import type { AdapterInfo, Agent } from '../../api/types.ts';
import { presenceFor } from '../../lib/format.ts';
import { routes } from '../../routes.ts';
import { action, query, services, watch } from '../../state/services.tsx';
import { Button, IconButton } from '../../ui/button.tsx';
import { Avatar, Badge, PresenceDot, kv, muted, presenceColor } from '../../ui/data.tsx';
import { Dialog } from '../../ui/dialog.tsx';
import { Empty, ErrorNote, Loading } from '../../ui/feedback.tsx';
import { Icon } from '../../ui/icon.tsx';
import { Page, Section, card, cardGrid } from '../../ui/layout.tsx';
import { t } from '../../ui/tokens.ts';
import { AddPrebuilt } from './add-prebuilt.tsx';
import { AgentEditor, AgentForm, FromTemplate, RemoveAgent } from './agent-forms.tsx';
import { CapabilityList } from './fields.tsx';
import { purposeExcerpt, sortAdapters } from './team.ts';

const gridCss = css({ display: 'grid', gridTemplateColumns: 'repeat(auto-fill, minmax(290px, 1fr))', gap: t.s3, margin: '0', padding: '0', listStyle: 'none' });

const agentCard = css({
  display: 'flex',
  flexDirection: 'column',
  gap: t.s3,
  padding: t.s4,
  border: `1px solid ${t.border}`,
  borderRadius: t.rLg,
  background: t.bgSurface,
  minWidth: '0',
  transition: `border-color ${t.fast} ${t.ease}, box-shadow ${t.fast} ${t.ease}`,
  '&:hover': { borderColor: t.borderStrong, boxShadow: t.shadowSm },
  '& .head': { display: 'flex', alignItems: 'center', gap: t.s3, minWidth: '0' },
  '& .who': { display: 'grid', gap: '3px', minWidth: '0', flex: '1' },
  '& h3': { fontSize: t.textMd, fontWeight: '600', whiteSpace: 'nowrap', overflow: 'hidden', textOverflow: 'ellipsis', margin: '0' },
  '& .sub': { display: 'flex', alignItems: 'center', gap: t.s2, minWidth: '0' },
  '& .spec': { display: 'flex', flexWrap: 'wrap', alignItems: 'center', gap: '4px 10px', fontSize: t.textSm, color: t.textMuted },
  '& .spec b': { fontWeight: '500', color: t.text },
  '& .purpose': {
    margin: '0',
    fontSize: t.textSm,
    color: t.textMuted,
    lineHeight: '1.5',
    display: '-webkit-box',
    WebkitLineClamp: '3',
    WebkitBoxOrient: 'vertical',
    overflow: 'hidden',
  },
  '& .nopurpose': { margin: '0', fontSize: t.textSm, color: t.textFaint, fontStyle: 'italic' },
  '& .actions': { display: 'flex', alignItems: 'center', gap: t.s1, marginTop: 'auto', paddingTop: t.s3, borderTop: `1px solid ${t.border}` },
  '& .actions .end': { marginLeft: 'auto', display: 'flex', gap: '2px' },
});

const addCard = css({ display: 'grid', gap: t.s4, scrollMarginTop: t.s4 });
const divider = css({ height: '1px', background: t.border, margin: '0' });

const adapterCss = css({
  display: 'grid',
  gap: t.s3,
  alignContent: 'start',
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
        title="Team"
        icon={Users}
        subtitle={count}
        actions={
          <>
            <Button variant="secondary" size="sm" icon={Sparkles} disabled={!projectId} mix={on('click', () => ((prebuiltOpen = true), void handle.update()))}>
              Add prebuilt agent
            </Button>
            <Button variant="primary" size="sm" icon={UserPlus} mix={on('click', focusAdd)}>
              Add agent
            </Button>
          </>
        }
      >
        <Section title="Agents" description="Each agent is a real coding CLI with a role. A task needs a worker and a different agent as reviewer.">
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
                return (
                  <li key={a.id} mix={agentCard} data-testid="agent" data-name={a.name}>
                    <div class="head">
                      <Avatar id={a.id} name={a.name} size={38} status={presenceColor(p)} />
                      <div class="who">
                        <h3 title={a.name}>{a.name}</h3>
                        <div class="sub">
                          <Badge tone="accent">{a.role}</Badge>
                          <PresenceDot presence={p} />
                        </div>
                      </div>
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
