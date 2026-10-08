import { useRef, useState, type FormEvent } from 'react';
import type { NewProvider, ProbeResult, Provider, ProviderModel } from '../api/types';
import { formatDateTime } from '../lib/format';
import { useClient } from '../state/context';
import { useLive } from '../state/hooks';
import { Badge, ErrorNote, Page } from './common';
import { ReadOnlyNote, useReadOnly } from '../state/license';

const isZeroTime = (s: string | undefined) => !s || s.startsWith('0001-');

/** USD per million tokens. A model with no known price says so; it is never shown as 0. */
export function priceText(m: ProviderModel, which: 'in' | 'out'): string {
  if (m.isFree) return 'free';
  const v = which === 'in' ? m.inputPerM : m.outputPerM;
  if (!(m.inputPerM > 0 || m.outputPerM > 0)) return 'not reported';
  return v > 0 ? `$${v < 0.01 ? v.toFixed(4) : v.toFixed(2)}` : 'free';
}

export function PriceSourceBadge({ m }: { m: ProviderModel }) {
  const known = m.isFree || m.inputPerM > 0 || m.outputPerM > 0;
  if (!known) return <Badge title="The provider did not report a price and none was entered">no price</Badge>;
  const hint: Record<string, string> = {
    catalog: 'Price from the provider catalog',
    builtin: 'Price from Drydock’s built-in table',
    user: 'Price entered by you',
  };
  return (
    <Badge tone={m.priceSource === 'catalog' ? 'ok' : m.priceSource === 'user' ? 'info' : 'warn'} title={hint[m.priceSource] ?? 'Source of the price'}>
      {m.priceSource || 'unknown'}
    </Badge>
  );
}

export function ModelTable({ models }: { models: ProviderModel[] }) {
  if (models.length === 0) return <p className="muted">No models in the catalog. Run Test to fetch the model list.</p>;
  return (
    <table className="table" aria-label="Model catalog">
      <thead>
        <tr>
          <th scope="col">Model</th>
          <th scope="col">Context</th>
          <th scope="col">Tools</th>
          <th scope="col">In / M</th>
          <th scope="col">Out / M</th>
          <th scope="col">Price source</th>
          <th scope="col">Probe</th>
        </tr>
      </thead>
      <tbody>
        {models.map((m) => (
          <tr key={m.id} data-testid="model">
            <td>
              <code>{m.id}</code>
            </td>
            <td>{m.contextLength > 0 ? m.contextLength.toLocaleString() : 'not reported'}</td>
            <td>{m.supportsTools ? 'yes' : 'not verified'}</td>
            <td>{priceText(m, 'in')}</td>
            <td>{priceText(m, 'out')}</td>
            <td>
              <PriceSourceBadge m={m} />
            </td>
            <td>{m.probeStatus || 'not probed'}</td>
          </tr>
        ))}
      </tbody>
    </table>
  );
}

export function ProbeView({ r }: { r: ProbeResult }) {
  return (
    <div className="report" data-testid="probe" data-ok={r.OK}>
      <p>
        <Badge tone={r.OK ? 'ok' : 'err'}>{r.OK ? 'reachable' : r.AuthFailed ? 'key rejected' : 'failed'}</Badge> {r.Flavor ? `flavor ${r.Flavor}, ` : ''}
        {r.Latency > 0 ? `${Math.round(r.Latency / 1e6)} ms, ` : ''}
        {(r.Models ?? []).length} model(s) listed
      </p>
      {r.Error ? <p className="error-note">{r.Error}</p> : null}
      {(r.Warnings ?? []).map((w, i) => (
        <p key={i} className="warn-note">
          {w}
        </p>
      ))}
    </div>
  );
}

