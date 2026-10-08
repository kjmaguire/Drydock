import { useState } from 'react';
import type { Pack, PackAttachment, PackCitation, PackConsult, PackSearchResult, QuarantinedChunk } from '../api/extTypes';
import { Markdown, safeHref } from '../lib/Markdown';
import { formatCost, formatDateTime } from '../lib/format';
import { useAction } from '../lib/useAction';
import { useClient } from '../state/context';
import { useLive } from '../state/hooks';
import { Badge, ErrorNote, Page } from '../components/common';

const zeroTime = (s?: string) => !s || s.startsWith('0001-');

/** A citation as the pack search returned it. The URL is outside text: linked only when it is http or https. */
export function CitationLine({ c }: { c: PackCitation }) {
  const href = safeHref(c.url);
  return (
    <span className="citation" data-testid="citation">
      <code>
        {c.pack}@{c.version}
      </code>{' '}
      {href ? (
        <a href={href} target="_blank" rel="noopener noreferrer" title={href}>
          {c.headingPath || c.url}
        </a>
      ) : (
        <span title="Local or non-web source">{c.headingPath || c.url}</span>
      )}{' '}
      <span className="muted">
        {c.origin}
        {!zeroTime(c.fetchedAt) ? `, fetched ${formatDateTime(c.fetchedAt)}` : ''}, hash {c.contentHash.slice(0, 8)}
      </span>
    </span>
  );
}

export function SearchResultView({ r }: { r: PackSearchResult }) {
  const hits = r.hits ?? [];
  return (
    <div data-testid="pack-search-result" data-found={r.found}>
      <p>
        {r.found ? `${hits.length} hit(s)` : 'Nothing found'} in {(r.searched ?? []).join(', ') || 'no pack'}; confidence <Badge>{r.confidence}</Badge>{' '}
        <span className="muted">(a heuristic from the search, not calibrated); source age {r.sourceAgeDays} day(s)</span>
      </p>
      {Object.entries(r.unavailable ?? {}).map(([k, v]) => (
        <p key={k} className="warn-note">
          {k} unavailable: {v}
        </p>
      ))}
      {(r.warn ?? []).map((w, i) => (
        <p key={i} className="warn-note">
          {w}
        </p>
      ))}
      {(r.suspiciousDropped ?? 0) > 0 ? <p className="warn-note">{r.suspiciousDropped} result(s) were dropped because they looked like instructions to an agent.</p> : null}
      <ol className="hits">
        {hits.map((h, i) => (
          <li key={i} data-testid="pack-hit">
            <CitationLine c={h.citation} />
            <Markdown source={h.snippet || h.text} maxChars={4000} />
          </li>
        ))}
      </ol>
      {(r.attributions ?? []).length > 0 ? <p className="muted">Attribution: {(r.attributions ?? []).join('; ')}</p> : null}
    </div>
  );
}

export function ConsultView({ r }: { r: PackConsult }) {
  return (
    <div data-testid="pack-consult" data-found={r.found}>
      <p>
        <Badge>{r.confidence}</Badge> <span className="muted">route {r.route}{r.model ? `, model ${r.model}` : ''}; source age {r.sourceAgeDays} day(s)</span>
      </p>
      {r.degraded ? <p className="warn-note">{r.degraded}</p> : null}
      <Markdown source={r.answer || '(no answer)'} />
      <h4>Citations</h4>
      <ol>
        {(r.sources ?? []).map((s) => (
          <li key={s.n}>
            <CitationLine c={s.citation} />
            <div className="muted">{s.excerpt}</div>
          </li>
        ))}
      </ol>
      <p className="muted">
        Cost of this question: {formatCost(r.usage.costUsd)} ({r.usage.inputTokens} in / {r.usage.outputTokens} out tokens). The answer is generated from the cited text; check the citations.
      </p>
    </div>
  );
}

