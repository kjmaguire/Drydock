import { expect } from 'remix/assert';
import { afterEach, describe, it } from 'remix/test';
import type { Client } from '../../api/client.ts';
import type { AgentTemplate, AgentTemplateInput, SkillChoice } from '../../api/libraryClient.ts';
import type { McpLibraryServer } from '../../api/mcpClient.ts';
import type { AdapterInfo } from '../../api/types.ts';
import { expiredLicense, lic } from '../../test/fixtures.ts';
import { byLabel, byText, cleanup, mount, optionsOf, testServices, type, valueOf, waitFor } from '../../test/render.tsx';
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
const tplOf = (name: string): AgentTemplate => ({
  id: 'tpl_1',
  name,
  role: 'backend',
  purpose: '',
  adapter: 'claude',
  model: '',
  effort: '',
  permissionMode: '',
  providerId: '',
  mcpServers: [],
  skills: [],
  createdAt: 1,
  updatedAt: 1,
});

function setup(client: Partial<Client>, props: { initial?: AgentTemplate; adapters?: AdapterInfo[] } = {}) {
  const s = testServices({ projectList: async () => [], licenseStatus: async () => lic(), appInfo: async () => ({}) as never, agentTemplateSkillChoices: async () => [], ...client });
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
    const adapter = byLabel<HTMLElement>(form, 'Adapter');
    expect(optionsOf(adapter).map((o) => o.value)).toEqual(['claude', 'codex']);
    expect(valueOf(adapter)).toBe('claude');
    type(byLabel(form, 'Template name'), ' Payments dev ');
    type(byLabel(form, 'Role'), 'backend');
    type(byLabel<HTMLTextAreaElement>(form, 'Purpose (optional)'), 'Own the payments service.');
    type(byLabel(form, 'Model'), 'sonnet');
    type(byLabel<HTMLElement>(form, 'Effort'), 'high');
    r.root.flush();
    type(byLabel<HTMLElement>(form, 'Permission mode'), 'plan');
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
        skills: [],
        practices: true,
      },
    ]);
  });

  it('edits an existing template with its values and id, and an adapter without model selection sends none', async () => {
    const calls: AgentTemplateInput[] = [];
    const initial: AgentTemplate = { id: 'tpl_1', name: 'Old', role: 'tester', purpose: '', adapter: 'codex', model: 'x', effort: 'y', permissionMode: '', providerId: 'prov', mcpServers: [], skills: [], createdAt: 1, updatedAt: 1 };
    const { r } = setup({ agentTemplateSave: async (x) => (calls.push(x), saved(x)) }, { initial });
    const form = r.container.querySelector<HTMLElement>('[data-testid="template-form"]')!;
    expect(form.getAttribute('aria-label')).toBe('Edit Old');
    expect(valueOf(byLabel(form, 'Adapter'))).toBe('codex');
    expect(byLabel<HTMLInputElement>(form, 'Model').disabled).toBe(true);
    await r.act(() => byText(form, 'button', 'Save template').click());
    await waitFor(() => expect(calls.length).toBe(1), r);
    expect(calls[0]).toEqual({ id: 'tpl_1', name: 'Old', role: 'tester', purpose: '', adapter: 'codex', model: '', effort: '', permissionMode: '', providerId: 'prov', mcpServers: [], skills: [] });
  });

  const wrangler = { registry: 'cloudflare-skills', name: 'wrangler', ref: 'a18ffe2a99448ebff262fc87915b0fd14bab9b70' };
  const choices: SkillChoice[] = [
    { ...wrangler, description: 'Wrangler config', from: ['project gocalc', 'prebuilt Cloudflare Engineer'] },
    { registry: 'anthropic-skills', name: 'frontend-design', ref: '683bc88e56f3e09ba94f7055977f3d3aa499f202', description: '', from: ['prebuilt UI Engineer'] },
    { registry: 'vercel-agent-skills', name: 'web-design-guidelines', ref: '063bee94c3f4df8453406c830b0a7df0f2860278', description: 'UI review', from: ['prebuilt UI Engineer'] },
  ];

  it('keeps the skills of the template it edits: saving sends them back', async () => {
    const calls: AgentTemplateInput[] = [];
    const initial: AgentTemplate = { ...tplOf('Edge'), skills: [wrangler] };
    const { r } = setup({ agentTemplateSave: async (x) => (calls.push(x), saved(x)), agentTemplateSkillChoices: async () => choices }, { initial, adapters: [claude] });
    const form = r.container.querySelector<HTMLElement>('[data-testid="template-form"]')!;
    const field = form.querySelector<HTMLElement>('[data-testid="template-skills"]')!;
    expect(field.textContent).toContain('wrangler');
    expect(field.textContent).toContain('cloudflare-skills, pinned at a18ffe2a9944');
    type(byLabel(form, 'Template name'), 'Edge worker');
    r.root.flush();
    await r.act(() => byText(form, 'button', 'Save template').click());
    await waitFor(() => expect(calls.length).toBe(1), r);
    expect(calls[0]!.skills).toEqual([wrangler]);
    expect(calls[0]!.name).toBe('Edge worker');
  });

  it('adds a skill from those Drydock knows and removes one', async () => {
    const calls: AgentTemplateInput[] = [];
    const initial: AgentTemplate = { ...tplOf('Edge'), skills: [wrangler] };
    const { r } = setup({ agentTemplateSave: async (x) => (calls.push(x), saved(x)), agentTemplateSkillChoices: async () => choices }, { initial, adapters: [claude] });
    const form = r.container.querySelector<HTMLElement>('[data-testid="template-form"]')!;
    const pickList = await waitFor(() => byLabel<HTMLElement>(form, 'Add a skill'), r);
    // a skill the template already has is not offered again
    expect(optionsOf(pickList).map((o) => o.label)).toEqual([
      'frontend-design (anthropic-skills), from prebuilt UI Engineer',
      'web-design-guidelines (vercel-agent-skills), from prebuilt UI Engineer',
    ]);
    type(pickList, `vercel-agent-skills/web-design-guidelines@${choices[2]!.ref}`);
    r.root.flush();
    await r.act(() => byText(form, 'button', 'Add skill').click());
    await r.act(() => byText(form, 'button', 'Remove wrangler').click());
    expect(optionsOf(byLabel(form, 'Add a skill')).map((o) => o.value)).toEqual([
      `cloudflare-skills/wrangler@${wrangler.ref}`,
      `anthropic-skills/frontend-design@${choices[1]!.ref}`,
    ]);
    await r.act(() => byText(form, 'button', 'Save template').click());
    await waitFor(() => expect(calls.length).toBe(1), r);
    expect(calls[0]!.skills).toEqual([{ registry: 'vercel-agent-skills', name: 'web-design-guidelines', ref: choices[2]!.ref }]);
  });

  it('says when there is no other skill to add', async () => {
    const { r } = setup({ agentTemplateSkillChoices: async () => [choices[0]!] }, { initial: { ...tplOf('Edge'), skills: [wrangler] }, adapters: [claude] });
    await waitFor(() => expect(r.container.textContent).toContain('No other skill to add'), r);
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
