// A project's agent tools by role (decision P115, docs/mcp.md section 3): the table the gateway applies to every run of the
// project's agents, a row per role and a column per tool or kind of server, each cell changeable for this project (a changed
// cell is marked and can be put back); the switch that lets agents act (task, room, handoff); and the project's rules, CEL
// expressions that take tools away. A grant to a server (Agent tools above) still decides whether an agent reaches that server
// at all: the table only says which kinds of servers each role may reach.

import { css, on, ref, type Handle } from 'remix/component';
import { RotateCcw, Trash2 } from 'lucide';
import type { RolePolicyView } from '../../api/mcpClient.ts';
import { action, query, services, watch } from '../../state/services.tsx';
import { Button, IconButton } from '../../ui/button.tsx';
import { faint, muted } from '../../ui/data.tsx';
import { ErrorNote, Loading } from '../../ui/feedback.tsx';
import { errorAttrs, Field, Switch } from '../../ui/form.tsx';
import { Section, tableCard } from '../../ui/layout.tsx';
import { t } from '../../ui/tokens.ts';
import { cellLabel, changedColumns, columnNote, nextCell, ruleProblem, RULE_VARIABLES } from './role-tools.ts';

const tableCss = css({
  width: '100%',
  borderCollapse: 'collapse',
  fontSize: t.textSm,
  '& th, & td': { padding: '6px 8px', borderBottom: `1px solid ${t.border}`, textAlign: 'center', whiteSpace: 'nowrap' },
  '& th:first-child, & td:first-child': { textAlign: 'left', position: 'sticky', left: '0', background: t.bgSurface },
  '& thead th': { fontWeight: '500', color: t.textMuted, fontSize: t.textXs, verticalAlign: 'bottom' },
  '& thead th[data-off="true"]': { color: t.textFaint, textDecoration: 'line-through' },
  '& td[data-changed="true"]': { background: t.accentSoft },
  '& input': { margin: '0', width: '16px', height: '16px' },
});
// the box is hit through a label of 28 px around it (the density budget's smallest target)
const hitCss = css({ display: 'inline-flex', alignItems: 'center', justifyContent: 'center', minWidth: '28px', minHeight: '28px', cursor: 'pointer' });
const rowHead = css({ display: 'flex', alignItems: 'center', gap: t.s2, '& button': { marginLeft: 'auto' } });
const rulesCss = css({ display: 'grid', gap: t.s3, '& h3': { margin: '0', fontSize: t.textMd, fontWeight: '600' }, '& p': { margin: '0' } });
const ruleList = css({ display: 'grid', gap: t.s2, margin: '0', padding: '0', listStyle: 'none', '& li': { display: 'flex', alignItems: 'center', gap: t.s2, minWidth: '0' }, '& code': { overflowWrap: 'anywhere' } });
const formRow = css({ display: 'grid', gap: t.s2, gridTemplateColumns: 'minmax(160px, 1fr) minmax(240px, 2fr) auto', alignItems: 'end', '@media (max-width: 720px)': { gridTemplateColumns: '1fr' } });
const varsCss = css({ margin: '0', paddingLeft: t.s5, color: t.textMuted, fontSize: t.textSm, '& code': { color: t.text } });