function ProviderCard({ p, onChanged }: { p: Provider; onChanged: () => void }) {
  const client = useClient();
  const [error, setError] = useState<Error | undefined>();
  const [busy, setBusy] = useState('');
  const [probe, setProbe] = useState<ProbeResult | undefined>();
  const [models, setModels] = useState<ProviderModel[] | undefined>();
  const [confirmDelete, setConfirmDelete] = useState(false);
  const run = async (what: string, fn: () => Promise<void>) => {
    setBusy(what);
    setError(undefined);
    try {
      await fn();
    } catch (e) {
      setError(e as Error);
    } finally {
      setBusy('');
    }
  };
  return (
    <article className="card" data-testid="provider" data-name={p.name}>
      <header className="card-head">
        <h3>{p.name}</h3>
        <Badge>{p.kind}</Badge>
        <Badge tone={p.enabled ? 'ok' : 'muted'}>{p.enabled ? 'enabled' : 'disabled'}</Badge>
        <Badge tone={p.status === 'up' ? 'ok' : p.status === 'down' ? 'err' : 'muted'}>{p.status || 'unknown'}</Badge>
      </header>
      {p.dataClass === 'trainable' ? (
        <p className="warn-note" data-testid="trainable-note">
          <Badge tone="warn">trainable</Badge> Public documentation questions only; never receives repo content.
        </p>
      ) : (
        <p>
          <Badge tone="ok">private</Badge>
        </p>
      )}
      <dl className="kv">
        <dt>Base URL</dt>
        <dd>
          <code>{p.baseUrl}</code>
        </dd>
        <dt>Tier</dt>
        <dd>{p.tier}</dd>
        <dt>Flavor</dt>
        <dd>{p.flavor || 'not detected'}</dd>
        <dt>API key</dt>
        <dd>{p.keyRef ? 'stored in the credential store (never shown)' : 'none'}</dd>
        <dt>Transport</dt>
        <dd>
          {p.tlsMode}
          {p.lanUnencrypted ? ' (plain http on a private network)' : ''}
        </dd>
        <dt>Last seen</dt>
        <dd>{isZeroTime(p.lastSeen) ? 'never' : formatDateTime(p.lastSeen)}</dd>
        {p.maxConcurrent > 0 ? (
          <>
            <dt>Max concurrent</dt>
            <dd>{p.maxConcurrent}</dd>
          </>
        ) : null}
        {Object.entries(p.attestations ?? {}).map(([k, v]) => (
          <div key={k} className="kv-row">
            <dt>{k}</dt>
            <dd>{v ? 'attested' : 'not attested'}</dd>
          </div>
        ))}
      </dl>
      <ErrorNote error={error} what="Failed" />
      <div className="form-actions">
        <button disabled={busy !== ''} onClick={() => run('test', async () => { setProbe(await client.providerTest(p.id)); onChanged(); })}>
          {busy === 'test' ? 'Testing...' : 'Test'}
        </button>
        <button disabled={busy !== ''} onClick={() => run('models', async () => setModels(await client.providerModels(p.id)))}>
          Models
        </button>
        <button disabled={busy !== ''} onClick={() => run('toggle', async () => { await client.providerSetEnabled(p.id, !p.enabled); onChanged(); })}>
          {p.enabled ? 'Disable' : 'Enable'}
        </button>
        {confirmDelete ? (
          <>
            <button className="danger" disabled={busy !== ''} onClick={() => run('delete', async () => { await client.providerDelete(p.id); onChanged(); })}>
              Yes, delete {p.name}
            </button>
            <button onClick={() => setConfirmDelete(false)}>Keep</button>
          </>
        ) : (
          <button onClick={() => setConfirmDelete(true)}>Delete</button>
        )}
      </div>
      {probe ? <ProbeView r={probe} /> : null}
      {models ? <ModelTable models={models} /> : null}
    </article>
  );
}

const ATTESTATIONS: [string, string][] = [
  ['billing_enabled', 'The Google project behind this key has billing enabled (needed for a private Gemini route)'],
  ['lan_attested', 'I accept plain http to this host on my private network'],
];

