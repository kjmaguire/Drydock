import { useState, type FormEvent } from 'react';
import type { McpLibraryServer, McpServerInput } from '../api/mcpClient';
import { useAction } from '../lib/useAction';
import { useClient } from '../state/context';
import { ReadOnlyNote, useReadOnly } from '../state/license';
import { Badge, ErrorNote } from './common';

/** One argument per line, so Windows paths and quoted values need no shell quoting rules. */
export const parseLines = (s: string): string[] => s.split('\n').map((l) => l.replace(/\r$/, '')).filter((l) => l.trim() !== '');

/** NAME=value per line. Throws a message the form shows. */
export function parseEnv(s: string, what: string): Record<string, string> {
  const out: Record<string, string> = {};
  for (const line of parseLines(s)) {
    const i = line.indexOf('=');
    if (i <= 0) throw new Error(`${what}: "${line.trim()}" is not NAME=value`);
    out[line.slice(0, i).trim()] = line.slice(i + 1);
  }
  return out;
}

/** The save input that keeps everything about a server as it is (secrets stay in the store, empty values keep them). */
export function inputOf(s: McpLibraryServer, over: Partial<McpServerInput> = {}): McpServerInput {
  return {
    id: s.id, display: s.display, transport: s.transport, url: s.url, argv: s.argv, cwd: s.cwd, env: s.env,
    secretEnv: Object.fromEntries(s.secretEnv.map((n) => [n, ''])), guard: s.guard, readOnlyTools: s.readOnlyTools, ...over,
  };
}

interface SecretRow {
  name: string;
  value: string;
  existing: boolean;
}

