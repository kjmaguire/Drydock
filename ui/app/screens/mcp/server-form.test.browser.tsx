import { expect } from 'remix/assert';
import { afterEach, describe, it } from 'remix/test';
import type { Client } from '../../api/client.ts';
import type { McpLibraryServer, McpServerInput } from '../../api/mcpClient.ts';
import { byLabel, byText, cleanup, errorOf, mount, testServices, type, waitFor } from '../../test/render.tsx';
import { devLicense, libServer } from './fixtures.ts';
import { McpServerForm } from './server-form.tsx';
import { envLineError, secretNameError } from './server-input.ts';

afterEach(cleanup);

// TEST DOUBLES (labelled): a stand-in client that records every save the form asks for and answers with a library server.
function show(client: Partial<Client> = {}, initial?: McpLibraryServer) {
  const saves: McpServerInput[] = [];
  const done: McpLibraryServer[] = [];
  const s = testServices({ licenseStatus: async () => devLicense, mcpLibSave: async (i) => (saves.push(i), libServer()), ...client });
  s.workspace.start();
  const r = mount(<McpServerForm initial={initial} onDone={(x) => done.push(x)} onCancel={() => undefined} />, s);
  const form = r.container.querySelector('form');
  if (!form) throw new Error('no form');
  return { r, saves, done, form };
}
type Shown = ReturnType<typeof show>;

/** A new program server with its three required fields filled in. */
function filled(client: Partial<Client> = {}) {
  const h = show(client);
  type(byLabel(h.form, 'Short id'), 'tst');
  type(byLabel(h.form, 'Name'), 'Test server');
  type(byLabel<HTMLTextAreaElement>(h.form, /^Command, one argument per line/), 'srv.exe');
  return h;
}
const envBox = (h: Shown) => byLabel<HTMLTextAreaElement>(h.form, /^Environment/);
const secretName = (h: Shown, n: number) => byLabel(h.form, `Secret name ${n}`);
const secretValue = (h: Shown, n: number) => byLabel(h.form, `Secret value ${n}`);
const addSecret = (h: Shown) => h.r.act(() => byText(h.form, 'button', 'Add a secret').click());
const connect = (h: Shown) => h.r.act(() => byText(h.form, 'button', 'Connect server').click());
const alerts = (h: Shown) => Array.from(h.form.querySelectorAll('[role="alert"]')).map((x) => x.textContent ?? '');

/** The fields of the form that are marked invalid. */
const invalidFields = (h: Shown): string[] => Array.from(h.form.querySelectorAll('[aria-invalid="true"]')).map((x) => x.getAttribute('aria-label') ?? x.id);

/** Every element of the form, other than the fields the person types in, that holds `needle` in an attribute or in its own text. */
const holds = (h: Shown, needle: string) =>
  Array.from(h.form.querySelectorAll('*'))
    .filter((el) => !el.matches('input, textarea'))
    .filter((el) => Array.from(el.attributes).some((a) => a.value.includes(needle)) || (el.children.length === 0 && (el.textContent ?? '').includes(needle)))
    .map((el) => el.outerHTML);