function QuarantineList({ packId, onChanged }: { packId: string; onChanged: () => void }) {
  const client = useClient();
  const act = useAction();
  const q = useLive(() => client.packQuarantined(packId), [packId], (e) => e.type.startsWith('pack.'));
  const rows: QuarantinedChunk[] = q.data ?? [];
  return (
    <div data-testid="quarantine">
      <p className="muted">These passages looked like instructions to an agent and were kept out of search. Only you can release one.</p>
      <ErrorNote error={q.error ?? act.error} />
      {rows.length === 0 ? <p className="muted">Nothing is quarantined.</p> : null}
      {rows.map((c) => (
        <div key={c.chunkId} className="gh-review" data-testid="quarantined-chunk">
          <div>
            <Badge tone="warn">risk {c.risk}</Badge> {(c.flags ?? []).join(', ')} <span className="muted">{c.headingPath}</span>
          </div>
          <Markdown source={c.body} maxChars={3000} />
          <button disabled={act.busy !== ''} onClick={async () => { await act.run('release', () => client.packReleaseChunk(c.chunkId)); q.reload(); onChanged(); }}>
            Release this passage to search
          </button>
        </div>
      ))}
    </div>
  );
}

function PackCard({ p, projectId, attached, onChanged }: { p: Pack; projectId: string; attached: PackAttachment[]; onChanged: () => void }) {
  const client = useClient();
  const act = useAction();
  const [role, setRole] = useState('');
  const [showQ, setShowQ] = useState(false);
  const [confirm, setConfirm] = useState(false);
  const [refreshed, setRefreshed] = useState<string | undefined>();
  const mine = attached.filter((a) => a.packId === p.id);
  return (
    <article className="card" data-testid="pack" data-pack={p.id}>
      <header className="card-head">
        <h3>{p.title}</h3>
        <code>{p.id}</code>
        <Badge>{p.kind}</Badge>
        <Badge tone={p.visibility === 'private' ? 'ok' : 'warn'}>{p.visibility}</Badge>
        <Badge tone={p.status === 'ready' ? 'ok' : 'muted'}>{p.status}</Badge>
      </header>
      <dl className="kv">
        <dt>Version</dt>
        <dd>{p.version || 'not fetched yet'}</dd>
        <dt>Content</dt>
        <dd>
          {p.pages} pages, {p.chunks} passages, {p.quarantined} quarantined
        </dd>
        <dt>Freshness</dt>
        <dd>
          {p.freshness || 'unknown'}
          {p.version ? `, ${p.ageDays} day(s) old` : ''}
          {p.offline ? ', offline' : ''}
        </dd>
        <dt>Last refresh</dt>
        <dd>{zeroTime(p.lastSuccessAt) ? 'never' : formatDateTime(p.lastSuccessAt)}</dd>
        {p.manifest?.license?.content ? (
          <>
            <dt>License</dt>
            <dd>{p.manifest.license.content}</dd>
          </>
        ) : null}
      </dl>
      {p.lastError ? <p className="error-note">{p.lastError}</p> : null}
      {p.manifestDrift ? <p className="warn-note">pack.yaml on disk differs from the installed manifest.</p> : null}
      <ErrorNote error={act.error} />
      {refreshed ? <p role="status">{refreshed}</p> : null}
      <p>
        Attached to this project:{' '}
        {mine.length === 0 ? 'no' : mine.map((a) => `${a.role || 'all roles'}${a.agentId ? ` (agent ${a.agentId})` : ''}`).join(', ')}
      </p>
      <div className="form-actions">
        <button
          disabled={act.busy !== ''}
          onClick={async () => {
            const r = await act.run('refresh', () => client.packRefresh(p.id, true));
            if (r) setRefreshed(`Version ${r.version}: ${r.pages} pages, ${r.added} added, ${r.changed} changed, ${r.unchanged} unchanged, ${r.skipped} skipped, ${r.failed} failed, ${r.quarantined} quarantined.`);
            onChanged();
          }}
        >
          Refresh now
        </button>
        <label>
          Role
          <input aria-label={`Role for ${p.id}`} value={role} onChange={(e) => setRole(e.target.value)} placeholder="all roles" />
        </label>
        <button disabled={act.busy !== ''} onClick={async () => { await act.run('attach', () => client.packAttach(projectId, p.id, '', role.trim())); onChanged(); }}>
          Attach to this project
        </button>
        {mine.length > 0 ? (
          <button disabled={act.busy !== ''} onClick={async () => { await act.run('detach', () => client.packDetach(projectId, p.id, mine[0]!.agentId ?? '', mine[0]!.role ?? '')); onChanged(); }}>
            Detach
          </button>
        ) : null}
        <button onClick={() => setShowQ(!showQ)} aria-expanded={showQ}>
          Quarantined ({p.quarantined})
        </button>
        {confirm ? (
          <>
            <button className="danger" onClick={async () => { await act.run('remove', () => client.packRemove(p.id)); onChanged(); }}>Yes, remove {p.id}</button>
            <button onClick={() => setConfirm(false)}>Keep</button>
          </>
        ) : (
          <button onClick={() => setConfirm(true)}>Remove</button>
        )}
      </div>
      {showQ ? <QuarantineList packId={p.id} onChanged={onChanged} /> : null}
    </article>
  );
}

