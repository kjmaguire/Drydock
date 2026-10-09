import { expect } from 'remix/assert';
import { afterEach, describe, it } from 'remix/test';
import type { Client } from '../../api/client.ts';
import type { McpLibraryServer, McpOAuthStatus } from '../../api/mcpClient.ts';
import { byLabel, byText, cleanup, mount, testServices, type, waitFor } from '../../test/render.tsx';
import { claude, devLicense, libServer, pay, projects, projServer, rev, tpl } from '../mcp/fixtures.ts';
import { DRAG_MIME } from './dnd.ts';
import { CATALOG } from '../project/wizard-fixtures.ts';
import { LibraryScreen } from './library-screen.tsx';

afterEach(cleanup);

type Calls = unknown[][];
/** A recording stand-in for one client method (a labelled test double). */
function rec<R>(calls: Calls, result: (...args: unknown[]) => R) {
  return async (...args: unknown[]) => {
    calls.push(args);
    return result(...args);
  };
}

function show(client: Partial<Client> = {}) {
  const s = testServices({
    licenseStatus: async () => devLicense,
    appInfo: async () => ({}) as never,
    projectList: async () => projects,
    adapterList: async () => [claude],
    mcpStatus: async () => ({ running: true, address: '127.0.0.1:1', adapters: [] }),
    mcpLibList: async () => [libServer({ projects: [] })],
    agentTemplateList: async () => [tpl()],
    mcpServerList: async (p: string) => (p === 'prj_1' ? [projServer({ agents: [{ agentId: 'agt_2', tools: ['*'] }] })] : []),
    agentList: async (p: string) => (p === 'prj_1' ? [pay, rev] : []),
    ...client,
  });
  s.workspace.start();
  const r = mount(<LibraryScreen />, s);
  return r;
}
type R = ReturnType<typeof show>;

const one = (r: R, sel: string) => waitFor(() => {
  const el = r.$(sel);
  if (!el) throw new Error(`no ${sel}`);
  return el as HTMLElement;
}, r);
const zone = (r: R, testId: string, text: string) =>
  waitFor(() => {
    const z = Array.from(r.$$(`[data-testid="${testId}"]`)).find((x) => x.textContent?.includes(text));
    if (!z) throw new Error(`no ${testId} with ${text}`);
    return z as HTMLElement;
  }, r);
const note = (r: R) => r.$('[data-testid="library-note"]')!.textContent ?? '';

/** Drag `from` and drop on `to`, as a browser does: the page learns what is dragged at dragstart, the data comes with the drop. */
async function dragDrop(r: R, from: Element, to: Element) {
  const dt = new DataTransfer();
  const ev = (type: string) => new DragEvent(type, { bubbles: true, cancelable: true, dataTransfer: dt });
  await r.act(() => from.dispatchEvent(ev('dragstart')));
  await r.act(() => {
    to.dispatchEvent(ev('dragenter'));
    to.dispatchEvent(ev('dragover'));
    to.dispatchEvent(ev('drop'));
    from.dispatchEvent(ev('dragend'));
  });
}

