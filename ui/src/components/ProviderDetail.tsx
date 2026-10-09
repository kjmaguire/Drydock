import { useState, type FormEvent } from 'react';
import type { ProviderModel } from '../api/types';
import { formatDateTime } from '../lib/format';
import { useAction, numOrUndef } from '../lib/useAction';
import { useClient } from '../state/context';
import { useLive } from '../state/hooks';
import { Badge, ErrorNote } from './common';

const KINDS: [string, string][] = [
  ['rpm', 'requests per minute'],
  ['rpd', 'requests per day'],
  ['tpm', 'tokens per minute'],
  ['tpd', 'tokens per day'],
  ['itpm', 'input tokens per minute'],
  ['otpm', 'output tokens per minute'],
  ['credits', 'credits (USD)'],
];

/** A provider in full (docs/ui.md 9): settings to edit, served models with editable prices, quota windows, OpenRouter key limits. */
export function ProviderDetailPanel({ id, onChanged }: { id: string; onChanged: () => void }) {
  const client = useClient();
  const q = useLive(() => client.providerDetail(id), [id], (e) => e.type.startsWith('provider.'));
  const act = useAction();
  const [keyInfo, setKeyInfo] = useState<{ text: string } | undefined>();
  const d = q.data;
  if (!d) return <ErrorNote error={q.error} what="Could not load the provider" />;
  return (
    <section className="provider-detail" data-testid="provider-detail" aria-label={`${d.provider.name} details`}>
      <ErrorNote error={act.error ?? q.error} what="Provider" />
      <SettingsForm
        d={d.provider}
        onSave={async (e) => {
          const r = await act.run('save', () => client.providerUpdate(e));
          if (r) {
            await q.reload();
            onChanged();
          }
        }}
        busy={act.busy}
      />
      <h4>Models and prices</h4>
      {d.models.length === 0 ? <p className="muted">No models are listed yet. Test the provider to fetch its catalog.</p> : null}
      <table className="table" aria-label="Models and prices">
        <thead>
          <tr>
            <th scope="col">Model</th>
            <th scope="col">Input $/Mtok</th>
            <th scope="col">Output $/Mtok</th>
            <th scope="col">Price from</th>
            <th scope="col">Probe</th>
            <th scope="col" />
          </tr>
        </thead>
        <tbody>
          {d.models.map((m) => (
            <PriceRow key={m.id} m={m} onSave={async (i, o) => {
              await act.run('price', () => client.providerSetPrice(id, m.id, i, o, m.cacheReadPerM, m.cacheWritePerM));
              await q.reload();
            }} />
          ))}
        </tbody>
      </table>
      <h4>Quota</h4>
      {d.quota.length === 0 ? <p className="muted">No limits known. Drydock never invents one: they come from the provider&apos;s headers or API, or from what you set here.</p> : null}
      <ul className="quota-list">
        {d.quota.map((w, i) => (
          <li key={i} data-testid="quota-window">
            <strong>{w.kind}</strong> {w.model ? <code>{w.model}</code> : 'all models'}: {w.used} of {w.limit} used <Badge>{w.source}</Badge>
            {w.resetAt ? <span className="muted"> resets {formatDateTime(new Date(w.resetAt).toISOString())}</span> : null}
          </li>
        ))}
      </ul>
      <QuotaForm models={d.models.map((m) => m.id)} onAdd={async (model, kind, limit) => {
        await act.run('quota', () => client.providerSetQuota(id, model, kind, limit));
        await q.reload();
      }} />
      <p>
        <button
          onClick={async () => {
            const r = await act.run('key', () => client.providerDetail(id, true));
            if (r) setKeyInfo({ text: r.openRouterKey ? `Key "${r.openRouterKey.label}": used ${r.openRouterKey.usage}${r.openRouterKey.limit !== null ? ` of ${r.openRouterKey.limit}` : ' (no limit)'}${r.openRouterKey.is_free_tier ? ', free tier' : ''}` : r.keyError ?? '' });
          }}
        >
          Read the key&apos;s limits from the provider
        </button>{' '}
        {keyInfo ? <span data-testid="key-info">{keyInfo.text}</span> : null}
      </p>
    </section>
  );
}

