import { expect } from 'remix/assert';
import { afterEach, describe, it } from 'remix/test';
import type { Client } from '../../api/client.ts';
import type { Connection, CustomCLIs } from '../../api/types.ts';
import { byLabel, byText, cleanup, errorOf, mount, testServices, type, waitFor } from '../../test/render.tsx';
import { AddCustomCLI, CustomCLIPanel } from './custom-cli.tsx';

afterEach(cleanup);

const files: CustomCLIs = { dir: 'C:\\Users\\k\\AppData\\Roaming\\Drydock\\adapters\\custom', files: [{ id: 'mine', path: 'C:\\Users\\k\\AppData\\Roaming\\Drydock\\adapters\\custom\\mine.yaml' }] };

describe('A custom CLI on the Agents page (P115)', () => {
  it('starts from the template for the id typed, sends the manifest, and shows the core refusal', async () => {
    const sent: string[] = [];
    let refuse = true;
    const added: Connection[] = [];
    const client: Partial<Client> = {
      connectionCustomTemplate: async (id) => `meta: {schema: 1, id: ${id}, display_name: ${id}, kind: agent_cli, custom: true}\n`,
      connectionCustomAdd: async (yaml) => {
        sent.push(yaml);
        if (refuse) throw new Error('manifest "mine" is invalid: binaries.native must list at least one executable');
        return { adapter: 'mine', key: 'mine', custom: true, usable: false, status: 'ready', installed: true } as unknown as Connection;
      },
    };
    const r = mount(<AddCustomCLI open dir={files.dir} taken={['claude', 'codex']} onClose={() => {}} onAdded={(c) => added.push(c)} />, testServices(client));
    const tpl = await waitFor(() => (r.$('[data-testid="custom-cli-template"]') as HTMLButtonElement), r);
    expect(r.container.textContent).toContain('adapters\\custom');
    // the id is checked on the page first
    type(byLabel(r.container, 'Id'), 'claude');
    await r.act(() => tpl.click());
    await waitFor(() => expect(errorOf(byLabel(r.container, 'Id'))).toContain('taken'), r);
    type(byLabel(r.container, 'Id'), 'mine');
    await r.act(() => tpl.click());
    const yaml = await waitFor(() => {
      const el = (r.$('[data-testid="custom-cli-yaml"]') as HTMLTextAreaElement);
      if (!el.value.includes('id: mine')) throw new Error('no template yet');
      return el;
    }, r);
    await r.act(() => (r.$('[data-testid="custom-cli-add"]') as HTMLButtonElement).click());
    await waitFor(() => expect(r.container.textContent).toContain('binaries.native must list'), r);
    refuse = false;
    type(yaml, yaml.value + 'binaries: {native: [my-cli.exe]}\n');
    await r.act(() => (r.$('[data-testid="custom-cli-add"]') as HTMLButtonElement).click());
    await waitFor(() => expect(added.map((c) => c.adapter)).toEqual(['mine']), r);
    expect(sent.length).toBe(2);
    expect(sent[1]).toContain('binaries: {native: [my-cli.exe]}');
  });

  it('says where the manifest is and removes the CLI after a confirmation, showing why when it is refused', async () => {
    let removed = 0;
    let refuse = true;
    const client: Partial<Client> = {
      connectionCustomRemove: async () => {
        if (refuse) throw new Error('1 agent runs on mine: give them another CLI or remove them first');
        return { dir: files.dir, files: [] };
      },
    };
    const r = mount(<CustomCLIPanel adapter="mine" files={files} readOnly={false} onRemoved={() => removed++} />, testServices(client));
    expect(r.container.textContent).toContain('mine.yaml');
    await r.act(() => (r.$('[data-testid="custom-cli-remove"]') as HTMLButtonElement).click());
    await r.act(() => (r.$('[data-testid="custom-cli-remove-confirm"]') as HTMLButtonElement).click());
    await waitFor(() => expect(r.container.textContent).toContain('1 agent runs on mine'), r);
    expect(removed).toBe(0);
    refuse = false;
    await r.act(() => byText(r.container, 'button', 'Remove mine').click());
    await waitFor(() => expect(removed).toBe(1), r);
  });
});