describe('Library page', () => {
  it('shows connected servers (secrets as names only), templates and the projects with their team', async () => {
    const r = show();
    const card = await one(r, '[data-testid="lib-mcp"]');
    expect(card.textContent).toContain('API_KEY');
    expect(card.textContent).not.toContain('sekret');
    expect(card.textContent).toContain('Not in any project yet.');
    expect((await one(r, '[data-testid="lib-template"]')).textContent).toContain('Payments dev');
    const alpha = await waitFor(() => {
      const a = r.$('[aria-label="Project Alpha"]');
      if (!a?.textContent?.includes('Rev')) throw new Error('not yet');
      return a;
    }, r);
    await waitFor(() => expect(alpha.querySelector('[aria-label="Servers Rev may use"]')!.textContent).toContain('Test server'), r);
    expect(r.$('h1')!.textContent).toContain('Library');
    // exactly one h3 per draggable card: the e2e suite drags by it
    expect(card.querySelectorAll('h3').length).toBe(1);
  });

  it('shows the prebuilt agents as a read-only section next to the templates', async () => {
    const r = show({ agentCatalogList: async () => CATALOG });
    const sec = await one(r, '[data-testid="library-prebuilt"]');
    await waitFor(() => expect(sec.querySelectorAll('[data-testid="prebuilt"]').length).toBe(CATALOG.length), r);
    expect(sec.textContent).toContain('Cloudflare Engineer');
    expect(sec.querySelectorAll('[data-testid="prebuilt"] button').length).toBe(0);
  });

  it('dropping a server on a project puts it there', async () => {
    const calls: Calls = [];
    const r = show({ mcpServerAdd: rec(calls, () => projServer({ projectId: 'prj_2' })) as never });
    const card = await one(r, '[data-testid="lib-mcp"]');
    const beta = await zone(r, 'lib-project', 'Beta');
    await dragDrop(r, card, beta);
    await waitFor(() => expect(calls).toEqual([['prj_2', 'tst']]), r);
    await waitFor(() => expect(note(r)).toContain('Test server is now in Beta.'), r);
  });

  it('marks the drop targets while a server is dragged, and only the ones that take it', async () => {
    const r = show();
    const card = await one(r, '[data-testid="lib-mcp"]');
    const payZone = await zone(r, 'lib-agent', 'Pay');
    const dt = new DataTransfer();
    await r.act(() => card.dispatchEvent(new DragEvent('dragstart', { bubbles: true, dataTransfer: dt })));
    expect(card.getAttribute('data-dragging')).toBe('true');
    expect(payZone.getAttribute('data-can-drop')).toBe('true');
    expect(r.$('[data-testid="lib-project"]')!.getAttribute('data-can-drop')).toBe('true');
    expect(r.container.textContent).toContain('drop here');
    await r.act(() => card.dispatchEvent(new DragEvent('dragend', { bubbles: true, dataTransfer: dt })));
    expect(payZone.getAttribute('data-can-drop')).toBe('false');

    // a template is not taken by an agent row
    const template = await one(r, '[data-testid="lib-template"]');
    await r.act(() => template.dispatchEvent(new DragEvent('dragstart', { bubbles: true, dataTransfer: new DataTransfer() })));
    expect(payZone.getAttribute('data-can-drop')).toBe('false');
    expect(r.$('[data-testid="lib-project"]')!.getAttribute('data-can-drop')).toBe('true');
  });

  it('dropping a server on an agent lets that agent use it', async () => {
    const calls: Calls = [];
    const r = show({ mcpServerGrant: rec(calls, () => projServer()) as never });
    const card = await one(r, '[data-testid="lib-mcp"]');
    const payZone = await zone(r, 'lib-agent', 'Pay');
    await dragDrop(r, card, payZone);
    await waitFor(() => expect(calls).toEqual([['prj_1', 'tst', 'agt_1', true]]), r);
    await waitFor(() => expect(note(r)).toContain('Pay can now use Test server.'), r);
  });

  it('dropping a template on a project adds the team member, and the note says what was granted and what was not', async () => {
    const calls: Calls = [];
    const r = show({
      agentTemplateApply: rec(calls, () => ({ agent: { ...pay, name: 'Payments dev' }, granted: ['tst'], skipped: ['docs: there is no MCP server "docs" in the library'] })) as never,
    });
    const card = await one(r, '[data-testid="lib-template"]');
    const beta = await zone(r, 'lib-project', 'Beta');
    await dragDrop(r, card, beta);
    await waitFor(() => expect(calls).toEqual([['tpl_1', 'prj_2']]), r);
    await waitFor(() => {
      expect(note(r)).toContain('Payments dev joined Beta');
      expect(note(r)).toContain('Test server');
      expect(note(r)).toContain('Not granted: docs');
    }, r);
  });

  it("ignores what does not belong (a foreign drop, a drop with nothing in it); a template dropped on an agent row lands in that agent's project", async () => {
    const grants: Calls = [];
    const applies: Calls = [];
    const adds: Calls = [];
    const r = show({
      mcpServerGrant: rec(grants, () => projServer()) as never,
      agentTemplateApply: rec(applies, () => ({ agent: pay, granted: [], skipped: [] })) as never,
      mcpServerAdd: rec(adds, () => projServer()) as never,
    });
    const template = await one(r, '[data-testid="lib-template"]');
    const payZone = await zone(r, 'lib-agent', 'Pay');
    await dragDrop(r, template, payZone); // an agent takes servers, not team members: the drop falls through to the project panel
    const project = r.$$('[data-testid="lib-project"]')[0]!;
    const foreign = new DataTransfer();
    foreign.setData('text/plain', 'https://example.com/x');
    await r.act(() => {
      project.dispatchEvent(new DragEvent('drop', { bubbles: true, cancelable: true, dataTransfer: foreign }));
      project.dispatchEvent(new DragEvent('drop', { bubbles: true, cancelable: true, dataTransfer: new DataTransfer() }));
      const bad = new DataTransfer();
      bad.setData(DRAG_MIME, '{"kind":"file","id":"x"}');
      project.dispatchEvent(new DragEvent('drop', { bubbles: true, cancelable: true, dataTransfer: bad }));
    });
    await new Promise((res) => setTimeout(res, 50));
    expect(grants).toEqual([]);
    expect(adds).toEqual([]);
    expect(applies).toEqual([['tpl_1', 'prj_1']]);
  });

  it('works without dragging: a project select and a button for servers and templates, and a Give button on an agent', async () => {
    const adds: Calls = [];
    const applies: Calls = [];
    const grants: Calls = [];
    const r = show({
      mcpServerAdd: rec(adds, () => projServer()) as never,
      agentTemplateApply: rec(applies, () => ({ agent: pay, granted: [], skipped: [] })) as never,
      mcpServerGrant: rec(grants, () => projServer()) as never,
    });
    const card = await one(r, '[data-testid="lib-mcp"]');
    await waitFor(() => byLabel<HTMLSelectElement>(card, 'Put Test server in project'), r);
    await r.act(() => type(byLabel<HTMLSelectElement>(card, 'Put Test server in project'), 'prj_2'));
    await r.act(() => byText(card, 'button', 'Put in project').click());
    await waitFor(() => expect(adds).toEqual([['prj_2', 'tst']]), r);
    const template = await one(r, '[data-testid="lib-template"]');
    await r.act(() => byText(template, 'button', 'Put in project').click());
    await waitFor(() => expect(applies).toEqual([['tpl_1', 'prj_1']]), r);
    const give = await waitFor(() => byText(r.container, 'button', 'Give to Pay'), r);
    await r.act(() => give.click());
    await waitFor(() => expect(grants).toEqual([['prj_1', 'tst', 'agt_1', true]]), r);
  });

  it('removes a server from an agent or from a project with the x on its chip', async () => {
    const grants: Calls = [];
    const removes: Calls = [];
    const r = show({ mcpServerGrant: rec(grants, () => projServer()) as never, mcpServerRemove: rec(removes, () => undefined) as never });
    const stop = await waitFor(() => byLabel<HTMLButtonElement>(r.container, 'Stop Rev using Test server'), r);
    await r.act(() => stop.click());
    await waitFor(() => expect(grants).toEqual([['prj_1', 'tst', 'agt_2', false]]), r);
    const out = await waitFor(() => byLabel<HTMLButtonElement>(r.container, 'Take Test server out of Alpha'), r);
    await waitFor(() => expect(out.disabled).toBe(false), r);
    await r.act(() => out.click());
    await waitFor(() => expect(removes).toEqual([['prj_1', 'tst']]), r);
    await waitFor(() => expect(note(r)).toContain('Test server was taken out of Alpha.'), r);
  });

  it('marks a tool read-only without sending or losing the stored secrets', async () => {
    const saves: Calls = [];
    const r = show({ mcpLibSave: rec(saves, () => libServer()) as never });
    const box = await waitFor(() => byLabel<HTMLInputElement>(r.container, 'echo only reads'), r);
    await r.act(() => box.click());
    await waitFor(() => expect(saves.length).toBe(1), r);
    expect(saves[0]![0]).toMatchObject({ id: 'tst', readOnlyTools: ['echo'], secretEnv: { API_KEY: '' } });
  });

  it('connects a server once: arguments one per line, a secret field that is a password', async () => {
    const saves: Calls = [];
    const r = show({ mcpLibList: async () => [], mcpLibSave: rec(saves, () => libServer()) as never });
    await waitFor(() => expect(r.container.textContent).toContain('No servers are connected yet.'), r);
    await r.act(() => byText(r.container, 'button', 'Connect an MCP server').click());
    const f = await one(r, '[data-testid="mcp-form"]');
    type(byLabel(f, 'Short id'), 'tst');
    type(byLabel(f, 'Name'), 'Test server');
    type(byLabel<HTMLTextAreaElement>(f, /^Command, one argument per line/), 'C:\\tools\\srv.exe\n--flag');
    await r.act(() => byText(f, 'button', 'Add a secret').click());
    await r.act(() => {
      type(byLabel(f, 'Secret name 1'), 'API_KEY');
      type(byLabel(f, 'Secret value 1'), 'abc123');
    });
    expect(byLabel<HTMLInputElement>(f, 'Secret value 1').type).toBe('password');
    await r.act(() => byText(f, 'button', 'Connect server').click());
    await waitFor(() => expect(saves.length).toBe(1), r);
    expect(saves[0]![0]).toMatchObject({ id: 'tst', display: 'Test server', transport: 'stdio', argv: ['C:\\tools\\srv.exe', '--flag'], secretEnv: { API_KEY: 'abc123' }, guard: true });
    await waitFor(() => expect(note(r)).toContain('is connected'), r);
    expect(r.$('[data-testid="mcp-form"]')).toBeNull();
  });

  it('says which environment line is wrong and saves nothing', async () => {
    const saves: Calls = [];
    const r = show({ mcpLibList: async () => [], mcpLibSave: rec(saves, () => libServer()) as never });
    await waitFor(() => byText(r.container, 'button', 'Connect an MCP server'), r);
    await r.act(() => byText(r.container, 'button', 'Connect an MCP server').click());
    const f = await one(r, '[data-testid="mcp-form"]');
    type(byLabel(f, 'Short id'), 'tst');
    type(byLabel(f, 'Name'), 'Test server');
    type(byLabel<HTMLTextAreaElement>(f, /^Command, one argument per line/), 'srv.exe');
    type(byLabel<HTMLTextAreaElement>(f, /^Environment/), 'NOEQUALS');
    await r.act(() => byText(f, 'button', 'Connect server').click());
    await waitFor(() => expect(f.querySelector('[role="alert"]')!.textContent).toContain('Environment: "NOEQUALS" is not NAME=value'), r);
    expect(f.querySelectorAll('[role="alert"]').length).toBe(1);
    expect(saves).toEqual([]);
  });

  it('disconnecting needs a confirmation that says what it removes', async () => {
    const deletes: Calls = [];
    const r = show({ mcpLibList: async () => [libServer({ projects: ['prj_1', 'prj_2'] })], mcpLibDelete: rec(deletes, () => undefined) as never });
    const card = await one(r, '[data-testid="lib-mcp"]');
    await r.act(() => byText(card, 'button', 'Disconnect').click());
    expect(deletes).toEqual([]);
    expect(card.textContent).toContain('leaves 2 project(s)');
    await r.act(() => byText(card, 'button', 'Yes, disconnect').click());
    await waitFor(() => expect(deletes).toEqual([['tst']]), r);
    await waitFor(() => expect(note(r)).toContain('Test server was disconnected.'), r);
  });

  it('deleting a template needs a confirmation; agents made from it stay', async () => {
    const deletes: Calls = [];
    const r = show({ agentTemplateDelete: rec(deletes, () => undefined) as never });
    const card = await one(r, '[data-testid="lib-template"]');
    await r.act(() => byText(card, 'button', 'Delete').click());
    expect(deletes).toEqual([]);
    expect(card.textContent).toContain('Agents already made from it stay.');
    await r.act(() => byText(card, 'button', 'Yes, delete').click());
    await waitFor(() => expect(deletes).toEqual([['tpl_1']]), r);
  });

  it('opens the template form for a new template (the form itself belongs to the team screen)', async () => {
    const r = show({ agentTemplateList: async () => [] });
    await waitFor(() => expect(r.container.textContent).toContain('No templates yet.'), r);
    expect(r.$('dialog[open]')).toBeNull();
    await r.act(() => byText(r.container, 'button', 'New agent template').click());
    await waitFor(() => expect(r.$('dialog[open]')!.textContent).toContain('New agent template'), r);
  });

  it('edits a server in a dialog: the id is fixed and the stored secret is kept unless replaced', async () => {
    const saves: Calls = [];
    const r = show({ mcpLibSave: rec(saves, () => libServer()) as never });
    const card = await one(r, '[data-testid="lib-mcp"]');
    await r.act(() => byText(card, 'button', 'Edit').click());
    const f = await one(r, '[data-testid="mcp-form"]');
    expect(f.getAttribute('aria-label')).toBe('Edit Test server');
    expect(byLabel<HTMLInputElement>(f, 'Short id').disabled).toBe(true);
    expect(byLabel<HTMLInputElement>(f, 'Secret name 1').value).toBe('API_KEY');
    expect(byLabel<HTMLInputElement>(f, 'Secret value 1').value).toBe('');
    await r.act(() => byText(f, 'button', 'Save changes').click());
    await waitFor(() => expect(saves.length).toBe(1), r);
    expect(saves[0]![0]).toMatchObject({ id: 'tst', secretEnv: { API_KEY: '' }, env: { MODE: 'x' } });
  });

  it('agent CLIs: Claude is verified, an unproven CLI is off until the human allows it, one with no mechanism cannot be allowed', async () => {
    const tries: Calls = [];
    const rows = (enabled: boolean) => [
      { adapter: 'claude', attached: true, canTry: false, trying: false },
      { adapter: 'codex', attached: enabled, canTry: true, trying: enabled, reason: 'codex: unverified' },
      { adapter: 'antigravity', attached: false, canTry: false, trying: false, reason: 'antigravity: unproven' },
    ];
    const r = show({
      mcpStatus: async () => ({ running: true, address: '', adapters: rows(false) }),
      mcpTrySet: rec(tries, (_a, enabled) => ({ running: true, address: '', adapters: rows(enabled as boolean) })) as never,
    });
    const list = await waitFor(() => {
      const l = r.$('[data-testid="cli-gateway"]');
      if (!l?.textContent?.includes('verified')) throw new Error('not yet');
      return l as HTMLElement;
    }, r);
    expect(() => byLabel(list, 'Let claude use the gateway')).toThrow();
    expect(() => byLabel(list, 'Let antigravity use the gateway')).toThrow();
    expect(list.textContent).toContain('antigravity: unproven');
    const box = byLabel<HTMLInputElement>(list, 'Let codex use the gateway');
    expect(box.checked).toBe(false);
    await r.act(() => box.click());
    await waitFor(() => expect(tries).toEqual([['codex', true]]), r);
  });

  describe('OAuth sign-in of a web server', () => {
    const oauthServer = (over: Partial<McpLibraryServer> = {}) =>
      libServer({ id: 'lin', display: 'Linear', transport: 'http', url: 'https://mcp.example.com/mcp', argv: undefined, env: undefined, secretEnv: [], oauth: true, oauthAt: 0, tools: [], projects: [], ...over });
    const status = (over: Partial<McpOAuthStatus> = {}): McpOAuthStatus => ({ id: 'lin', state: 'idle', signedIn: false, ...over });

    it('connecting a server with OAuth sends oauth true and no token', async () => {
      const saves: Calls = [];
      const r = show({ mcpLibList: async () => [], mcpLibSave: rec(saves, () => oauthServer()) as never });
      await waitFor(() => byText(r.container, 'button', 'Connect an MCP server'), r);
      await r.act(() => byText(r.container, 'button', 'Connect an MCP server').click());
      const f = await one(r, '[data-testid="mcp-form"]');
      type(byLabel(f, 'Short id'), 'lin');
      type(byLabel(f, 'Name'), 'Linear');
      await r.act(() => type(byLabel<HTMLSelectElement>(f, 'Kind'), 'http'));
      type(byLabel(f, 'Address'), 'https://mcp.example.com/mcp');
      await r.act(() => type(byLabel<HTMLSelectElement>(f, 'Sign-in'), 'oauth'));
      expect(() => byLabel(f, /Bearer token/)).toThrow();
      expect(f.textContent).toContain('press Sign in');
      await r.act(() => byText(f, 'button', 'Connect server').click());
      await waitFor(() => expect(saves.length).toBe(1), r);
      const sent = saves[0]![0] as Record<string, unknown>;
      expect(sent).toMatchObject({ id: 'lin', transport: 'http', url: 'https://mcp.example.com/mcp', oauth: true });
      expect(sent.bearerToken).toBeUndefined();
    });

    it("not signed in: Sign in asks the core to begin, then links the server's own sign-in page and can cancel", async () => {
      const begins: Calls = [];
      const cancels: Calls = [];
      let waiting = false;
      const url = 'https://auth.example.com/authorize?client_id=c1&state=s';
      const r = show({
        mcpLibList: async () => [oauthServer()],
        mcpOAuthStatus: async () => (waiting ? status({ state: 'waiting', url }) : status()),
        mcpOAuthBegin: rec(begins, () => ((waiting = true), status({ state: 'waiting', url }))) as never,
        mcpOAuthCancel: rec(cancels, () => ((waiting = false), status())) as never,
      });
      const panel = await one(r, '[data-testid="oauth-panel"]');
      await waitFor(() => expect(panel.textContent).toContain('not signed in'), r);
      expect(() => byText(panel, 'button', 'Sign out')).toThrow();
      await r.act(() => byText(panel, 'button', 'Sign in').click());
      await waitFor(() => expect(begins).toEqual([['lin']]), r);
      const link = (await one(r, '[data-testid="oauth-link"]')) as HTMLAnchorElement;
      expect(link.href).toBe(url);
      expect(link.target).toBe('_blank');
      expect(link.rel).toContain('noopener');
      const box = await one(r, '[data-testid="oauth-waiting"]');
      await r.act(() => byText(box, 'button', 'Cancel').click());
      await waitFor(() => expect(cancels).toEqual([['lin']]), r);
    });

    it('never links an address that is not safe to open', async () => {
      const r = show({ mcpLibList: async () => [oauthServer()], mcpOAuthStatus: async () => status({ state: 'waiting', url: 'javascript:alert(1)' }) });
      const box = await one(r, '[data-testid="oauth-waiting"]');
      expect(box.querySelector('[data-testid="oauth-link"]')).toBeNull();
      expect(box.querySelector('a')).toBeNull();
      expect(box.textContent).toContain('not safe to open');
    });

    it('signed in: says so and signs out', async () => {
      const outs: Calls = [];
      const r = show({
        mcpLibList: async () => [oauthServer({ oauthAt: 1760000000000 })],
        mcpOAuthStatus: async () => status({ signedIn: true, at: 1760000000000 }),
        mcpOAuthSignOut: rec(outs, () => status()) as never,
      });
      const panel = await one(r, '[data-testid="oauth-panel"]');
      await waitFor(() => byText(panel, 'button', 'Sign in again'), r);
      expect(panel.textContent).toContain('signed in');
      expect(panel.textContent).not.toContain('not signed in');
      await r.act(() => byText(panel, 'button', 'Sign out').click());
      await waitFor(() => expect(outs).toEqual([['lin']]), r);
    });

    it('shows the reason when the sign-in did not finish', async () => {
      const r = show({ mcpLibList: async () => [oauthServer()], mcpOAuthStatus: async () => status({ state: 'failed', error: 'the provider refused the sign-in: access_denied' }) });
      const err = await one(r, '[data-testid="oauth-error"]');
      expect(err.textContent).toContain('access_denied');
    });
  });
});