export default function PacksView({ projectId }: { projectId: string }) {
  const client = useClient();
  const packs = useLive(() => client.packList(), [], (e) => e.type.startsWith('pack.'));
  const attachments = useLive(() => client.packAttachments(projectId), [projectId], (e) => e.type.startsWith('pack.'));
  const act = useAction();
  const [yaml, setYaml] = useState('');
  const [query, setQuery] = useState('');
  const [question, setQuestion] = useState('');
  const [selected, setSelected] = useState<string[]>([]);
  const [result, setResult] = useState<PackSearchResult | undefined>();
  const [consult, setConsult] = useState<PackConsult | undefined>();
  const list = packs.data ?? [];
  const ids = selected.length ? selected : list.map((p) => p.id);
  const reload = () => { packs.reload(); attachments.reload(); };
  return (
    <Page title="Expert packs">
      <p className="muted">
        A pack is a body of reference text (documentation, standards) that agents search and cite. Everything inside a pack is outside text: it is shown here as inert text,
        passages that look like instructions are quarantined, and nothing in a pack can grant a permission or make a decision.
      </p>
      <ErrorNote error={packs.error ?? act.error} what="Packs" />
      {packs.data && list.length === 0 ? <p className="muted">No packs installed.</p> : null}
      <div className="cards">
        {list.map((p) => (
          <PackCard key={p.id} p={p} projectId={projectId} attached={attachments.data ?? []} onChanged={reload} />
        ))}
      </div>
      <h2>Install a pack</h2>
      <p className="muted">Paste a pack manifest (YAML). A source can be a local folder (type local_dir with a path), a list of URLs or a site. Installing does not fetch anything; use Refresh.</p>
      <div className="form">
        <label>
          Pack manifest (YAML)
          <textarea value={yaml} onChange={(e) => setYaml(e.target.value)} rows={8} spellCheck={false} />
        </label>
        <div className="form-actions">
          <button className="primary" disabled={act.busy !== '' || !yaml.trim()} onClick={async () => { const r = await act.run('install', () => client.packInstall(yaml)); if (r) { setYaml(''); reload(); } }}>
            Install pack
          </button>
        </div>
      </div>
      <h2>Search and ask</h2>
      {list.length > 0 ? (
        <fieldset>
          <legend>Packs to use (none selected means all)</legend>
          {list.map((p) => (
            <label key={p.id} className="inline">
              <input type="checkbox" checked={selected.includes(p.id)} onChange={(e) => setSelected(e.target.checked ? [...selected, p.id] : selected.filter((x) => x !== p.id))} /> {p.id}
            </label>
          ))}
        </fieldset>
      ) : null}
      <div className="form-actions">
        <label>
          Search
          <input aria-label="Search packs" value={query} onChange={(e) => setQuery(e.target.value)} />
        </label>
        <button disabled={act.busy !== '' || !query.trim() || ids.length === 0} onClick={async () => setResult(await act.run('search', () => client.packSearch(ids, query.trim(), 8)))}>
          Search
        </button>
      </div>
      {result ? <SearchResultView r={result} /> : null}
      <div className="form-actions">
        <label>
          Ask a question (uses a model call and may cost a little)
          <input aria-label="Ask the packs" value={question} onChange={(e) => setQuestion(e.target.value)} />
        </label>
        <button disabled={act.busy !== '' || !question.trim() || ids.length === 0} onClick={async () => setConsult(await act.run('consult', () => client.packConsult(ids, question.trim())))}>
          Consult
        </button>
      </div>
      {consult ? <ConsultView r={consult} /> : null}
    </Page>
  );
}