/** Connect a server once (add) or change one (edit). The credentials go to the Windows credential store and are never shown again. */
export function McpServerForm({ initial, onDone, onCancel }: { initial?: McpLibraryServer; onDone: (saved: McpLibraryServer) => void; onCancel: () => void }) {
  const client = useClient();
  const ro = useReadOnly();
  const act = useAction();
  const [localErr, setLocalErr] = useState<Error | undefined>();
  const [id, setId] = useState(initial?.id ?? '');
  const [display, setDisplay] = useState(initial?.display ?? '');
  const [transport, setTransport] = useState<'stdio' | 'http'>(initial?.transport ?? 'stdio');
  const [url, setUrl] = useState(initial?.url ?? '');
  const [argv, setArgv] = useState((initial?.argv ?? []).join('\n'));
  const [cwd, setCwd] = useState(initial?.cwd ?? '');
  const [env, setEnv] = useState(Object.entries(initial?.env ?? {}).map(([k, v]) => `${k}=${v}`).join('\n'));
  const [secrets, setSecrets] = useState<SecretRow[]>((initial?.secretEnv ?? []).map((n) => ({ name: n, value: '', existing: true })));
  const [token, setToken] = useState('');
  const [clearToken, setClearToken] = useState(false);
  const [guard, setGuard] = useState(initial?.guard ?? true);

  const submit = async (e: FormEvent) => {
    e.preventDefault();
    setLocalErr(undefined);
    let envObj: Record<string, string>;
    try {
      envObj = parseEnv(env, 'Environment');
    } catch (err) {
      setLocalErr(err as Error);
      return;
    }
    const input: McpServerInput = {
      id: id.trim(), display: display.trim(), transport, guard, readOnlyTools: initial?.readOnlyTools ?? [],
      ...(transport === 'http'
        ? { url: url.trim(), bearerToken: token.trim() || undefined, clearBearer: clearToken || undefined }
        : { argv: parseLines(argv), cwd: cwd.trim() || undefined, env: envObj, secretEnv: Object.fromEntries(secrets.filter((s) => s.name.trim()).map((s) => [s.name.trim(), s.value])) }),
    };
    const r = await act.run('save', () => client.mcpLibSave(input));
    setToken('');
    if (r) onDone(r);
  };

  return (
    <form className="form form-grid" onSubmit={submit} aria-label={initial ? `Edit ${initial.display}` : 'Connect an MCP server'} data-testid="mcp-form">
      <ErrorNote error={localErr ?? act.error} what="Server not saved" />
      <ReadOnlyNote />
      <label>
        Short id (letters and digits, at most 8; agents see its tools as <code>id__tool</code>)
        <input value={id} onChange={(e) => setId(e.target.value)} disabled={!!initial} required pattern="[a-z][a-z0-9]{0,7}" spellCheck={false} />
      </label>
      <label>
        Name
        <input value={display} onChange={(e) => setDisplay(e.target.value)} required />
      </label>
      <label>
        Kind
        <select aria-label="Kind" value={transport} onChange={(e) => setTransport(e.target.value as 'stdio' | 'http')}>
          <option value="stdio">A program on this computer (stdio)</option>
          <option value="http">A web address (https)</option>
        </select>
      </label>
      {transport === 'http' ? (
        <>
          <label>
            Address (https, or http on this computer only)
            <input value={url} onChange={(e) => setUrl(e.target.value)} required spellCheck={false} placeholder="https://example.com/mcp" />
          </label>
          <label>
            Bearer token {initial?.hasToken ? <Badge tone="ok">set</Badge> : null}
            <input type="password" autoComplete="off" value={token} onChange={(e) => setToken(e.target.value)} placeholder={initial?.hasToken ? 'leave empty to keep the stored token' : 'optional'} />
          </label>
          {initial?.hasToken ? (
            <label className="inline">
              <input type="checkbox" checked={clearToken} onChange={(e) => setClearToken(e.target.checked)} /> Remove the stored token
            </label>
          ) : null}
        </>
      ) : (
        <>
          <label>
            Command, one argument per line (the program first)
            <textarea value={argv} onChange={(e) => setArgv(e.target.value)} rows={3} required spellCheck={false} placeholder={'npx\n-y\n@modelcontextprotocol/server-everything@1.0.0'} />
          </label>
          <label>
            Working folder (optional)
            <input value={cwd} onChange={(e) => setCwd(e.target.value)} spellCheck={false} />
          </label>
          <label>
            Environment, NAME=value per line (not secret)
            <textarea value={env} onChange={(e) => setEnv(e.target.value)} rows={2} spellCheck={false} />
          </label>
          <fieldset>
            <legend>Secret environment variables (kept in the Windows credential store, never shown again)</legend>
            {secrets.map((s, i) => (
              <div key={i} className="form-actions">
                <input aria-label={`Secret name ${i + 1}`} value={s.name} disabled={s.existing} onChange={(e) => setSecrets(secrets.map((x, j) => (j === i ? { ...x, name: e.target.value } : x)))} placeholder="API_KEY" spellCheck={false} />
                <input
                  aria-label={`Secret value ${i + 1}`}
                  type="password"
                  autoComplete="off"
                  value={s.value}
                  onChange={(e) => setSecrets(secrets.map((x, j) => (j === i ? { ...x, value: e.target.value } : x)))}
                  placeholder={s.existing ? 'leave empty to keep' : 'value'}
                />
                <button type="button" onClick={() => setSecrets(secrets.filter((_, j) => j !== i))}>
                  Remove
                </button>
              </div>
            ))}
            <button type="button" onClick={() => setSecrets([...secrets, { name: '', value: '', existing: false }])}>
              Add a secret
            </button>
          </fieldset>
        </>
      )}
      <label className="inline">
        <input type="checkbox" checked={guard} onChange={(e) => setGuard(e.target.checked)} /> Stop agents from sending code, files, secrets or long text to this server
      </label>
      <div className="form-actions">
        <button type="submit" className="primary" disabled={act.busy !== '' || ro.readOnly} title={ro.readOnly ? ro.reason : undefined}>
          {act.busy === 'save' ? 'Saving...' : initial ? 'Save changes' : 'Connect server'}
        </button>
        <button type="button" onClick={onCancel}>
          Cancel
        </button>
      </div>
    </form>
  );
}
