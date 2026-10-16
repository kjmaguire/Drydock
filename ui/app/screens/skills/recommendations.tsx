// Recommendations (decision P52) on a project's Skills page: an agent of the team (the lead unless the person picks another)
// reads the project and what the workspace offers, and says which agent should get which skill, MCP server or integration and
// why. The person applies or dismisses each item; nothing is applied by the agent. Skills picked in the workspace Library
// arrive here first: they can be added at once or given to the advisor to judge. The project asks again by itself as it
// evolves unless the person switches that off.

import { css, on, type Handle } from 'remix/component';
import { Check, Lightbulb, Plug, Sparkles, X } from 'lucide';
import type { RecommendItem, SkillPick } from '../../api/extTypes.ts';
import type { Agent } from '../../api/types.ts';
import { formatDateTime } from '../../lib/format.ts';
import { action, query, services } from '../../state/services.tsx';
import { Button } from '../../ui/button.tsx';
import { Badge } from '../../ui/data.tsx';
import { ErrorNote, Loading, Notice } from '../../ui/feedback.tsx';
import { Checkbox } from '../../ui/form.tsx';
import { Select } from '../../ui/select.tsx';
import { Icon } from '../../ui/icon.tsx';
import { Section } from '../../ui/layout.tsx';
import { t } from '../../ui/tokens.ts';
import { itemText, openItems, triggerText } from './recommend.ts';

const askCss = css({
  display: 'flex',
  alignItems: 'center',
  gap: t.s2,
  flexWrap: 'wrap',
  '& [role="combobox"]': { maxWidth: '260px' },
});
const listCss = css({
  listStyle: 'none',
  margin: '0',
  padding: '0',
  display: 'grid',
  gap: t.s2,
  '& li': {
    display: 'grid',
    gridTemplateColumns: 'auto minmax(0, 1fr) auto',
    alignItems: 'start',
    gap: t.s3,
    padding: `${t.s2} ${t.s3}`,
    border: `1px solid ${t.border}`,
    borderRadius: t.rMd,
    background: t.bgSurface,
  },
  '& li[data-status="applied"], & li[data-status="dismissed"]': { opacity: '0.7' },
  '& .what': { fontWeight: '600', fontSize: t.textMd },
  '& .why': { color: t.textMuted, fontSize: t.textSm },
  '& .acts': { display: 'flex', gap: t.s2, flexWrap: 'wrap', justifyContent: 'flex-end' },
});
const metaCss = css({ margin: '0', color: t.textMuted, fontSize: t.textSm });
const kindIcon = { skill: Sparkles, mcp: Plug, integration: Plug } as const;

export interface RecommendationsProps {
  projectId: string;
  projectName: string;
  agents: readonly Agent[];
  /** Skills picked in the workspace Library for this project, not added yet. */
  picks: readonly SkillPick[];
  onPicksDone: () => void;
  /** Something changed in the project's skills or servers: the page re-reads them. */
  onChanged: () => void;
}

