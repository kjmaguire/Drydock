import { expect } from 'remix/assert';
import { afterEach, describe, it } from 'remix/test';
import type { Client } from '../../api/client.ts';
import type { McpServerInput } from '../../api/mcpClient.ts';
import { byLabel, byText, cleanup, drain, mount, testServices, type, typeKeys, waitFor } from '../../test/render.tsx';
import { libServer } from './fixtures.ts';
import { ImportServers } from './import-panel.tsx';

afterEach(cleanup);

// TEST DOUBLES (labelled): the client below answers like the core's mcplib.save; mcplib.discover is the call that starts a
// server, so the stand-in counts every call it gets and the tests say it must get none.
function show(over: Partial<Client> = {}, existing: string[] = []) {
  const saves: McpServerInput[] = [];
  const discovers: string[] = [];
  const done: number[] = [];
  const s = testServices({
    mcpLibSave: (async (input: McpServerInput) => (saves.push(input), libServer({ id: input.id }))) as never,
    mcpLibDiscover: (async (id: string) => (discovers.push(id), libServer({ id }))) as never,
    ...over,
  });
  const r = mount(<ImportServers existingIds={existing} onDone={(n) => done.push(n)} onCancel={() => undefined} />, s);
  return { r, saves, discovers, done };
}
type T = ReturnType<typeof show>;

const FILE = {
  mcpServers: {
    'GitHub Local': { command: 'github-mcp-server', args: ['stdio', '--read-only'], env: { GITHUB_PERSONAL_ACCESS_TOKEN: 'ghp_x' } },
    docs: { url: 'https://learn.microsoft.com/api/mcp' },
  },
};

async function read(t: T, file: unknown = FILE) {
  type(byLabel<HTMLTextAreaElement>(t.r.container, 'Configuration (JSON)'), typeof file === 'string' ? file : JSON.stringify(file));
  await t.r.act(() => byText(t.r.container, 'button', 'Read servers').click());
  return waitFor(() => {
    const c = Array.from(t.r.$$('[data-testid="import-candidate"]')) as HTMLElement[];
    if (c.length === 0) throw new Error('not read yet');
    return c;
  }, t.r);
}
const box = (li: HTMLElement) => li.querySelector<HTMLInputElement>('input[type="checkbox"]')!;