export function RoleToolsPanel(handle: Handle<{ projectId: string }>) {
  const s = services(handle);
  watch(handle, s.workspace);
  const q = query(handle, {
    load: () => s.client.mcpRolePolicyGet(handle.props.projectId),
    key: () => handle.props.projectId,
    when: (e) => e.type === 'mcp.role_policy_changed' && (!e.projectId || e.projectId === handle.props.projectId),
  });
  const act = action(handle);
  let name = '';
  let expr = '';
  let problem = '';
  let rulesEl: HTMLElement | null = null;
  const set = (f: () => void) => {
    f();
    void handle.update();
  };
  const show = (v: RolePolicyView | undefined) => {
    if (v) q.set(v);
  };

  const toggle = async (role: string, column: string) => {
    const cell = q.data?.rows.find((r) => r.role === role)?.cells[column];
    if (!cell || act.busy) return;
    show(await act.run(`cell:${role}:${column}`, () => s.client.mcpRolePolicySet(handle.props.projectId, role, column, nextCell(cell))));
  };
  const resetRow = async (role: string) => {
    const v = q.data;
    if (!v || act.busy) return;
    show(
      await act.run(`reset:${role}`, async () => {
        let last = v;
        for (const col of changedColumns(v, role)) last = await s.client.mcpRolePolicySet(handle.props.projectId, role, col, null);
        return last;
      }),
    );
  };
  const setAct = async (yes: boolean) => {
    if (act.busy) return;
    const ok = await act.run('act', () => s.client.mcpActToolsSet(handle.props.projectId, yes));
    if (ok !== undefined) q.reload();
  };
  const addRule = async () => {
    problem = ruleProblem(name, expr);
    if (problem) {
      void handle.update();
      handle.queueTask(() => rulesEl?.querySelector<HTMLElement>('[aria-invalid="true"]')?.focus());
      return;
    }
    const v = await act.run('rule', () => s.client.mcpPolicyRuleAdd(handle.props.projectId, name.trim(), expr.trim()));
    if (v) {
      name = '';
      expr = '';
      show(v);
    }
  };

  return () => {
    const v = q.data;
    const ro = s.workspace.license.readOnly;
    return (
      <Section
        label="Agent tools by role"
        testId="role-tools"
        title="Agent tools by role"
        description="Which tools each role's agents get here, and which kinds of servers they may reach once a server is granted to them above. Every role may propose memory; you decide what is kept."
      >
        <ErrorNote error={q.error} what="Could not load the table" />
        <ErrorNote error={act.error} what="Not changed" />
        {!v ? (
          q.error ? null : <Loading />
        ) : (
          <>
            <Switch
              label={
                <span>
                  Let agents act <span mix={muted}>(update their task, post in rooms, hand off work)</span>
                </span>
              }
              checked={v.actTools}
              disabled={ro || !!act.busy}
              onChange={(yes) => void setAct(yes)}
            />
            <div mix={tableCard}>
              <table mix={tableCss} data-testid="role-table">
                <thead>
                  <tr>
                    <th scope="col">Role</th>
                    {v.columns.map((c) => {
                      const note = columnNote(v, c.key);
                      return (
                        <th key={c.key} scope="col" title={note ? `${c.help}. ${note}` : c.help} data-off={String(!!note)} data-column={c.key}>
                          {c.label}
                        </th>
                      );
                    })}
                  </tr>
                </thead>
                <tbody>
                  {v.rows.map((r) => {
                    const changed = changedColumns(v, r.role);
                    return (
                      <tr key={r.role} data-role={r.role}>
                        <th scope="row">
                          <span mix={rowHead}>
                            {r.label}
                            {changed.length > 0 ? (
                              <IconButton
                                icon={RotateCcw}
                                label={`Put ${r.label} back to the defaults`}
                                size={14}
                                disabled={ro || !!act.busy}
                                data-testid="role-reset"
                                mix={[on('click', () => void resetRow(r.role))]}
                              />
                            ) : null}
                          </span>
                        </th>
                        {v.columns.map((c) => {
                          const cell = r.cells[c.key];
                          return (
                            <td key={c.key} data-changed={String(!!cell?.changed)} data-column={c.key}>
                              <label mix={hitCss}>
                                <input
                                  type="checkbox"
                                  aria-label={cellLabel(r.label, c.label)}
                                  checked={!!cell?.allowed}
                                  disabled={ro || !!act.busy || !cell}
                                  mix={on('change', () => void toggle(r.role, c.key))}
                                />
                              </label>
                            </td>
                          );
                        })}
                      </tr>
                    );
                  })}
                </tbody>
              </table>
            </div>
            <p mix={faint}>A shaded cell differs from Drydock's table for that role.</p>
            <div mix={[rulesCss, ref((el) => (rulesEl = el as HTMLElement))]} data-testid="role-rules">
              <h3>Rules</h3>
              <p mix={muted}>A rule takes away every tool its expression is true for, in every run of this project. Rules cannot give a tool.</p>
              {v.rules.length > 0 ? (
                <ul mix={ruleList} aria-label="Rules">
                  {v.rules.map((r) => (
                    <li key={r.id} data-testid="role-rule">
                      <strong>{r.name}</strong>
                      <code>{r.expr}</code>
                      <IconButton
                        icon={Trash2}
                        label={`Remove the rule ${r.name}`}
                        size={14}
                        disabled={ro || !!act.busy}
                        mix={[on('click', async () => show(await act.run(`rm:${r.id}`, () => s.client.mcpPolicyRuleRemove(handle.props.projectId, r.id))))]}
                      />
                    </li>
                  ))}
                </ul>
              ) : (
                <p mix={faint} data-testid="no-rules">
                  No rules.
                </p>
              )}
              <form mix={[formRow, on('submit', (e) => (e.preventDefault(), void addRule()))]}>
                <Field label="Name" error={problem && !name.trim() ? problem : undefined}>
                  {(id) => (
                    <input
                      id={id}
                      value={name}
                      disabled={ro}
                      {...errorAttrs(id, problem && !name.trim() ? problem : undefined)}
                      mix={on('input', (e) => set(() => ((name = e.currentTarget.value), (problem = ''))))}
                    />
                  )}
                </Field>
                <Field label="Expression" error={problem && name.trim() ? problem : undefined}>
                  {(id) => (
                    <input
                      id={id}
                      value={expr}
                      disabled={ro}
                      spellcheck={false}
                      placeholder={'role == "qa" && column == "trackers"'}
                      {...errorAttrs(id, problem && name.trim() ? problem : undefined)}
                      mix={on('input', (e) => set(() => ((expr = e.currentTarget.value), (problem = ''))))}
                    />
                  )}
                </Field>
                <Button type="submit" size="sm" disabled={ro || !!act.busy}>
                  {act.busy === 'rule' ? 'Adding...' : 'Add rule'}
                </Button>
              </form>
              <ul mix={varsCss}>
                {RULE_VARIABLES.map((x) => (
                  <li key={x.name}>
                    <code>{x.name}</code>: {x.help}
                  </li>
                ))}
              </ul>
            </div>
          </>
        )}
      </Section>
    );
  };
}
