// "Joins task rooms" on the agent's editor panel (X17): the project's join rule for this agent as one choice, never, every task,
// or tasks that touch a folder chosen from the repository's folders (repo.folders, directory names only). Choosing applies it
// at once (it is the project's rule, not a field of the agent): the core removes the agent's old rules and adds the new one.
// The agent joins as an observer (it reads and answers only when mentioned) unless the person chooses active.

import { css, on, type Handle } from 'remix/component';
import { action, query, services } from '../../state/services.tsx';
import { Button } from '../../ui/button.tsx';
import { ErrorNote } from '../../ui/feedback.tsx';
import { Field } from '../../ui/form.tsx';
import { Select } from '../../ui/select.tsx';
import { t } from '../../ui/tokens.ts';
import { joinChange, joinChoiceOf, normFolder, type JoinChoice, type JoinMode } from './join-choice.ts';

const boxCss = css({
  display: 'grid',
  gap: t.s2,
  '& .row': { display: 'flex', flexWrap: 'wrap', alignItems: 'flex-end', gap: t.s2, '& > *': { flex: '1 1 160px', minWidth: '0' }, '& > button': { flex: 'none' } },
  '& .muted': { margin: '0', color: t.textMuted, fontSize: t.textSm },
});

export function AgentJoinRule(handle: Handle<{ projectId: string; agentId: string; agentName: string }>) {
  const s = services(handle);
  const act = action(handle);
  const rules = query(handle, {
    load: () => s.client.roomJoinRules(handle.props.projectId),
    when: (e) => e.type.startsWith('room.join_rule'),
    key: () => handle.props.projectId,
  });
  const folders = query(handle, { load: () => s.client.repoFolders(handle.props.projectId), key: () => handle.props.projectId });
  /** What the person is choosing; undefined while it is what the rules say. */
  let draft: JoinChoice | undefined;
  const listId = `join-folders-${Math.random().toString(36).slice(2, 8)}`;

  const apply = (next: JoinChoice) =>
    void act.run('join', async () => {
      const ch = joinChange(rules.data ?? [], handle.props.agentId, next);
      for (const id of ch.remove) await s.client.roomJoinRuleRemove(id);
      if (ch.add) await s.client.roomJoinRuleAdd({ projectId: handle.props.projectId, agentId: handle.props.agentId, ...ch.add });
      draft = undefined;
      await rules.reload();
    });

  return () => {
    const saved = joinChoiceOf(rules.data ?? [], handle.props.agentId);
    const c = draft ?? saved;
    const busy = act.busy !== '';
    const set = (p: Partial<JoinChoice>) => {
      const next = { ...c, ...p };
      // never and every task apply at once; a folder applies with Set once one is named
      if (next.mode === 'folder') {
        draft = next;
        void handle.update();
      } else apply(next);
    };
    return (
      <div mix={boxCss} data-testid="agent-join-rule">
        <div class="row">
          <Select
            label="Joins task rooms"
            value={c.mode}
            disabled={busy || !rules.data}
            options={[
              { value: 'never', label: 'Never by rule' },
              { value: 'every', label: 'Every task' },
              { value: 'folder', label: 'Tasks that touch a folder' },
            ]}
            onChange={(v) => set({ mode: v as JoinMode })}
          />
          {c.mode !== 'never' ? (
            <Select
              label="Joins as"
              value={c.state}
              disabled={busy}
              options={[
                { value: 'observer', label: 'Observer' },
                { value: 'active', label: 'Active' },
              ]}
              onChange={(v) => set({ state: v })}
            />
          ) : null}
        </div>
        {c.mode === 'folder' ? (
          <div class="row">
            <Field label="Folder">
              {(id) => (
                <input
                  id={id}
                  list={listId}
                  value={c.folder}
                  placeholder="auth/"
                  autocomplete="off"
                  data-testid="agent-join-folder"
                  mix={[
                    on('input', (e) => {
                      draft = { ...c, folder: (e.currentTarget as HTMLInputElement).value };
                      void handle.update();
                    }),
                  ]}
                />
              )}
            </Field>
            <datalist id={listId}>
              {(folders.data ?? []).map((f) => (
                <option key={f} value={f} />
              ))}
            </datalist>
            <Button size="sm" disabled={busy || !normFolder(c.folder)} data-testid="agent-join-set" mix={[on('click', () => apply(c))]}>
              {act.busy === 'join' ? 'Setting...' : 'Set'}
            </Button>
          </div>
        ) : null}
        <p class="muted">
          {c.mode === 'never'
            ? `${handle.props.agentName} joins a task room only when the task names it or someone invites it.`
            : c.state === 'active'
              ? 'It takes turns in the rooms it joins. A rule reaches task rooms made after it.'
              : 'It reads the rooms it joins and answers only when mentioned. A rule reaches task rooms made after it.'}
        </p>
        <ErrorNote error={act.error ?? rules.error} what="Not saved" />
      </div>
    );
  };
}