describe('the MCP server form checks the names of environment variables (L38)', () => {
  it('refuses a pasted NAME=value in a secret name, says which row, sends nothing, and repeats none of it', async () => {
    const h = filled();
    await addSecret(h);
    type(secretName(h, 1), 'API_KEY=sk-live-0123456789abcdef');
    type(secretValue(h, 1), 'a-value');
    await connect(h);
    await waitFor(() => expect(errorOf(secretName(h, 1))).toBe(secretNameError(1)), h.r);
    expect(h.saves).toEqual([]);
    expect(alerts(h)).toEqual([secretNameError(1)]);
    // the keyboard is on the field that is wrong, and the value field is not blamed
    expect(document.activeElement).toBe(secretName(h, 1));
    expect(secretValue(h, 1).getAttribute('aria-invalid')).toBeNull();
    for (const piece of ['sk-live', '0123456789abcdef', 'API_KEY=', 'a-value']) expect(holds(h, piece)).toEqual([]);
    // the name alone is accepted, and the value goes with it
    type(secretName(h, 1), 'API_KEY');
    await waitFor(() => expect(errorOf(secretName(h, 1))).toBe(null), h.r);
    expect(alerts(h)).toEqual([]);
    await connect(h);
    await waitFor(() => expect(h.saves.length).toBe(1), h.r);
    expect(h.saves[0]!.secretEnv).toEqual({ API_KEY: 'a-value' });
    await waitFor(() => expect(h.done.length).toBe(1), h.r);
  });

  it('refuses a line of the Environment box that is not NAME=value, counted as the box shows its lines, under the box and with the keyboard on it', async () => {
    const h = filled();
    type(envBox(h), 'MODE=x\n\nghp_aB3dE5fG7hJ9kL1mN3pQ5rS7tU9vW1xY3zA5');
    await connect(h);
    await waitFor(() => expect(errorOf(envBox(h))).toBe(envLineError(3)), h.r);
    expect(h.saves).toEqual([]);
    expect(document.activeElement).toBe(envBox(h));
    expect(alerts(h)).toEqual([envLineError(3)]);
    expect(holds(h, 'ghp_aB3d')).toEqual([]);
    expect(holds(h, 'wW1xY3zA5')).toEqual([]);
    // a name that is not one before the equals sign is the same
    type(envBox(h), 'MODE=x\nMY-VAR=1');
    await waitFor(() => expect(errorOf(envBox(h))).toBe(null), h.r);
    await connect(h);
    await waitFor(() => expect(errorOf(envBox(h))).toBe(envLineError(2)), h.r);
    expect(holds(h, 'MY-VAR')).toEqual([]);
    expect(h.saves).toEqual([]);
    // a value after a valid name is the person's own and is sent as typed
    type(envBox(h), 'MODE=x\nTOKEN_URL=https://example.com/a=b');
    await connect(h);
    await waitFor(() => expect(h.saves.length).toBe(1), h.r);
    expect(h.saves[0]!.env).toEqual({ MODE: 'x', TOKEN_URL: 'https://example.com/a=b' });
  });

  it('reports the box and the secret rows together, and the keyboard goes to the first of them on the page', async () => {
    const h = filled();
    type(envBox(h), 'nonsense');
    await addSecret(h);
    await addSecret(h);
    type(secretName(h, 1), 'OK_NAME');
    type(secretValue(h, 1), 'v1');
    type(secretName(h, 2), 'two words');
    type(secretValue(h, 2), 'v2');
    await connect(h);
    await waitFor(() => expect(alerts(h).length).toBe(2), h.r);
    expect(alerts(h)).toEqual([envLineError(1), secretNameError(2)]);
    expect(errorOf(envBox(h))).toBe(envLineError(1));
    expect(errorOf(secretName(h, 1))).toBe(null);
    expect(errorOf(secretName(h, 2))).toBe(secretNameError(2));
    expect(document.activeElement).toBe(envBox(h));
    expect(h.saves).toEqual([]);
  });

  it('takes a message away when its field is typed in, and all the row messages when a row is removed', async () => {
    const h = filled();
    type(envBox(h), 'nonsense');
    await addSecret(h);
    await addSecret(h);
    type(secretName(h, 1), 'a b');
    type(secretValue(h, 1), 'v1');
    type(secretName(h, 2), 'c d');
    type(secretValue(h, 2), 'v2');
    await connect(h);
    await waitFor(() => expect(alerts(h).length).toBe(3), h.r);
    // typing in one field clears that field's message only
    await h.r.act(() => type(secretName(h, 2), 'c_d'));
    expect(alerts(h)).toEqual([envLineError(1), secretNameError(1)]);
    await h.r.act(() => type(envBox(h), 'MODE=x'));
    expect(alerts(h)).toEqual([secretNameError(1)]);
    // removing the first row moves the second up: a message numbered by place would now be on the wrong row, so none is left
    await h.r.act(() => byText(h.form, 'button', 'Remove').click());
    expect(alerts(h)).toEqual([]);
    expect(invalidFields(h)).toEqual([]);
    await connect(h);
    await waitFor(() => expect(h.saves.length).toBe(1), h.r);
    expect(h.saves[0]!.secretEnv).toEqual({ c_d: 'v2' });
    expect(h.saves[0]!.env).toEqual({ MODE: 'x' });
  });

  it('does not look at a name that the server already holds (its field is disabled), only at the ones typed', async () => {
    const stored = libServer({ secretEnv: ['STORED_KEY'] });
    const h = show({}, stored);
    expect(secretName(h, 1).hasAttribute('disabled')).toBe(true);
    await addSecret(h);
    type(secretName(h, 2), 'NEW_ONE=1');
    type(secretValue(h, 2), 'v');
    await h.r.act(() => byText(h.form, 'button', 'Save changes').click());
    await waitFor(() => expect(errorOf(secretName(h, 2))).toBe(secretNameError(2)), h.r);
    expect(errorOf(secretName(h, 1))).toBe(null);
    expect(h.saves).toEqual([]);
  });

  it('is not stopped by a stored name it cannot edit, even one that is not a valid name now: the core decides about what it holds', async () => {
    const h = show({}, libServer({ secretEnv: ['legacy-name'] }));
    expect(secretName(h, 1).hasAttribute('disabled')).toBe(true);
    await h.r.act(() => byText(h.form, 'button', 'Save changes').click());
    await waitFor(() => expect(h.saves.length).toBe(1), h.r);
    expect(h.saves[0]!.secretEnv).toEqual({ 'legacy-name': '' });
    expect(invalidFields(h)).toEqual([]);
  });

  it('says only what it refuses when the core had refused an earlier save: the core\'s answer is not left beside it', async () => {
    let calls = 0;
    const h = filled({
      mcpLibSave: async () => {
        calls++;
        throw new Error('the credential store is locked');
      },
    });
    await connect(h);
    await waitFor(() => expect(alerts(h).join(' ')).toContain('the credential store is locked'), h.r);
    expect(calls).toBe(1);
    type(envBox(h), 'MODE=x\nPASTED=1\nnonsense');
    await connect(h);
    await waitFor(() => expect(errorOf(envBox(h))).toBe(envLineError(3)), h.r);
    expect(alerts(h)).toEqual([envLineError(3)]);
    expect(calls).toBe(1);
  });

  it('saves a program with no environment as before', async () => {
    const h = filled();
    await connect(h);
    await waitFor(() => expect(h.saves.length).toBe(1), h.r);
    expect(h.saves[0]).toMatchObject({ id: 'tst', transport: 'stdio', argv: ['srv.exe'], env: {}, secretEnv: {} });
  });
});