describe('Import from a file (M24)', () => {
  it('keeps every key typed into the import box, and reads the whole file (H12)', async () => {
    const t = show();
    const json = JSON.stringify({ mcpServers: { docs: { url: 'https://learn.microsoft.com/api/mcp' } } });
    const box = byLabel<HTMLTextAreaElement>(t.r.container, 'Configuration (JSON)');
    await typeKeys(box, json, t.r);
    expect(box.value).toBe(json);
    await t.r.act(() => byText(t.r.container, 'button', 'Read servers').click());
    await waitFor(() => expect(t.r.$$('[data-testid="import-candidate"]').length).toBe(1), t.r);
  });

  it('a server that runs a program is not ticked and says so; one that reaches an address is ticked and shows the address', async () => {
    const t = show();
    const [gh, docs] = await read(t);
    expect(gh!.getAttribute('data-kind')).toBe('program');
    expect(box(gh!).checked).toBe(false);
    expect(gh!.querySelector('[data-testid="import-runs-program"]')!.textContent).toBe('Runs a program on this computer');
    // the badge is part of the box's name, so it is read when the box is reached
    expect(box(gh!).closest('label')!.textContent).toContain('Runs a program on this computer');
    expect(gh!.querySelector('[data-testid="import-command"]')!.textContent).toBe('github-mcp-server stdio --read-only');

    expect(docs!.getAttribute('data-kind')).toBe('address');
    expect(box(docs!).checked).toBe(true);
    expect(docs!.querySelector('[data-testid="import-runs-program"]')).toBeNull();
    expect(docs!.querySelector('[data-testid="import-address"]')!.textContent).toBe('https://learn.microsoft.com/api/mcp');

    // the page says why, and only one server is on the button
    expect(t.r.$('[data-testid="import-programs-note"]')!.textContent).toContain('One server in this file runs a program on this computer, so it is not ticked');
    expect(byText(t.r.container, 'button', 'Import 1 server')).not.toBeNull();
  });

  it('importing saves what is ticked and starts nothing: the server is never reached, however many are saved', async () => {
    const t = show();
    const [gh, docs] = await read(t);
    await t.r.act(() => byText(t.r.container, 'button', 'Import 1 server').click());
    await waitFor(() => expect(t.done).toEqual([1]), t.r);
    expect(t.saves).toEqual([{ id: 'docs', display: 'docs', transport: 'http', url: 'https://learn.microsoft.com/api/mcp' }]);
    await drain();
    expect(t.discovers).toEqual([]);
    // the person ticks the program themselves: it is saved then, and still not started
    expect(box(gh!).checked).toBe(false);
    await t.r.act(() => box(gh!).click());
    expect(box(gh!).checked).toBe(true);
    await t.r.act(() => box(docs!).click());
    await t.r.act(() => byText(t.r.container, 'button', 'Import 1 server').click());
    await waitFor(() => expect(t.saves.length).toBe(2), t.r);
    expect(t.saves[1]).toEqual({ id: 'githublo', display: 'GitHub Local', transport: 'stdio', argv: ['github-mcp-server', 'stdio', '--read-only'], secretEnv: { GITHUB_PERSONAL_ACCESS_TOKEN: 'ghp_x' } });
    await waitFor(() => expect(t.done).toEqual([1, 1]), t.r);
    await drain();
    expect(t.discovers).toEqual([]);
  });

  it('a file of programs only leaves every box clear and the button off until the person chooses', async () => {
    const t = show();
    const list = await read(t, { mcpServers: { a: { command: 'a.exe' }, b: { command: 'b.exe', args: ['-x'] } } });
    expect(list.map((li) => box(li).checked)).toEqual([false, false]);
    expect(t.r.$('[data-testid="import-programs-note"]')!.textContent).toContain('2 servers in this file run a program on this computer, so they are not ticked');
    expect(byText<HTMLButtonElement>(t.r.container, 'button', 'Import 0 servers').disabled).toBe(true);
    await t.r.act(() => box(list[1]!).click());
    expect(byText<HTMLButtonElement>(t.r.container, 'button', 'Import 1 server').disabled).toBe(false);
  });

  it('shows the whole command line, never cut: a long one is in a box with a most height that scrolls and the keyboard can reach', async () => {
    const t = show();
    const args = Array.from({ length: 400 }, (_, i) => `--option-${i}=value-${i}`);
    const [li] = await read(t, { mcpServers: { big: { command: 'C:\\Program Files\\Tools\\big.exe', args: [...args, 'TAIL-OF-THE-COMMAND'] } } });
    const cmd = li!.querySelector<HTMLElement>('[data-testid="import-command"]')!;
    // an argument with a space in it is quoted so where it ends can be read; every argument is there, the last one included
    expect(cmd.textContent!.startsWith('"C:\\Program Files\\Tools\\big.exe" --option-0=value-0 ')).toBe(true);
    expect(cmd.textContent!.endsWith(' --option-399=value-399 TAIL-OF-THE-COMMAND')).toBe(true);
    for (const a of args) expect(cmd.textContent).toContain(a);
    // a box with a most height, scrolling, named, and reachable by keyboard
    const style = getComputedStyle(cmd);
    expect(style.overflowY).toBe('auto');
    expect(style.maxHeight).toBe('120px');
    expect(cmd.scrollHeight).toBeGreaterThan(cmd.clientHeight);
    expect(cmd.getAttribute('tabindex')).toBe('0');
    expect(cmd.getAttribute('role')).toBe('group');
    expect(cmd.getAttribute('aria-label')).toBe('Command line of big');
    // and the end of it is reachable by scrolling
    cmd.scrollTop = cmd.scrollHeight;
    expect(cmd.scrollTop).toBeGreaterThan(0);
  });

  it('writes out the characters of a command that are not drawn, so it reads as what it is', async () => {
    const t = show();
    const [li] = await read(t, { mcpServers: { sneaky: { command: 'node', args: ['safe\u202e', 'run\u200b.js'] } } });
    const cmd = li!.querySelector('[data-testid="import-command"]')!.textContent!;
    expect(cmd).toBe('node safe\\u{202e} run\\u{200b}.js');
  });

  it('a save that fails is named, the others are saved, and still nothing is started', async () => {
    let n = 0;
    const t = show({
      mcpLibSave: (async (input: McpServerInput) => {
        if (++n === 1) throw new Error('the short id is taken');
        return libServer({ id: input.id });
      }) as never,
    });
    await read(t, { mcpServers: { one: { url: 'https://one.example/mcp' }, two: { url: 'https://two.example/mcp' } } });
    await t.r.act(() => byText(t.r.container, 'button', 'Import 2 servers').click());
    await waitFor(() => expect(t.r.container.textContent).toContain('one: the short id is taken'), t.r);
    expect(t.done).toEqual([]);
    await drain();
    expect(t.discovers).toEqual([]);
  });
});
