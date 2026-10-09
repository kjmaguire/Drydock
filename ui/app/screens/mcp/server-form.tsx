// Connect an MCP server once (add) or change one (edit). The credentials go to the Windows credential store and are never
// shown again: secret values and the bearer token are write-only fields, and an empty one keeps what is stored.

import { css, on, type Handle } from 'remix/component';
import { KeyRound, Plus, Trash2 } from 'lucide';
import type { McpLibraryServer } from '../../api/mcpClient.ts';
import { ReadOnlyNote } from '../../shell/license-banner.tsx';
import { action, services, watch } from '../../state/services.tsx';
import { Button, IconButton } from '../../ui/button.tsx';
import { Badge, muted } from '../../ui/data.tsx';
import { ErrorNote } from '../../ui/feedback.tsx';
import { Checkbox, Field, formActions, formGrid } from '../../ui/form.tsx';
import { t } from '../../ui/tokens.ts';
import { buildInput, formValues } from './server-input.ts';

const full = css({ gridColumn: '1 / -1' });
const fieldsetCss = css({
  gridColumn: '1 / -1',
  display: 'grid',
  gap: t.s2,
  margin: '0',
  padding: t.s3,
  border: `1px solid ${t.border}`,
  borderRadius: t.rMd,
  '& legend': { padding: `0 ${t.s1}`, fontSize: t.textSm, fontWeight: '500' },
});
const secretRow = css({ display: 'grid', gridTemplateColumns: '1fr 1fr auto', gap: t.s2, alignItems: 'center' });
const hintCss = css({ gridColumn: '1 / -1', margin: '0', fontSize: t.textSm });