function ProviderForm({ onCreated }: { onCreated: () => void }) {
  const client = useClient();
  const keyRef = useRef<HTMLInputElement>(null);
  const [name, setName] = useState('');
  const [kind, setKind] = useState<'aggregator' | 'direct' | 'host'>('direct');
  const [baseUrl, setBaseUrl] = useState('');
  const [dataClass, setDataClass] = useState<'private' | 'trainable'>('private');
  const [tier, setTier] = useState<'premium' | 'cheap' | 'free'>('cheap');
  const [flavor, setFlavor] = useState('');
  const [maxConcurrent, setMaxConcurrent] = useState('');
  const [hourlyCost, setHourlyCost] = useState('');
  const [fingerprint, setFingerprint] = useState('');
  const [att, setAtt] = useState<Record<string, boolean>>({});
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<Error | undefined>();
  const ro = useReadOnly();

  const submit = async (e: FormEvent) => {
    e.preventDefault();
    // The key lives only in the (uncontrolled) input: read it once, clear the field before the request leaves, and never
    // put it in React state. A rejected request means the user types it again.
    const apiKey = keyRef.current?.value ?? '';
    if (keyRef.current) keyRef.current.value = '';
    const body: NewProvider = {
      Name: name.trim(),
      Kind: kind,
      BaseURL: baseUrl.trim(),
      DataClass: dataClass,
      Tier: tier,
      Attestations: Object.fromEntries(Object.entries(att).filter(([, v]) => v)),
    };
    if (apiKey) body.APIKey = apiKey;
    if (flavor) body.Flavor = flavor;
    if (maxConcurrent.trim()) body.MaxConcurrent = Number(maxConcurrent);
    if (hourlyCost.trim()) body.HourlyCost = Number(hourlyCost);
    if (fingerprint.trim()) body.TLSFingerprint = fingerprint.trim();
    setBusy(true);
    setError(undefined);
    try {
      await client.providerCreate(body);
      setName('');
      setBaseUrl('');
      setAtt({});
      onCreated();
    } catch (err) {
      setError(err as Error); // the core's validation text, verbatim
    } finally {
      setBusy(false);
    }
  };

  return (
    <form className="form form-grid" onSubmit={submit} autoComplete="off">
      <ErrorNote error={error} what="Provider not created" />
      <ReadOnlyNote />
      <label>
        Provider name
        <input value={name} onChange={(e) => setName(e.target.value)} required />
      </label>
      <label>
        Kind
        <select aria-label="Kind" value={kind} onChange={(e) => setKind(e.target.value as typeof kind)}>
          <option value="aggregator">aggregator (for example OpenRouter)</option>
          <option value="direct">direct (a model vendor&apos;s own API)</option>
          <option value="host">host (a server you run: Ollama, vLLM, ...)</option>
        </select>
      </label>
      <label>
        Base URL
        <input value={baseUrl} onChange={(e) => setBaseUrl(e.target.value)} placeholder="https://api.example.com/v1" spellCheck={false} required />
      </label>
      <label>
        API key (optional for a local host)
        <input ref={keyRef} type="password" autoComplete="new-password" spellCheck={false} name="provider-api-key" />
      </label>
      <label>
        Data class
        <select aria-label="Data class" value={dataClass} onChange={(e) => setDataClass(e.target.value as typeof dataClass)}>
          <option value="private">private (may receive repository content)</option>
          <option value="trainable">trainable (public documentation questions only)</option>
        </select>
      </label>
      <label>
        Tier
        <select aria-label="Tier" value={tier} onChange={(e) => setTier(e.target.value as typeof tier)}>
          <option value="premium">premium</option>
          <option value="cheap">cheap</option>
          <option value="free">free</option>
        </select>
      </label>
      <fieldset className="span-all">
        <legend>Attestations (only what is true)</legend>
        {ATTESTATIONS.map(([k, text]) => (
          <label key={k} className="inline">
            <input type="checkbox" checked={!!att[k]} onChange={(e) => setAtt({ ...att, [k]: e.target.checked })} /> {text}
          </label>
        ))}
      </fieldset>
      <details className="span-all">
        <summary>Advanced</summary>
        <div className="form-grid">
          <label>
            Flavor
            <select aria-label="Flavor" value={flavor} onChange={(e) => setFlavor(e.target.value)}>
              <option value="">detect from the URL</option>
              {['openai', 'openrouter', 'gemini', 'anthropic', 'vllm', 'sglang', 'ollama'].map((f) => (
                <option key={f} value={f}>
                  {f}
                </option>
              ))}
            </select>
          </label>
          <label>
            Max concurrent requests (0 = default)
            <input inputMode="numeric" value={maxConcurrent} onChange={(e) => setMaxConcurrent(e.target.value)} />
          </label>
          <label>
            Hourly cost in USD (hosts)
            <input inputMode="decimal" value={hourlyCost} onChange={(e) => setHourlyCost(e.target.value)} />
          </label>
          <label>
            Pinned TLS fingerprint (SHA-256, self-signed hosts)
            <input value={fingerprint} onChange={(e) => setFingerprint(e.target.value)} spellCheck={false} />
          </label>
        </div>
      </details>
      <div className="form-actions">
        <button type="submit" className="primary" disabled={busy || ro.readOnly || !name.trim() || !baseUrl.trim()} title={ro.readOnly ? ro.reason : undefined}>
          Add provider
        </button>
      </div>
    </form>
  );
}

export function ProvidersView() {
  const client = useClient();
  const q = useLive(() => client.providerList(), [], (e) => e.type.startsWith('provider.'));
  const list = q.data ?? [];
  return (
    <Page title="Providers">
      <p className="muted">
        Model providers Drydock may call directly. API keys are stored in the Windows Credential Manager and are never shown again after you save them. Plain http is
        accepted only for hosts on your own machine or private network.
      </p>
      <ErrorNote error={q.error} what="Could not list providers" />
      {q.data && list.length === 0 ? <p className="muted">No providers yet.</p> : null}
      <div className="cards">
        {list.map((p) => (
          <ProviderCard key={p.id} p={p} onChanged={q.reload} />
        ))}
      </div>
      <h2>Add a provider</h2>
      <ProviderForm onCreated={q.reload} />
    </Page>
  );
}
