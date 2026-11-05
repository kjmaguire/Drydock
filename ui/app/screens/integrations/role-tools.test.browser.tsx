import { expect } from 'remix/assert';
import { afterEach, describe, it } from 'remix/test';
import type { Client } from '../../api/client.ts';
import type { RolePolicyView } from '../../api/mcpClient.ts';
import { byLabel, byText, cleanup, mount, testServices, type, waitFor } from '../../test/render.tsx';
import { RoleToolsPanel } from './role-tools.tsx';

afterEach(cleanup);

// the core's table, small: two roles, three columns
function table(): RolePolicyView {
  return {
    projectId: 'prj_1',
    columns: [
      { key: 'read_range', label: 'Read files', help: 'read_range: lines of a file', tools: ['read_range'] },
      { key: 'act', label: 'Act', help: 'task, room, handoff', tools: ['task', 'room', 'handoff'] },
      { key: 'docs', label: 'Docs servers', help: 'documentation servers' },
    ],
    rows: [
      { role: 'pm', label: 'PM', cells: { read_range: { allowed: false, default: false, changed: false }, act: { allowed: true, default: true, changed: false }, docs: { allowed: false, default: false, changed: false } } },
      { role: 'qa', label: 'QA', cells: { read_range: { allowed: true, default: true, changed: false }, act: { allowed: true, default: true, changed: false }, docs: { allowed: false, default: false, changed: false } } },
    ],
    rules: [],
    actTools: false,
    repoMap: true,
  };
}

/** A labelled test double of the core's table: it applies what the panel sends and answers with the table. */
function fakeCore() {
  const v = table();
  const calls: string[] = [];
  const client: Partial<Client> = {
    mcpRolePolicyGet: async () => structuredClone(v),
    mcpRolePolicySet: async (_p, role, column, allowed) => {
      calls.push(`${role}:${column}:${allowed}`);
      const cell = v.rows.find((x) => x.role === role)!.cells[column]!;
      cell.allowed = allowed === null ? cell.default : allowed;
      cell.changed = allowed !== null;
      return structuredClone(v);
    },
    mcpActToolsSet: async (_p, on) => {
      calls.push(`act:${on}`);
      v.actTools = on;
      return on;
    },
    mcpPolicyRuleAdd: async (_p, name, expr) => {
      if (!expr.trim().endsWith('==')) {
        v.rules.push({ id: `r${v.rules.length + 1}`, name, expr });
        return structuredClone(v);
      }
      throw new Error(`rule "${name}": ERROR: <input>:1:5: Syntax error`);
    },
    mcpPolicyRuleRemove: async (_p, id) => {
      v.rules = v.rules.filter((r) => r.id !== id);
      return structuredClone(v);
    },
  };
  return { client, calls, v };
}

describe('Agent tools by role (P115)', () => {
  it('shows the table, changes a cell for the project and puts the row back', async () => {
    const f = fakeCore();
    const r = mount(<RoleToolsPanel projectId="prj_1" />, testServices(f.client));
    const cell = await waitFor(() => byLabel<HTMLInputElement>(r.container, 'PM: Read files'), r);
    // the table is behind "Change what each role may use", closed while it is Drydock's (decision P144)
    const more = r.$('[data-testid="role-tools-more"]') as HTMLDetailsElement;
    expect([more.open, more.querySelector('summary')!.textContent]).toEqual([false, 'Change what each role may use']);
    expect(cell.checked).toBe(false);
    expect(byLabel<HTMLInputElement>(r.container, 'QA: Read files').checked).toBe(true);
    // the act column says it gives nothing while agents may not act
    expect(r.$('th[data-column="act"]')!.getAttribute('data-off')).toBe('true');
    await r.act(() => cell.click());
    await waitFor(() => expect(byLabel<HTMLInputElement>(r.container, 'PM: Read files').checked).toBe(true), r);
    expect(f.calls).toEqual(['pm:read_range:true']);
    expect(r.$('tr[data-role="pm"] td[data-column="read_range"]')!.getAttribute('data-changed')).toBe('true');
    // a changed cell opens it, so the change is in sight
    await waitFor(() => expect((r.$('[data-testid="role-tools-more"]') as HTMLDetailsElement).open).toBe(true), r);
    // clicking it again sends the default back as null
    await r.act(() => byLabel<HTMLInputElement>(r.container, 'PM: Read files').click());
    await waitFor(() => expect(f.calls).toEqual(['pm:read_range:true', 'pm:read_range:null']), r);
    // a changed row is put back in one go
    await r.act(() => byLabel<HTMLInputElement>(r.container, 'QA: Docs servers').click());
    const reset = await waitFor(() => (r.$('tr[data-role="qa"] [data-testid="role-reset"]') as HTMLButtonElement), r);
    await r.act(() => reset.click());
    await waitFor(() => expect(f.calls.slice(-2)).toEqual(['qa:docs:true', 'qa:docs:null']), r);
    await waitFor(() => expect(r.$('tr[data-role="qa"] [data-testid="role-reset"]')).toBe(null), r);
  });

  it('lets agents act by the project switch, and adds and removes a rule with the core refusal shown', async () => {
    const f = fakeCore();
    const r = mount(<RoleToolsPanel projectId="prj_1" />, testServices(f.client));
    const sw = await waitFor(() => byLabel<HTMLButtonElement>(r.container, /Let agents act/), r);
    await r.act(() => sw.click());
    await waitFor(() => expect(f.calls).toEqual(['act:true']), r);
    await waitFor(() => expect(r.$('th[data-column="act"]')!.getAttribute('data-off')).toBe('false'), r);

    // the page checks the form first: no name, nothing is sent
    await r.act(() => byText(r.container, 'button', 'Add rule').click());
    await waitFor(() => expect(r.$('[data-testid="role-rules"] [role="alert"]')!.textContent).toContain('name'), r);
    type(byLabel(r.container, 'Name'), 'no trackers for QA');
    type(byLabel(r.container, 'Expression'), 'role ==');
    // the core's own check of the expression: refused with the reason
    await r.act(() => byText(r.container, 'button', 'Add rule').click());
    await waitFor(() => expect(r.container.textContent).toContain('Syntax error'), r);
    type(byLabel(r.container, 'Expression'), 'role == "qa" && column == "trackers"');
    await r.act(() => byText(r.container, 'button', 'Add rule').click());
    const rule = await waitFor(() => r.$('[data-testid="role-rule"]')!, r);
    expect(rule.textContent).toContain('role == "qa" && column == "trackers"');
    await r.act(() => byLabel<HTMLButtonElement>(r.container, 'Remove the rule no trackers for QA').click());
    await waitFor(() => expect(r.$('[data-testid="no-rules"]')).not.toBe(null), r);
  });
});
