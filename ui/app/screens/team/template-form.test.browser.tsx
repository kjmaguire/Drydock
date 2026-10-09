import { expect } from 'remix/assert';
import { afterEach, describe, it } from 'remix/test';
import type { Client } from '../../api/client.ts';
import type { AgentTemplate, AgentTemplateInput } from '../../api/libraryClient.ts';
import type { McpLibraryServer } from '../../api/mcpClient.ts';
import type { AdapterInfo } from '../../api/types.ts';
import { expiredLicense, lic } from '../../test/fixtures.ts';
import { byLabel, byText, cleanup, mount, testServices, type, waitFor } from '../../test/render.tsx';
import { TemplateForm } from './template-form.tsx';

afterEach(cleanup);

const claude: AdapterInfo = {
  id: 'claude',
  detection: { adapter: 'claude', installed: true, status: 'ready' },
  capabilities: { modelSelection: true, effortControl: true, efforts: ['low', 'medium', 'high'], models: ['opus', 'sonnet'], permissionModes: ['acceptEdits', 'plan'] } as AdapterInfo['capabilities'],
};
const codex: AdapterInfo = { id: 'codex', detection: { adapter: 'codex', installed: false, status: 'not_installed' }, capabilities: {} as AdapterInfo['capabilities'] };
const server = { id: 'docs', display: 'Docs server', transport: 'stdio', secretEnv: [], hasToken: false } as unknown as McpLibraryServer;
const saved = (x: AgentTemplateInput): AgentTemplate => ({ ...x, id: x.id ?? 'tpl_new', createdAt: 1, updatedAt: 1 });

function setup(client: Partial<Client>, props: { initial?: AgentTemplate; adapters?: AdapterInfo[] } = {}) {
  const s = testServices({ projectList: async () => [], licenseStatus: async () => lic(), appInfo: async () => ({}) as never, ...client });
  s.workspace.start();
  const done: AgentTemplate[] = [];
  let cancelled = 0;
  const r = mount(
    <TemplateForm initial={props.initial} adapters={props.adapters ?? [codex, claude]} servers={[server]} onDone={(t) => done.push(t)} onCancel={() => void cancelled++} />,
    s,
  );
  return { r, done, cancelled: () => cancelled };
}

describe('TemplateForm', () => {
  it('creates a template with the CLI, model, effort, permission mode and servers, ready adapters first', async () => {
    const calls: AgentTemplateInput[] = [];
    const { r, done } = setup({ agentTemplateSave: async (x) => (calls.push(x), saved(x)) });
    const form = r.container.querySelector<HTMLElement>('[data-testid="template-form"]')!;
    expect(form.getAttribute('aria-label')).toBe('New agent template');
    const adapter = byLabel<HTMLSelectElement>(form, 'Adapter');
    expect(Array.from(adapter.options).map((o) => o.value)).toEqual(['claude', 'codex']);
    expect(adapter.value).toBe('claude');
    type(byLabel(form, 'Template name'), ' Payments dev ');
    type(byLabel(form, 'Role'), 'backend');
    type(byLabel<HTMLTextAreaElement>(form, 'Purpose (optional)'), 'Own the payments service.');
    type(byLabel(form, 'Model'), 'sonnet');
    type(byLabel<HTMLSelectElement>(form, 'Effort'), 'high');
    r.root.flush();
    type(byLabel<HTMLSelectElement>(form, 'Permission mode'), 'plan');
    await r.act(() => byLabel<HTMLInputElement>(form, 'Template gets Docs server').click());
    await r.act(() => byText(form, 'button', 'Create template').click());
    await waitFor(() => expect(done.length).toBe(1), r);
    expect(calls).toEqual([
      {
        name: 'Payments dev',
        role: 'backend',
        purpose: 'Own the payments service.',
        adapter: 'claude',
        model: 'sonnet',
        effort: 'high',
        permissionMode: 'plan',
        providerId: '',
        mcpServers: ['docs'],
        practices: true,
      },
    ]);
  });

  it('edits an existing template with its values and id, and an adapter without model selection sends none', async () => {
    const calls: AgentTemplateInput[] = [];
    const initial: AgentTemplate = { id: 'tpl_1', name: 'Old', role: 'tester', purpose: '', adapter: 'codex', model: 'x', effort: 'y', permissionMode: '', providerId: 'prov', mcpServers: [], createdAt: 1, updatedAt: 1 };
    const { r } = setup({ agentTemplateSave: async (x) => (calls.push(x), saved(x)) }, { initial });
    const form = r.container.querySelector<HTMLElement>('[data-testid="template-form"]')!;
    expect(form.getAttribute('aria-label')).toBe('Edit Old');
    expect(byLabel<HTMLSelectElement>(form, 'Adapter').value).toBe('codex');
    expect(byLabel<HTMLInputElement>(form, 'Model').disabled).toBe(true);
    await r.act(() => byText(form, 'button', 'Save template').click());
    await waitFor(() => expect(calls.length).toBe(1), r);
    expect(calls[0]).toEqual({ id: 'tpl_1', name: 'Old', role: 'tester', purpose: '', adapter: 'codex', model: '', effort: '', permissionMode: '', providerId: 'prov', mcpServers: [] });
  });

  it('shows the core refusal and does not finish', async () => {
    const { r, done } = setup({
      agentTemplateSave: async () => {
        throw new Error('a template named Payments dev already exists');
      },
    });
    const form = r.container.querySelector<HTMLElement>('[data-testid="template-form"]')!;
    type(byLabel(form, 'Template name'), 'Payments dev');
    type(byLabel(form, 'Role'), 'backend');
    r.root.flush();
    await r.act(() => byText(form, 'button', 'Create template').click());
    await waitFor(() => expect(form.querySelector('[role="alert"]')!.textContent).toContain('already exists'), r);
    expect(done.length).toBe(0);
  });

  it('is turned off while read-only, and Cancel still works', async () => {
    const { r, cancelled } = setup({ licenseStatus: async () => expiredLicense });
    const form = r.container.querySelector<HTMLElement>('[data-testid="template-form"]')!;
    type(byLabel(form, 'Template name'), 'x');
    type(byLabel(form, 'Role'), 'y');
    await waitFor(() => expect(byText<HTMLButtonElement>(form, 'button', 'Create template').disabled).toBe(true), r);
    expect(form.querySelector('[data-testid="readonly-note"]')).toBeTruthy();
    await r.act(() => byText(form, 'button', 'Cancel').click());
    expect(cancelled()).toBe(1);
  });

  it('says when no servers are connected', () => {
    const s = testServices({ projectList: async () => [], licenseStatus: async () => lic(), appInfo: async () => ({}) as never });
    const r = mount(<TemplateForm adapters={[claude]} servers={[]} onDone={() => undefined} onCancel={() => undefined} />, s);
    expect(r.container.textContent).toContain('No servers are connected yet.');
  });
});
