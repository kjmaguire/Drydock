// The project's join rules (decision P130, room-protocol s10 (c)): a rule a person makes adds an agent to every task room
// created after it, or only to those whose task names a file under a folder. The agent joins as an observer (it reads and
// answers only when mentioned) unless the person chooses active. Rules never reach channels, direct messages or rooms that
// exist already; the core checks every rule.

import { css, on, type Handle } from 'remix/component';
import { Trash2 } from 'lucide';
import type { Agent } from '../../api/types.ts';
import { action, query, services } from '../../state/services.tsx';
import { Button } from '../../ui/button.tsx';
import { ErrorNote } from '../../ui/feedback.tsx';
import { Field } from '../../ui/form.tsx';
import { tightList } from '../../ui/layout.tsx';
import { Select } from '../../ui/select.tsx';
import { t } from '../../ui/tokens.ts';

const cardCss = css({
  display: 'grid',
  gap: t.s3,
  padding: t.s4,
  borderRadius: t.rLg,
  background: t.bgApp,
  minWidth: '0',
  '& h3': { margin: '0', fontSize: t.textMd },
  '& .muted': { margin: '0', color: t.textMuted, fontSize: t.textSm },
});
const rowCss = css({ display: 'flex', alignItems: 'center', gap: t.s2, padding: '4px 8px', fontSize: t.textSm, '& .what': { flex: '1', minWidth: '0' } });
const formCss = css({ display: 'flex', flexWrap: 'wrap', gap: t.s2, alignItems: 'flex-end', '& > *': { flex: '1 1 140px', minWidth: '0' }, '& > button': { flex: 'none' } });

/** A rule in words: "Sec joins as an observer: tasks naming a file under auth/". */
export function ruleWords(agentName: string, pathPrefix: string | undefined, state: string): string {
  const as = state === 'active' ? 'an active member' : 'an observer';
  return `${agentName} joins as ${as}: ${pathPrefix ? `tasks naming a file under ${pathPrefix}` : 'every task'}`;
}

export function JoinRules(handle: Handle<{ projectId: string; agents: readonly Agent[] }>) {
  const s = services(handle);
  const rules = query(handle, {
    load: () => s.client.roomJoinRules(handle.props.projectId),
    when: (e) => e.type.startsWith('room.join_rule'),
    key: () => handle.props.projectId,
  });
  const act = action(handle);
  let agent = '';
  let prefix = '';
  let state = 'observer';
  const update = () => void handle.update();
  const name = (id: string) => handle.props.agents.find((a) => a.id === id)?.name ?? id;

  return () => {
    const { projectId, agents } = handle.props;
    const list = rules.data ?? [];
    const busy = act.busy !== '';
    return (
      <section mix={cardCss} aria-label="Join rules" data-testid="join-rules">
        <h3>Join rules</h3>
        <p class="muted">Add an agent to each new task room, or only to tasks that name a file under a folder.</p>
        <ErrorNote error={act.error ?? rules.error} />
        {list.length ? (
          <ul mix={tightList}>
            {list.map((r) => (
              <li key={r.id} mix={rowCss} data-testid="join-rule">
                <span class="what">{ruleWords(name(r.agentId), r.pathPrefix, r.state)}</span>
                <Button
                  size="sm"
                  variant="ghost"
                  icon={Trash2}
                  disabled={busy}
                  aria-label={`Remove the rule for ${name(r.agentId)}`}
                  mix={[on('click', () => void act.run('remove', async () => (await s.client.roomJoinRuleRemove(r.id), await rules.reload())))]}
                >
                  Remove
                </Button>
              </li>
            ))}
          </ul>
        ) : null}
        <div mix={formCss}>
          <Select
            label="Agent for the rule"
            value={agent}
            options={[{ value: '', label: 'Choose an agent...' }, ...agents.map((a) => ({ value: a.id, label: a.name }))]}
            onChange={(v) => {
              agent = v;
              update();
            }}
          />
          <Field label="Folder (optional)">
            {(id) => (
              <input
                id={id}
                value={prefix}
                placeholder="auth/"
                mix={[
                  on('input', (e) => {
                    prefix = (e.currentTarget as HTMLInputElement).value;
                    update();
                  }),
                ]}
              />
            )}
          </Field>
          <Select
            label="Joins as"
            value={state}
            options={[
              { value: 'observer', label: 'Observer' },
              { value: 'active', label: 'Active' },
            ]}
            onChange={(v) => {
              state = v;
              update();
            }}
          />
          <Button
            disabled={!agent || busy}
            mix={[
              on('click', () =>
                void act.run('add', async () => {
                  await s.client.roomJoinRuleAdd({ projectId, agentId: agent, pathPrefix: prefix.trim(), state });
                  agent = '';
                  prefix = '';
                  await rules.reload();
                }),
              ),
            ]}
          >
            {act.busy === 'add' ? 'Adding...' : 'Add rule'}
          </Button>
        </div>
      </section>
    );
  };
}
