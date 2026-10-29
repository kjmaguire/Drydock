// A task's agent tools (decision P115): what its agent gets for this task through Drydock's gateway, and a way to narrow that to
// fewer tools for this task only. Narrowing never adds a tool: a tool the role table, the agent's grants or the project's rules
// do not give stays out whatever is ticked here.

import { css, on, type Handle } from 'remix/component';
import type { TaskToolsView } from '../../api/mcpClient.ts';
import { action, query, services, watch } from '../../state/services.tsx';
import { Button } from '../../ui/button.tsx';
import { faint, muted } from '../../ui/data.tsx';
import { ErrorNote, Loading } from '../../ui/feedback.tsx';
import { Checkbox } from '../../ui/form.tsx';
import { t } from '../../ui/tokens.ts';

const bodyCss = css({ display: 'grid', gap: t.s3, minWidth: '0', fontSize: t.textMd, '& p': { margin: '0' } });
const listCss = css({ display: 'grid', gridTemplateColumns: 'repeat(auto-fill, minmax(200px, 1fr))', gap: `${t.s1} ${t.s3}` });
const rowCss = css({ display: 'flex', gap: t.s2, alignItems: 'center', flexWrap: 'wrap' });
const toolsCss = css({ display: 'flex', flexWrap: 'wrap', gap: '6px', '& code': { fontSize: t.textXs } });

export function TaskTools(handle: Handle<{ taskId: string }>) {
  const s = services(handle);
  watch(handle, s.workspace);
  const q = query(handle, {
    load: () => s.client.taskMcpToolsGet(handle.props.taskId),
    key: () => handle.props.taskId,
    when: (e) => (e.type === 'task.mcp_tools_changed' && e.taskId === handle.props.taskId) || e.type === 'mcp.role_policy_changed' || e.type.startsWith('mcp.server'),
  });
  const act = action(handle);
  let editing = false;
  let picked = new Set<string>();
  const set = (f: () => void) => {
    f();
    void handle.update();
  };
  const edit = (v: TaskToolsView) =>
    set(() => {
      editing = true;
      picked = new Set(v.narrowed ?? v.choices.map((c) => c.name));
    });
  const save = async (tools: string[] | null) => {
    const v = await act.run('save', () => s.client.taskMcpToolsSet(handle.props.taskId, tools));
    if (v) {
      q.set(v);
      set(() => (editing = false));
    }
  };

  return () => {
    const v = q.data;
    const ro = s.workspace.license.readOnly;
    if (!v) {
      return (
        <div mix={bodyCss}>
          <ErrorNote error={q.error} what="Could not read the task's tools" />
          {q.error ? null : <Loading />}
        </div>
      );
    }
    return (
      <div mix={bodyCss} data-testid="task-tools" data-narrowed={String(v.narrowed !== null)}>
        <ErrorNote error={act.error} what="Not changed" />
        {v.agentId ? (
          v.agentTools.length > 0 ? (
            <div mix={toolsCss} aria-label="Tools its agent gets" data-testid="task-agent-tools">
              {v.agentTools.map((x) => (
                <code key={x}>{x}</code>
              ))}
            </div>
          ) : (
            <p mix={muted}>Its agent gets no tools from Drydock for this task (its CLI or its route gets none).</p>
          )
        ) : (
          <p mix={muted}>No agent is assigned yet.</p>
        )}
        <p mix={faint}>
          {v.narrowed === null ? 'Not narrowed: the agent gets what its role and grants give.' : `Narrowed to ${v.narrowed.length} of the project's tools for this task.`}
        </p>
        {editing ? (
          <form mix={on('submit', (e) => (e.preventDefault(), void save([...picked])))}>
            <div mix={listCss} role="group" aria-label="Tools this task keeps">
              {v.choices.map((c) => (
                <Checkbox
                  key={c.name}
                  label={c.group === 'server' ? `${c.label} (every tool)` : c.label}
                  checked={picked.has(c.name)}
                  disabled={ro}
                  onChange={(keep) => set(() => (keep ? picked.add(c.name) : picked.delete(c.name)))}
                />
              ))}
            </div>
            <div mix={rowCss}>
              <Button type="submit" variant="primary" size="sm" disabled={ro || !!act.busy}>
                {act.busy === 'save' ? 'Saving...' : 'Keep only these'}
              </Button>
              <Button size="sm" mix={on('click', () => set(() => (editing = false)))}>
                Cancel
              </Button>
            </div>
          </form>
        ) : (
          <div mix={rowCss}>
            <Button size="sm" disabled={ro} data-testid="task-tools-edit" mix={on('click', () => edit(v))}>
              Narrow for this task...
            </Button>
            {v.narrowed !== null ? (
              <Button size="sm" variant="ghost" disabled={ro || !!act.busy} data-testid="task-tools-clear" mix={on('click', () => void save(null))}>
                Stop narrowing
              </Button>
            ) : null}
          </div>
        )}
      </div>
    );
  };
}
