import { useEffect, useRef, useState, type FormEvent } from 'react';
import type { ExternalItem, ExternalItemDetail, IntegrationRule, IntegrationTestReport, IntegrationView, NotionPageText, Outbox } from '../api/extTypes';
import type { Project } from '../api/types';
import { Markdown, safeHref } from '../lib/Markdown';
import { formatDateTime } from '../lib/format';
import { useAction, numOrUndef } from '../lib/useAction';
import { useClient } from '../state/context';
import { useLive } from '../state/hooks';
import { Badge, ErrorNote, Page } from '../components/common';
import { ReadOnlyNote, useReadOnly } from '../state/license';

const KINDS: { kind: string; label: string; config: { key: string; label: string }[] }[] = [
  { kind: 'linear', label: 'Linear', config: [{ key: 'team', label: 'Team key (optional)' }] },
  { kind: 'notion', label: 'Notion', config: [{ key: 'query', label: 'Search query (optional)' }] },
  { kind: 'sentry', label: 'Sentry', config: [{ key: 'organization', label: 'Organization' }, { key: 'project', label: 'Project' }, { key: 'query', label: 'Query (optional)' }] },
  { kind: 'aikido', label: 'Aikido', config: [{ key: 'workspace', label: 'Workspace (optional)' }, { key: 'repo', label: 'Repository (optional)' }] },
];

/** The injection-risk score the core computed for outside text, with the reasons. 0 is low; 3 and above are never turned into tasks by a rule. */
export function RiskBadge({ risk, flags }: { risk: number; flags: string[] | null }) {
  const tone = risk >= 3 ? 'err' : risk >= 1 ? 'warn' : 'ok';
  return (
    <Badge tone={tone} title="How much this text looks like an attempt to instruct an agent. Computed by Drydock, not by the vendor.">
      injection risk {risk}
      {(flags ?? []).length ? `: ${(flags ?? []).join(', ')}` : ''}
    </Badge>
  );
}

/** The quoted block exactly as it would appear in a task description. It is data, never instructions. */
export function QuotedBlock({ text }: { text: string }) {
  return (
    <blockquote className="quoted" data-testid="quoted-block">
      <Markdown source={text} maxChars={20000} />
    </blockquote>
  );
}

function ItemRow({ it, projects, onChanged }: { it: ExternalItem; projects: Project[]; onChanged: () => void }) {
  const client = useClient();
  const act = useAction();
  const [detail, setDetail] = useState<ExternalItemDetail | undefined>();
  const [page, setPage] = useState<NotionPageText | undefined>();
  const [project, setProject] = useState(it.projectId || projects[0]?.id || '');
  const href = safeHref(it.url);
  return (
    <li className="gh-issue" data-testid="ext-item" data-item={it.id}>
      <header className="msg-head">
        <strong>{it.issueRef ? `${it.issueRef} ` : ''}{it.title}</strong>
        <Badge>{it.kind}</Badge>
        <Badge>{it.state}</Badge>
        {(it.labels ?? []).map((l) => <Badge key={l}>{l}</Badge>)}
        <Badge tone="warn" title="Text from outside Drydock: data, never instructions">untrusted</Badge>
        <RiskBadge risk={it.risk} flags={it.riskFlags} />
        {it.gone ? <Badge tone="muted">gone from the vendor</Badge> : null}
        {href ? <a href={href} target="_blank" rel="noopener noreferrer" title={href}>open</a> : null}
      </header>
      {it.excerpt ? <Markdown source={it.excerpt} maxChars={600} /> : null}
      {it.redactions > 0 ? <p className="muted">{it.redactions} secret(s) were redacted from this text before it was stored.</p> : null}
      {(it.warnings ?? []).map((w, i) => <p key={i} className="warn-note">{w}</p>)}
      <ErrorNote error={act.error} />
      <div className="form-actions">
        <button onClick={async () => setDetail(detail ? undefined : await act.run('detail', () => client.integrationItem(it.id)))}>{detail ? 'Hide text' : 'Show full text'}</button>
        {it.kind === 'page' ? (
          <button onClick={async () => setPage(await act.run('page', () => client.integrationNotionPage(it.id)))}>Read as requirements text</button>
        ) : null}
        {it.taskId ? (
          <a href={`#/task/${encodeURIComponent(it.taskId)}`}>Imported as a task</a>
        ) : it.kind !== 'page' ? (
          <>
            <select aria-label={`Project for ${it.title}`} value={project} onChange={(e) => setProject(e.target.value)}>
              {projects.map((p) => <option key={p.id} value={p.id}>{p.name}</option>)}
            </select>
            <button disabled={act.busy !== '' || it.risk >= 3} title={it.risk >= 3 ? 'High injection risk: import is blocked' : 'Create a backlog task. The text is quoted as data.'} onClick={async () => { const r = await act.run('import', () => client.integrationImport(it.id, project)); if (r) { onChanged(); window.location.hash = `#/task/${encodeURIComponent(r.taskId)}`; } }}>
              Import as a task
            </button>
          </>
        ) : null}
      </div>
      {detail ? <QuotedBlock text={detail.quoted || detail.body} /> : null}
      {page ? (
        <div data-testid="notion-page">
          <p className="muted">Requirements text from <code>{page.sourceRef}</code> <RiskBadge risk={page.risk} flags={page.riskFlags} />{page.truncated ? ' (truncated)' : ''}</p>
          <QuotedBlock text={page.text} />
        </div>
      ) : null}
    </li>
  );
}