function SettingsForm({ d, onSave, busy }: { d: { id: string; name: string; baseUrl: string; tier: string; dataClass: string; maxConcurrent: number }; onSave: (e: Parameters<ReturnType<typeof useClient>['providerUpdate']>[0]) => void; busy: string }) {
  const [name, setName] = useState(d.name);
  const [baseUrl, setBaseUrl] = useState(d.baseUrl);
  const [tier, setTier] = useState(d.tier);
  const [dataClass, setDataClass] = useState(d.dataClass);
  const [maxc, setMaxc] = useState(String(d.maxConcurrent || ''));
  const [key, setKey] = useState('');
  const submit = (e: FormEvent) => {
    e.preventDefault();
    onSave({ id: d.id, name, baseUrl, tier, dataClass, maxConcurrent: numOrUndef(maxc) ?? 0, apiKey: key || undefined });
    setKey('');
  };
  return (
    <form className="form form-grid" onSubmit={submit} aria-label="Provider settings">
      <label>
        Name
        <input value={name} onChange={(e) => setName(e.target.value)} required />
      </label>
      <label>
        Base URL
        <input value={baseUrl} onChange={(e) => setBaseUrl(e.target.value)} required />
      </label>
      <label>
        Tier
        <select value={tier} onChange={(e) => setTier(e.target.value)}>
          <option value="premium">premium</option>
          <option value="cheap">cheap</option>
          <option value="free">free</option>
        </select>
      </label>
      <label>
        Data class
        <select value={dataClass} onChange={(e) => setDataClass(e.target.value)}>
          <option value="private">private</option>
          <option value="trainable">trainable (public documentation questions only)</option>
        </select>
      </label>
      <label>
        Max concurrent agents (empty: unlimited)
        <input value={maxc} onChange={(e) => setMaxc(e.target.value)} inputMode="numeric" />
      </label>
      <label>
        Replace the API key (kept in the credential store; never shown)
        <input type="password" value={key} onChange={(e) => setKey(e.target.value)} autoComplete="off" />
      </label>
      <div className="form-actions">
        <button type="submit" className="primary" disabled={busy !== ''}>
          {busy === 'save' ? 'Saving...' : 'Save settings'}
        </button>
      </div>
    </form>
  );
}

function PriceRow({ m, onSave }: { m: ProviderModel; onSave: (i: number, o: number) => void }) {
  const [editing, setEditing] = useState(false);
  const [i, setI] = useState(String(m.inputPerM ?? 0));
  const [o, setO] = useState(String(m.outputPerM ?? 0));
  return (
    <tr data-testid="price-row" data-model={m.id}>
      <td>
        <code>{m.id}</code>
      </td>
      {editing ? (
        <>
          <td>
            <input aria-label={`Input price of ${m.id}`} value={i} onChange={(e) => setI(e.target.value)} inputMode="decimal" size={8} />
          </td>
          <td>
            <input aria-label={`Output price of ${m.id}`} value={o} onChange={(e) => setO(e.target.value)} inputMode="decimal" size={8} />
          </td>
        </>
      ) : (
        <>
          <td>{m.isFree ? 'free' : m.inputPerM || m.outputPerM ? m.inputPerM : 'unknown'}</td>
          <td>{m.isFree ? 'free' : m.inputPerM || m.outputPerM ? m.outputPerM : 'unknown'}</td>
        </>
      )}
      <td>
        {m.priceSource || 'unknown'}
        {m.priceSource === 'user' && m.priceUpdatedAt ? <span className="muted"> {formatDateTime(m.priceUpdatedAt)}</span> : null}
      </td>
      <td>{m.probeStatus || 'not probed'}</td>
      <td>
        {editing ? (
          <>
            <button
              onClick={() => {
                const a = Number(i);
                const b = Number(o);
                if (Number.isFinite(a) && Number.isFinite(b)) {
                  onSave(a, b);
                  setEditing(false);
                }
              }}
            >
              Save price
            </button>
            <button className="link-button" onClick={() => setEditing(false)}>
              Cancel
            </button>
          </>
        ) : (
          <button className="link-button" onClick={() => setEditing(true)}>
            Edit price
          </button>
        )}
      </td>
    </tr>
  );
}

function QuotaForm({ models, onAdd }: { models: string[]; onAdd: (model: string, kind: string, limit: number) => void }) {
  const [model, setModel] = useState('');
  const [kind, setKind] = useState('rpd');
  const [limit, setLimit] = useState('');
  return (
    <form
      className="form inline-form"
      aria-label="Set a quota limit"
      onSubmit={(e) => {
        e.preventDefault();
        const n = Number(limit);
        if (n > 0) onAdd(model, kind, n);
      }}
    >
      <select aria-label="Quota model" value={model} onChange={(e) => setModel(e.target.value)}>
        <option value="">all models</option>
        {models.map((m) => (
          <option key={m} value={m}>
            {m}
          </option>
        ))}
      </select>
      <select aria-label="Quota window" value={kind} onChange={(e) => setKind(e.target.value)}>
        {KINDS.map(([k, l]) => (
          <option key={k} value={k}>
            {l}
          </option>
        ))}
      </select>
      <input aria-label="Quota limit" value={limit} onChange={(e) => setLimit(e.target.value)} inputMode="decimal" placeholder="limit" size={8} />
      <button type="submit">Set limit</button>
    </form>
  );
}