export function Recommendations(handle: Handle<RecommendationsProps>) {
  const s = services(handle);
  const recs = query(handle, {
    load: () => s.client.recommendList(handle.props.projectId),
    key: () => handle.props.projectId,
    when: (e) => e.type.startsWith('recommend.'),
  });
  const act = action(handle);
  let chosen = '';
  let added = '';
  const set = (f: () => void) => {
    f();
    void handle.update();
  };
  const advisor = (): Agent | undefined => {
    const team = handle.props.agents;
    return team.find((a) => a.id === chosen) ?? team.find((a) => a.role === 'lead') ?? team[0];
  };
  const ask = async (withPicks: boolean) => {
    const a = advisor();
    const r = await act.run('ask', () => s.client.recommendStart(handle.props.projectId, a?.id, withPicks ? [...handle.props.picks] : []));
    if (r) {
      if (withPicks) handle.props.onPicksDone();
      recs.reload();
    }
  };
  const addPicks = async () => {
    const { projectId, picks, onPicksDone, onChanged } = handle.props;
    const done: string[] = [];
    const ok = await act.run('addPicks', async () => {
      for (const p of picks) {
        await s.client.skillLibraryAdd(projectId, p.registryId, p.name);
        done.push(p.name);
      }
      return true;
    });
    if (done.length > 0) {
      set(() => (added = `${done.join(', ')} ${done.length === 1 ? 'was' : 'were'} added to ${handle.props.projectName} for the whole team.`));
      onChanged();
    }
    if (ok) onPicksDone();
  };
  const apply = async (id: string, itemId: string) => {
    if (await act.run(`apply-${itemId}`, () => s.client.recommendApply(id, itemId))) {
      recs.reload();
      handle.props.onChanged();
    }
  };
  const dismiss = async (id: string, itemId: string) => {
    if (await act.run(`dismiss-${itemId}`, () => s.client.recommendDismiss(id, itemId))) recs.reload();
  };
  const setAuto = async (on: boolean) => {
    if (await act.run('auto', () => s.client.recommendSetAuto(handle.props.projectId, on))) recs.reload();
  };

  return () => {
    const { agents, picks, projectName } = handle.props;
    const names = new Map(agents.map((a) => [a.id, a.name] as const));
    const a = advisor();
    const runs = recs.data?.runs ?? [];
    const latest = runs[0];
    const running = latest?.status === 'running';
    const earlier = runs.slice(1);
    const item = (rid: string, it: RecommendItem) => (
      <li key={it.id} data-testid="recommend-item" data-kind={it.kind} data-status={it.status}>
        <Icon icon={kindIcon[it.kind as keyof typeof kindIcon] ?? Lightbulb} size={16} />
        <div>
          <div class="what">{itemText(it, names)}</div>
          <div class="why">{it.reason}</div>
          {it.note ? <div class="why">{it.note}</div> : null}
        </div>
        <div class="acts">
          {it.status === 'open' ? (
            <>
              {it.kind === 'integration' ? (
                <span class="why">Assign it on this project's Integrations page, where you choose what it syncs.</span>
              ) : (
                <Button size="sm" variant="primary" icon={Check} disabled={act.busy !== ''} data-testid="recommend-apply" mix={on('click', () => void apply(rid, it.id))}>
                  {act.busy === `apply-${it.id}` ? 'Applying...' : 'Apply'}
                </Button>
              )}
              <Button size="sm" variant="ghost" icon={X} disabled={act.busy !== ''} data-testid="recommend-dismiss" mix={on('click', () => void dismiss(rid, it.id))}>
                Dismiss
              </Button>
            </>
          ) : (
            <Badge tone={it.status === 'applied' ? 'ok' : 'muted'}>{it.status}</Badge>
          )}
        </div>
      </li>
    );
    return (
      <Section
        label="Recommendations"
        title="Recommendations"
        description="An agent of your team reads the project's plan, tasks and team, and recommends who should get which skill, MCP server or integration. You apply or dismiss each one."
        testId="recommendations"
      >
        <ErrorNote error={recs.error ?? act.error} what="Recommendations" />
        {added ? (
          <Notice tone="ok" role="status" testId="picks-added">
            {added}
          </Notice>
        ) : null}
        {picks.length > 0 ? (
          <Notice tone="info" testId="skill-picks" title={`You picked ${picks.length} skill${picks.length === 1 ? '' : 's'} in the Library for ${projectName}`}>
            <p style={{ margin: '0 0 8px' }}>{picks.map((p) => p.name).join(', ')}</p>
            <div mix={askCss}>
              {a ? (
                <Button size="sm" variant="primary" icon={Lightbulb} disabled={act.busy !== '' || running} data-testid="picks-ask" mix={on('click', () => void ask(true))}>
                  Ask {a.name} which to use
                </Button>
              ) : null}
              <Button size="sm" disabled={act.busy !== ''} data-testid="picks-add" mix={on('click', () => void addPicks())}>
                {act.busy === 'addPicks' ? 'Adding...' : `Add ${picks.length === 1 ? 'it' : `all ${picks.length}`} for the whole team`}
              </Button>
              <Button size="sm" variant="ghost" mix={on('click', () => handle.props.onPicksDone())}>
                Forget the picks
              </Button>
            </div>
          </Notice>
        ) : null}
        {agents.length === 0 ? (
          <p mix={metaCss}>Add a team member first: recommendations come from an agent of the team.</p>
        ) : (
          <div mix={askCss}>
            <label>
              Ask{' '}
              <Select
                label="Agent to ask"
                size="sm"
                value={a?.id ?? ''}
                options={agents.map((x) => ({ value: x.id, label: `${x.name} (${x.role})` }))}
                onChange={(v) => set(() => (chosen = v))}
              />
            </label>
            <Button size="sm" icon={Lightbulb} disabled={act.busy !== '' || running} data-testid="recommend-ask" mix={on('click', () => void ask(false))}>
              {running ? 'Thinking...' : 'Recommend'}
            </Button>
          </div>
        )}
        {recs.loading && !recs.data ? <Loading /> : null}
        {running ? (
          <p mix={metaCss} role="status" data-testid="recommend-running">
            {names.get(latest.agentId) ?? 'The agent'} is looking at the project ({triggerText(latest.trigger)})...
          </p>
        ) : null}
        {latest && latest.status === 'failed' ? <ErrorNote error={`The last recommendation failed: ${latest.error || 'no reason given'}`} /> : null}
        {latest && latest.status === 'done' ? (
          <div data-testid="recommend-latest" style={{ display: 'grid', gap: '8px' }}>
            <p mix={metaCss}>
              From {names.get(latest.agentId) ?? latest.agentId}, {triggerText(latest.trigger)}, {formatDateTime(latest.finishedAt || latest.createdAt)}
              {latest.costUsd > 0 ? ` · $${latest.costUsd.toFixed(3)}` : ''}
            </p>
            {latest.summary ? <p data-testid="recommend-summary">{latest.summary}</p> : null}
            {latest.items.length === 0 ? <p mix={metaCss}>Nothing to change.</p> : <ul mix={listCss}>{latest.items.map((it) => item(latest.id, it))}</ul>}
            {openItems(latest).length === 0 && latest.items.length > 0 ? <p mix={metaCss}>Every recommendation was applied or dismissed.</p> : null}
            {latest.dropped.length > 0 ? (
              <details>
                <summary mix={metaCss}>
                  {latest.dropped.length} suggestion{latest.dropped.length === 1 ? ' was' : 's were'} left out (they named something that does not exist)
                </summary>
                <ul mix={metaCss}>
                  {latest.dropped.map((d, i) => (
                    <li key={i}>{d}</li>
                  ))}
                </ul>
              </details>
            ) : null}
          </div>
        ) : null}
        {earlier.length > 0 ? (
          <details>
            <summary mix={metaCss}>Earlier recommendations ({earlier.length})</summary>
            <ul mix={metaCss}>
              {earlier.map((r) => (
                <li key={r.id}>
                  {formatDateTime(r.createdAt)}, {triggerText(r.trigger)}: {r.status === 'done' ? `${r.items.length} item${r.items.length === 1 ? '' : 's'}` : r.status}
                </li>
              ))}
            </ul>
          </details>
        ) : null}
        {recs.data ? (
          <Checkbox
            label="Ask again as the project evolves"
            description="After every 5 completed tasks, at most once a day, within the budgets; the lead's new recommendations are announced in #general."
            checked={recs.data.auto}
            disabled={act.busy !== ''}
            onChange={(v) => void setAuto(v)}
          />
        ) : null}
      </Section>
    );
  };
}