function Outbound({ conn, projects }: { conn: IntegrationView; projects: Project[] }) {
  const client = useClient();
  const act = useAction();
  const ro = useReadOnly();
  const q = useLive(() => client.integrationOutboundList({ integrationId: conn.id, limit: 50 }), [conn.id], (e) => e.type.startsWith('integration.') || e.type.startsWith('approval.'));
  const ops = conn.tools.ops ?? [];
  const [op, setOp] = useState(ops[0]?.op ?? '');
  const [target, setTarget] = useState('');
  const [params, setParams] = useState('');
  const [project, setProject] = useState(conn.projectId || projects[0]?.id || '');
  const cur = ops.find((o) => o.op === op);
  const parse = () => Object.fromEntries(params.split('\n').map((l) => l.trim()).filter(Boolean).map((l) => { const i = l.indexOf('='); return i < 0 ? [l, ''] : [l.slice(0, i).trim(), l.slice(i + 1)]; }));
  const rows: Outbox[] = q.data ?? [];
  return (
    <section aria-label="Outbound writes">
      <h4>Outbound writes (need your approval)</h4>
      <p className="muted">Nothing is sent to the vendor until you approve the exact text below. Approval is bound to a hash of this exact call; a changed call is refused.</p>
      <ErrorNote error={q.error ?? act.error} />
      {ops.length === 0 ? <p className="muted">This connection offers no write operations.</p> : (
        <div className="form">
          <label>
            Operation
            <select aria-label="Operation" value={op} onChange={(e) => setOp(e.target.value)}>
              {ops.map((o) => <option key={o.op} value={o.op}>{o.label}</option>)}
            </select>
          </label>
          {cur ? <p className="muted">{cur.description}{cur.neverAuto ? ' This operation can never be pre-approved.' : ''} Parameters: {(cur.params ?? []).join(', ') || 'none'}</p> : null}
          <label>
            Target (the external id of the item or page)
            <input value={target} onChange={(e) => setTarget(e.target.value)} spellCheck={false} />
          </label>
          <label>
            Parameters (one key=value per line)
            <textarea value={params} onChange={(e) => setParams(e.target.value)} rows={3} />
          </label>
          <label>
            Project
            <select aria-label="Outbound project" value={project} onChange={(e) => setProject(e.target.value)}>
              {projects.map((p) => <option key={p.id} value={p.id}>{p.name}</option>)}
            </select>
          </label>
          <div className="form-actions">
            <button disabled={act.busy !== '' || !op || !target.trim()} onClick={async () => { const r = await act.run('propose', () => client.integrationOutboundPropose({ integrationId: conn.id, op, target: target.trim(), projectId: project, params: parse() })); if (r) { setTarget(''); setParams(''); q.reload(); } }}>
              Propose this write
            </button>
          </div>
        </div>
      )}
      {rows.map((o) => (
        <div key={o.id} className="gh-review" data-testid="outbox" data-status={o.status} data-outbox={o.id}>
          <div>
            <Badge tone={o.status === 'sent' ? 'ok' : o.status === 'awaiting_approval' || o.status === 'approved' ? 'warn' : 'muted'}>{o.status}</Badge> <strong>{o.op}</strong> on <code>{o.target}</code>{' '}
            <span className="muted">proposed by {o.createdBy}; approval <code data-testid="approval-id">{o.approvalId}</code>; hash {o.payloadHash.slice(0, 10)}</span>
          </div>
          <p className="muted">Exactly what would be sent:</p>
          <pre className="md-code" tabIndex={0}>{o.preview}</pre>
          {o.error ? <p className="error-note">{o.error}</p> : null}
          {o.receipt ? <p className="muted">Receipt: {o.receipt}</p> : null}
          <div className="form-actions">
            {o.status === 'awaiting_approval' ? (
              <>
                <button className="primary" disabled={act.busy !== '' || ro.readOnly} title={ro.readOnly ? ro.reason : undefined} onClick={async () => { await act.run('approve', () => client.integrationOutboundDecide({ id: o.id, approve: true })); q.reload(); }}>Approve this exact text</button>
                <button disabled={act.busy !== ''} onClick={async () => { await act.run('decline', () => client.integrationOutboundDecide({ id: o.id, approve: false })); q.reload(); }}>Decline</button>
              </>
            ) : null}
            {o.status === 'approved' ? (
              <button className="primary" disabled={act.busy !== '' || ro.readOnly} title={ro.readOnly ? ro.reason : undefined} onClick={async () => { await act.run('execute', () => client.integrationOutboundExecute(o.id)); q.reload(); }}>Send now</button>
            ) : null}
          </div>
        </div>
      ))}
    </section>
  );
}