export function McpServerForm(handle: Handle<{ initial?: McpLibraryServer; onDone: (saved: McpLibraryServer) => void; onCancel: () => void }>) {
  const s = services(handle);
  watch(handle, s.workspace);
  const act = action(handle);
  const v = formValues(handle.props.initial);
  let localErr: Error | undefined;

  const set = <K extends keyof typeof v>(k: K, value: (typeof v)[K]) => {
    v[k] = value;
    void handle.update();
  };
  const text = (k: 'id' | 'display' | 'url' | 'cwd' | 'token') => on<HTMLInputElement, 'input'>('input', (e) => set(k, e.currentTarget.value));
  const area = (k: 'argv' | 'env') => on<HTMLTextAreaElement, 'input'>('input', (e) => set(k, e.currentTarget.value));

  const submit = async (e: SubmitEvent) => {
    e.preventDefault();
    localErr = undefined;
    const { initial, onDone } = handle.props;
    let input;
    try {
      input = buildInput(v, initial);
    } catch (err) {
      localErr = err as Error;
      void handle.update();
      return;
    }
    const r = await act.run('save', () => s.client.mcpLibSave(input));
    v.token = '';
    void handle.update();
    if (r) onDone(r);
  };

  return () => {
    const { initial, onCancel } = handle.props;
    const ro = s.workspace.license;
    return (
      <form mix={[formGrid, on<HTMLFormElement, 'submit'>('submit', submit)]} aria-label={initial ? `Edit ${initial.display}` : 'Connect an MCP server'} data-testid="mcp-form">
        <div mix={full}>
          <ErrorNote error={localErr ?? act.error} what="Server not saved" />
          <ReadOnlyNote />
        </div>
        <Field
          label="Short id"
          hint={
            <>
              Letters and digits, at most 8. Agents see its tools as <code>id__tool</code>.
            </>
          }
        >
          {(id) => <input id={id} value={v.id} disabled={!!initial} required pattern="[a-z][a-z0-9]{0,7}" spellcheck={false} mix={text('id')} />}
        </Field>
        <Field label="Name">{(id) => <input id={id} value={v.display} required mix={text('display')} />}</Field>
        <Field label="Kind">
          {(id) => (
            <select id={id} aria-label="Kind" value={v.transport} mix={on('change', (e) => set('transport', e.currentTarget.value as 'stdio' | 'http'))}>
              <option value="stdio" selected={v.transport === 'stdio'}>
                A program on this computer (stdio)
              </option>
              <option value="http" selected={v.transport === 'http'}>
                A web address (https)
              </option>
            </select>
          )}
        </Field>
        {v.transport === 'http' ? (
          <>
            <Field label="Address" hint="https, or http on this computer only.">
              {(id) => <input id={id} value={v.url} required spellcheck={false} placeholder="https://example.com/mcp" mix={text('url')} />}
            </Field>
            <Field label="How Drydock proves who it is">
              {(id) => (
                <select id={id} aria-label="Sign-in" value={v.auth} mix={on('change', (e) => set('auth', e.currentTarget.value as 'none' | 'token' | 'oauth'))}>
                  <option value="none" selected={v.auth === 'none'}>
                    Nothing (a public server)
                  </option>
                  <option value="token" selected={v.auth === 'token'}>
                    A bearer token or API key I paste
                  </option>
                  <option value="oauth" selected={v.auth === 'oauth'}>
                    Sign in with the server's account (OAuth)
                  </option>
                </select>
              )}
            </Field>
            {v.auth === 'token' ? (
              <>
                <Field
                  label={
                    <>
                      Bearer token {initial?.hasToken ? <Badge tone="ok">set</Badge> : null}
                    </>
                  }
                >
                  {(id) => (
                    <input
                      id={id}
                      type="password"
                      autocomplete="off"
                      value={v.token}
                      placeholder={initial?.hasToken ? 'leave empty to keep the stored token' : 'optional'}
                      mix={text('token')}
                    />
                  )}
                </Field>
                {initial?.hasToken ? <Checkbox label="Remove the stored token" checked={v.clearToken} onChange={(c) => set('clearToken', c)} /> : null}
              </>
            ) : null}
            {v.auth === 'oauth' ? (
              <p mix={[hintCss, muted]}>
                After you save, press Sign in on the server's card. Your browser opens the server's own sign-in page. Drydock keeps the tokens in the Windows credential store.
              </p>
            ) : null}
          </>
        ) : (
          <>
            <div mix={full}>
              <Field label="Command, one argument per line (the program first)">
                {(id) => <textarea id={id} value={v.argv} rows={3} required spellcheck={false} placeholder={'npx\n-y\n@modelcontextprotocol/server-everything@1.0.0'} mix={area('argv')} />}
              </Field>
            </div>
            <Field label="Working folder (optional)">{(id) => <input id={id} value={v.cwd} spellcheck={false} mix={text('cwd')} />}</Field>
            <Field label="Environment, NAME=value per line (not secret)">
              {(id) => <textarea id={id} value={v.env} rows={2} spellcheck={false} mix={area('env')} />}
            </Field>
            <fieldset mix={fieldsetCss}>
              <legend>Secret environment variables (kept in the Windows credential store, never shown again)</legend>
              {v.secrets.map((row, i) => (
                <div key={i} mix={secretRow}>
                  <input
                    aria-label={`Secret name ${i + 1}`}
                    value={row.name}
                    disabled={row.existing}
                    placeholder="API_KEY"
                    spellcheck={false}
                    mix={on('input', (e) => set('secrets', v.secrets.map((x, j) => (j === i ? { ...x, name: e.currentTarget.value } : x))))}
                  />
                  <input
                    aria-label={`Secret value ${i + 1}`}
                    type="password"
                    autocomplete="off"
                    value={row.value}
                    placeholder={row.existing ? 'leave empty to keep' : 'value'}
                    mix={on('input', (e) => set('secrets', v.secrets.map((x, j) => (j === i ? { ...x, value: e.currentTarget.value } : x))))}
                  />
                  <IconButton icon={Trash2} label="Remove" mix={[on('click', () => set('secrets', v.secrets.filter((_, j) => j !== i)))]} />
                </div>
              ))}
              <div>
                <Button size="sm" variant="ghost" icon={Plus} mix={[on('click', () => set('secrets', [...v.secrets, { name: '', value: '', existing: false }]))]}>
                  Add a secret
                </Button>
              </div>
            </fieldset>
          </>
        )}
        <div mix={full}>
          <Checkbox label="Stop agents from sending code, files, secrets or long text to this server" checked={v.guard} onChange={(c) => set('guard', c)} />
        </div>
        <div mix={formActions}>
          <Button type="submit" variant="primary" icon={initial ? undefined : KeyRound} disabled={act.busy !== '' || ro.readOnly} title={ro.readOnly ? ro.reason : undefined}>
            {act.busy === 'save' ? 'Saving...' : initial ? 'Save changes' : 'Connect server'}
          </Button>
          <Button mix={[on('click', () => onCancel())]}>Cancel</Button>
        </div>
      </form>
    );
  };
}
