// Import MCP servers from the configuration file other apps use: Claude Desktop's claude_desktop_config.json and Cursor's
// mcp.json ({"mcpServers": {...}}), or VS Code's mcp.json ({"servers": {...}}). Pure: it turns the file into library inputs
// and says what it could not take. Secrets in the file (environment values, a bearer token) become write-only secrets that
// go to the credential store, never plain settings.

import type { McpServerInput } from '../../api/mcpClient.ts';

export interface ImportCandidate {
  /** The server's name in the file. */
  name: string;
  input: McpServerInput;
  /** What changed on the way (an environment value kept as a secret, a header that is not sent). */
  notes: string[];
}

export interface ImportResult {
  candidates: ImportCandidate[];
  skipped: { name: string; reason: string }[];
}

type Raw = Record<string, unknown>;

const isObj = (v: unknown): v is Raw => typeof v === 'object' && v !== null && !Array.isArray(v);
const placeholder = /\$\{[^}]*\}/;

/** A short id from a name: lowercase letters and digits, starting with a letter, at most 8, unique among `taken`. */
export function slugFor(name: string, taken: Set<string>): string {
  let base = name.toLowerCase().replace(/[^a-z0-9]/g, '');
  base = base.replace(/^[0-9]+/, '');
  if (!base) base = 'srv';
  base = base.slice(0, 8);
  let id = base;
  for (let n = 2; taken.has(id); n++) id = base.slice(0, 8 - String(n).length) + n;
  taken.add(id);
  return id;
}

export function parseMcpConfig(text: string, existingIds: readonly string[] = []): ImportResult {
  let doc: unknown;
  try {
    doc = JSON.parse(text);
  } catch {
    throw new Error('This is not valid JSON.');
  }
  if (!isObj(doc)) throw new Error('The file must be a JSON object.');
  const servers = isObj(doc.mcpServers) ? doc.mcpServers : isObj(doc.servers) ? doc.servers : undefined;
  if (!servers) throw new Error('No "mcpServers" (Claude Desktop, Cursor) or "servers" (VS Code) object was found.');
  const taken = new Set(existingIds);
  const out: ImportResult = { candidates: [], skipped: [] };
  for (const [name, raw] of Object.entries(servers)) {
    if (!isObj(raw)) {
      out.skipped.push({ name, reason: 'its entry is not an object' });
      continue;
    }
    if (placeholder.test(JSON.stringify(raw))) {
      out.skipped.push({ name, reason: 'it uses ${...} placeholders that only its own app fills in; add it by hand' });
      continue;
    }
    const notes: string[] = [];
    const display = name.slice(0, 80);
    const url = [raw.url, raw.serverUrl, raw.httpUrl].find((u) => typeof u === 'string') as string | undefined;
    const type = typeof raw.type === 'string' ? raw.type.toLowerCase() : '';
    if (typeof raw.command === 'string' && raw.command.trim()) {
      const args = Array.isArray(raw.args) ? raw.args.filter((a): a is string => typeof a === 'string') : [];
      const input: McpServerInput = { id: slugFor(name, taken), display, transport: 'stdio', argv: [raw.command.trim(), ...args] };
      if (typeof raw.cwd === 'string' && raw.cwd.trim()) input.cwd = raw.cwd.trim();
      if (isObj(raw.env) && Object.keys(raw.env).length > 0) {
        input.secretEnv = {};
        for (const [k, v] of Object.entries(raw.env)) input.secretEnv[k] = String(v ?? '');
        notes.push(`Its environment values (${Object.keys(raw.env).join(', ')}) are kept in the credential store.`);
      }
      out.candidates.push({ name, input, notes });
    } else if (url) {
      if (type === 'sse') {
        out.skipped.push({ name, reason: 'it uses the old SSE transport; Drydock speaks streamable HTTP' });
        continue;
      }
      const input: McpServerInput = { id: slugFor(name, taken), display, transport: 'http', url: url.trim() };
      if (isObj(raw.headers)) {
        for (const [k, v] of Object.entries(raw.headers)) {
          const m = k.toLowerCase() === 'authorization' && typeof v === 'string' ? /^Bearer\s+(.+)$/i.exec(v.trim()) : null;
          if (m) {
            input.bearerToken = m[1];
            notes.push('Its bearer token is kept in the credential store.');
          } else {
            notes.push(`The header ${k} is not sent: Drydock sends only Authorization: Bearer.`);
          }
        }
      }
      out.candidates.push({ name, input, notes });
    } else {
      out.skipped.push({ name, reason: 'it has neither a command nor a url' });
    }
  }
  return out;
}