function Rules({ conn, projects }: { conn: IntegrationView; projects: Project[] }) {
  const client = useClient();
  const act = useAction();
  const q = useLive(() => client.integrationRulesList(conn.id), [conn.id], (e) => e.type.startsWith('integration.'));
  const [name, setName] = useState('');
  const [when, setWhen] = useState('item.state == "open"');
  const [maxPerDay, setMax] = useState('5');
  const [project, setProject] = useState(conn.projectId || projects[0]?.id || '');
  const rules: IntegrationRule[] = q.data ?? [];
  const set = (r: IntegrationRule, patch: Partial<{ enabled: boolean; dryRun: boolean }>) => async () => {
    await act.run('rule', () => client.integrationRulesSet({ id: r.id, version: r.version, integrationId: r.integrationId, projectId: r.projectId, name: r.name, when: r.when, action: r.action, ...patch }));
    q.reload();
  };
  return (
    <section aria-label="Rules">
      <h4>Rules (create tasks automatically)</h4>
      <p className="muted">A rule creates backlog tasks from matching items. Only you can create, change or enable a rule. A new rule starts disabled and in dry-run: it lists what it would create and creates nothing until you turn both on. Items with an injection risk of 3 or more are never created by a rule, and item text cannot set a rule parameter.</p>
      <ErrorNote error={q.error ?? act.error} what="Rules" />
      {rules.map((r) => (
        <div key={r.id} className="gh-review" data-testid="int-rule" data-rule={r.id}>
          <div>
            <strong>{r.name}</strong> <Badge tone={r.enabled ? 'ok' : 'muted'}>{r.enabled ? 'enabled' : 'disabled'}</Badge> <Badge tone={r.dryRun ? 'info' : 'warn'}>{r.dryRun ? 'dry-run' : 'live'}</Badge> <span className="muted">{r.action}, at most {r.maxPerDay}/day, {r.createdLast24h ?? 0} in 24h, v{r.version}</span>
          </div>
          <div>when <code>{r.when}</code></div>
          {(r.matches ?? []).length > 0 ? <p className="muted">{(r.matches ?? []).length} match(es) recorded (dry-run listings included).</p> : null}
          <div className="form-actions">
            <button onClick={set(r, { enabled: !r.enabled })}>{r.enabled ? 'Disable' : 'Enable'}</button>
            <button onClick={set(r, { dryRun: !r.dryRun })}>{r.dryRun ? 'Switch to live' : 'Switch to dry-run'}</button>
            <button onClick={async () => { await act.run('delete', () => client.integrationRulesSet({ id: r.id, version: r.version, integrationId: r.integrationId, projectId: r.projectId, name: r.name, when: r.when, action: r.action, delete: true })); q.reload(); }}>Delete</button>
          </div>
        </div>
      ))}
      <div className="form">
        <label>
          Rule name
          <input value={name} onChange={(e) => setName(e.target.value)} />
        </label>
        <label>
          When (CEL over <code>item</code>: source, kind, state, title, labels, priority, level, risk, ...)
          <input value={when} onChange={(e) => setWhen(e.target.value)} spellCheck={false} />
        </label>
        <label>
          At most per day
          <input inputMode="numeric" value={maxPerDay} onChange={(e) => setMax(e.target.value)} />
        </label>
        <label>
          Project for the tasks
          <select aria-label="Rule project" value={project} onChange={(e) => setProject(e.target.value)}>
            {projects.map((p) => <option key={p.id} value={p.id}>{p.name}</option>)}
          </select>
        </label>
        <div className="form-actions">
          <button disabled={act.busy !== '' || !name.trim() || !when.trim()} onClick={async () => { const r = await act.run('create', () => client.integrationRulesSet({ integrationId: conn.id, projectId: project, name: name.trim(), when, action: 'create_task', maxPerDay: numOrUndef(maxPerDay) })); if (r) { setName(''); q.reload(); } }}>
            Add rule (starts disabled, dry-run)
          </button>
        </div>
      </div>
    </section>
  );
}

function ReportView({ r }: { r: IntegrationTestReport }) {
  return (
    <div className="report" data-testid="int-report" data-ok={r.ok}>
      <p><Badge tone={r.ok ? 'ok' : 'err'}>{r.ok ? 'passed' : 'did not pass'}</Badge> {r.summary}</p>
      <ol className="steps">
        {(r.steps ?? []).map((s) => <li key={s.id}><span aria-hidden="true">{s.ok ? '✔' : '✖'}</span> {s.name}: {s.ok ? 'ok' : 'failed'} <span className="muted">{s.detail}</span></li>)}
      </ol>
      {(r.toolsMissing ?? []).length > 0 ? <p className="warn-note">The server does not offer: {(r.toolsMissing ?? []).join(', ')}</p> : null}
      {(r.writesMissing ?? []).length > 0 ? <p className="warn-note">Write operations unavailable: {(r.writesMissing ?? []).join(', ')}</p> : null}
    </div>
  );
}

function ConnectionCard({ c, projects, onChanged }: { c: IntegrationView; projects: Project[]; onChanged: () => void }) {
  const client = useClient();
  const act = useAction();
  const [report, setReport] = useState<IntegrationTestReport | undefined>();
  const [sync, setSync] = useState<string | undefined>();
  const [confirm, setConfirm] = useState(false);
  const [show, setShow] = useState<'items' | 'rules' | 'outbound' | ''>('');
  const items = useLive(() => client.integrationItems({ integrationId: c.id, limit: 50 }), [c.id, c.sync.itemsSeen], (e) => e.type.startsWith('integration.'), show === 'items');
  const sy = c.sync;
  return (
    <article className="card" data-testid="integration" data-kind={c.kind} data-name={c.name}>
      <header className="card-head">
        <h3>{c.name}</h3>
        <Badge>{c.kind}</Badge>
        <Badge tone={c.status === 'ok' ? 'ok' : 'warn'}>{c.status}</Badge>
        <Badge tone={sy.state === 'ok' ? 'ok' : sy.state === 'error' || sy.state === 'unauthenticated' ? 'err' : 'muted'}>sync: {sy.state}</Badge>
      </header>
      {c.statusDetail ? <p className="muted">{c.statusDetail}</p> : null}
      <dl className="kv">
        <dt>Credential</dt>
        <dd>{c.hasCredential ? 'stored in the credential store (never shown)' : 'none'}</dd>
        <dt>Items stored</dt>
        <dd>{sy.itemsSeen}</dd>
        <dt>Last successful sync</dt>
        <dd>{sy.lastSuccessAt ? formatDateTime(sy.lastSuccessAt) : 'never'}</dd>
        <dt>Polling every</dt>
        <dd>{c.pollIntervalS ? `${c.pollIntervalS} s` : 'default'}</dd>
      </dl>
      {sy.lastError ? <p className="error-note">{sy.lastError}</p> : null}
      <ErrorNote error={act.error} />
      {report ? <ReportView r={report} /> : null}
      {sync ? <p role="status">{sync}</p> : null}
      <div className="form-actions">
        <button disabled={act.busy !== ''} onClick={async () => { const r = await act.run('test', () => client.integrationTest(c.id)); if (r) setReport(r); onChanged(); }}>Test connection</button>
        <button disabled={act.busy !== ''} onClick={async () => { const r = await act.run('sync', () => client.integrationSync(c.id, false)); if (r) setSync(r.skipped ? `Skipped: ${r.skipped}` : `${r.seen} seen: ${r.new} new, ${r.changed} changed, ${r.gone} gone.`); onChanged(); }}>Sync now</button>
        <button onClick={() => setShow(show === 'items' ? '' : 'items')} aria-expanded={show === 'items'}>Items</button>
        <button onClick={() => setShow(show === 'rules' ? '' : 'rules')} aria-expanded={show === 'rules'}>Rules</button>
        <button onClick={() => setShow(show === 'outbound' ? '' : 'outbound')} aria-expanded={show === 'outbound'}>Outbound</button>
        {confirm ? (
          <>
            <button className="danger" onClick={async () => { await act.run('disconnect', () => client.integrationDisconnect(c.id)); onChanged(); }}>Yes, disconnect {c.name}</button>
            <button onClick={() => setConfirm(false)}>Keep</button>
          </>
        ) : (
          <button onClick={() => setConfirm(true)}>Disconnect</button>
        )}
      </div>
      {show === 'items' ? (
        <section aria-label="Items">
          <ErrorNote error={items.error} />
          {items.data && items.data.length === 0 ? <p className="muted">No items yet. Run a sync.</p> : null}
          <ul className="gh-issues">
            {(items.data ?? []).map((it) => <ItemRow key={it.id} it={it} projects={projects} onChanged={items.reload} />)}
          </ul>
        </section>
      ) : null}
      {show === 'rules' ? <Rules conn={c} projects={projects} /> : null}
      {show === 'outbound' ? <Outbound conn={c} projects={projects} /> : null}
    </article>
  );
}

function ConnectForm({ projects, onConnected }: { projects: Project[]; onConnected: () => void }) {
  const client = useClient();
  const act = useAction();
  const ro = useReadOnly();
  const tokenRef = useRef<HTMLInputElement>(null);
  const [kind, setKind] = useState('linear');
  const [name, setName] = useState('');
  const [project, setProject] = useState(projects[0]?.id ?? '');
  const [cfg, setCfg] = useState<Record<string, string>>({});
  const [baseUrl, setBaseUrl] = useState('');
  const [result, setResult] = useState<IntegrationTestReport | undefined>();
  const [oauth, setOAuth] = useState<{ ref: string; url?: string; signedIn: boolean } | undefined>();
  const spec = KINDS.find((k) => k.kind === kind)!;
  const canOAuth = kind !== 'aikido';
  // while a sign-in is open, ask the core how it went
  useEffect(() => {
    if (!oauth || oauth.signedIn) return;
    const id = oauth.ref.replace('oauth/mcplib/', '');
    const timer = setInterval(async () => {
      try {
        const st = await client.mcpOAuthStatus(id);
        if (st.signedIn) setOAuth((o) => (o ? { ...o, signedIn: true } : o));
      } catch {
        /* the next poll tries again */
      }
    }, 1000);
    return () => clearInterval(timer);
  }, [client, oauth]);
  const submit = async (e: FormEvent) => {
    e.preventDefault();
    // The token lives only in the uncontrolled input: read once and cleared before the request leaves.
    const token = tokenRef.current?.value ?? '';
    if (tokenRef.current) tokenRef.current.value = '';
    const r = await act.run('connect', () =>
      client.integrationConnect({ kind, name: name.trim() || spec.label, projectId: project, token: oauth?.signedIn ? undefined : token || undefined, oauthRef: oauth?.signedIn ? oauth.ref : undefined, baseUrl: baseUrl.trim() || undefined, config: Object.fromEntries(Object.entries(cfg).filter(([, v]) => v.trim())) }),
    );
    if (r) {
      setResult(r.test);
      setName('');
      onConnected();
    }
  };
  return (
    <form className="form form-grid" onSubmit={submit} autoComplete="off">
      <ErrorNote error={act.error} what="Not connected" />
      <label>
        Service
        <select aria-label="Service" value={kind} onChange={(e) => { setKind(e.target.value); setCfg({}); }}>
          {KINDS.map((k) => <option key={k.kind} value={k.kind}>{k.label}</option>)}
        </select>
      </label>
      <label>
        Name
        <input value={name} onChange={(e) => setName(e.target.value)} placeholder={spec.label} />
      </label>
      <label>
        Project
        <select aria-label="Project" value={project} onChange={(e) => setProject(e.target.value)}>
          {projects.map((p) => <option key={p.id} value={p.id}>{p.name}</option>)}
        </select>
      </label>
      {oauth?.signedIn ? (
        <p data-testid="integration-oauth" className="span-all">
          Signed in to {spec.label} with OAuth. Drydock keeps and refreshes the sign-in; no token to paste.
        </p>
      ) : (
        <label>
          Access token (stored in the credential store, never shown again)
          <input ref={tokenRef} type="password" autoComplete="new-password" spellCheck={false} name="integration-token" aria-label="Access token" />
        </label>
      )}
      {canOAuth && !oauth?.signedIn ? (
        <p className="span-all">
          <button
            type="button"
            onClick={async () => {
              const r = await act.run('oauth', () => client.integrationOAuthBegin(kind, baseUrl.trim() || undefined));
              if (r) setOAuth({ ref: r.oauthRef, url: r.status.url, signedIn: !!r.status.signedIn });
            }}
          >
            Sign in with OAuth instead
          </button>{' '}
          {oauth?.url && safeHref(oauth.url) ? (
            <a href={safeHref(oauth.url)} target="_blank" rel="noreferrer noopener">
              Open the {spec.label} sign-in page
            </a>
          ) : null}
        </p>
      ) : null}
      {spec.config.map((f) => (
        <label key={f.key}>
          {f.label}
          <input value={cfg[f.key] ?? ''} onChange={(e) => setCfg({ ...cfg, [f.key]: e.target.value })} />
        </label>
      ))}
      <details className="span-all">
        <summary>Advanced</summary>
        <label>
          Server URL (only if the vendor server is self-hosted)
          <input value={baseUrl} onChange={(e) => setBaseUrl(e.target.value)} spellCheck={false} />
        </label>
      </details>
      <ReadOnlyNote />
      <div className="form-actions">
        <button className="primary" type="submit" disabled={act.busy !== '' || ro.readOnly || !project} title={ro.readOnly ? ro.reason : undefined}>Connect</button>
      </div>
      {result ? <ReportView r={result} /> : null}
    </form>
  );
}

export default function IntegrationsView({ projects }: { projects: Project[] }) {
  const client = useClient();
  const q = useLive(() => client.integrationList(), [], (e) => e.type.startsWith('integration.'));
  const list: IntegrationView[] = q.data ?? [];
  return (
    <Page title="Integrations">
      <p className="muted">
        Linear, Notion, Sentry and Aikido are read through their MCP servers with a token you paste here. Everything they return is outside text: it is redacted, scored for
        injection risk, shown inert and quoted as data when you import it. Writing back needs your approval of the exact text. Nothing here was run against a live vendor
        account when this page was built; Test connection tells you what the server does and does not offer.
      </p>
      <ErrorNote error={q.error} what="Integrations" />
      {q.data && list.length === 0 ? <p className="muted">Nothing is connected.</p> : null}
      <div className="cards single">
        {list.map((c) => <ConnectionCard key={c.id} c={c} projects={projects} onChanged={q.reload} />)}
      </div>
      <h2>Connect a service</h2>
      <ConnectForm projects={projects} onConnected={q.reload} />
    </Page>
  );
}
