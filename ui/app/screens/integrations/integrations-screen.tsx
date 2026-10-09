// Integrations: Linear, Notion, Sentry and Aikido read through their MCP servers. Everything they return is outside text: it is
// redacted, scored for injection risk, shown inert and quoted as data when imported. Writing back needs the human's approval of
// the exact text, and rules that create tasks start disabled and in dry-run.

import { css, navigate, on, ref, type Handle } from 'remix/component';
import {
  BadgeCheck,
  Blocks,
  Check,
  ExternalLink,
  FileText,
  ListChecks,
  LogIn,
  Plus,
  RefreshCw,
  Send,
  ShieldAlert,
  Stethoscope,
  Trash2,
  Unplug,
  Workflow,
  X,
} from 'lucide';
import type { ExternalItem, ExternalItemDetail, IntegrationRule, IntegrationTestReport, IntegrationView, NotionPageText, Outbox } from '../../api/extTypes.ts';
import type { Project } from '../../api/types.ts';
import { formatDateTime } from '../../lib/format.ts';
import { routes } from '../../routes.ts';
import { ReadOnlyNote } from '../../shell/license-banner.tsx';
import { action, query, services, watch } from '../../state/services.tsx';
import { Button, buttonStyle } from '../../ui/button.tsx';
import { Avatar, Badge, faint, mono, muted } from '../../ui/data.tsx';
import { Empty, ErrorNote, Loading, Notice } from '../../ui/feedback.tsx';
import { Field, formActions, formGrid } from '../../ui/form.tsx';
import { Icon } from '../../ui/icon.tsx';
import { card, Page, Section, stack } from '../../ui/layout.tsx';
import { Markdown, safeHref } from '../../ui/markdown.tsx';
import { t, tone } from '../../ui/tokens.ts';
import { canOAuth, KINDS, oauthLibraryId, outboxTone, parseParams, riskText, riskTone, syncText, syncTone } from './integrations.ts';

type OpenTask = (href: string) => void;
const defaultOpen: OpenTask = (href) => void navigate(href);

const introCss = css({ margin: '0', color: t.textMuted, fontSize: t.textMd, maxWidth: '86ch' });
const head = css({
  display: 'flex',
  alignItems: 'center',
  gap: t.s2,
  flexWrap: 'wrap',
  minWidth: '0',
  '& h3': { fontSize: t.textLg, fontWeight: '600', margin: '0' },
});
const cardCss = css({ display: 'grid', gap: t.s3, '& p': { margin: '0' } });
const stats = css({
  display: 'grid',
  gridTemplateColumns: 'repeat(auto-fill, minmax(160px, 1fr))',
  gap: '1px',
  margin: '0',
  border: `1px solid ${t.border}`,
  borderRadius: t.rMd,
  background: t.border,
  overflow: 'hidden',
  '& > div': { padding: `${t.s2} ${t.s3}`, background: t.bgSurface, minWidth: '0' },
  '& dt': { fontSize: t.textXs, color: t.textMuted, fontWeight: '500' },
  '& dd': { margin: '2px 0 0', fontSize: t.textMd, overflowWrap: 'anywhere' },
});
const toolbar = css({ display: 'flex', alignItems: 'center', gap: t.s2, flexWrap: 'wrap' });
const segmented = css({
  display: 'inline-flex',
  padding: '2px',
  gap: '2px',
  borderRadius: t.rMd,
  background: t.bgActive,
  '& > button': { height: '26px', padding: '0 10px', border: '0', borderRadius: t.rSm, background: 'transparent', color: t.textMuted, fontSize: t.textSm, fontWeight: '500' },
  '& > button:hover': { color: t.text },
  '& > button[aria-expanded="true"]': { background: t.bgRaised, color: t.text, boxShadow: t.shadowSm },
});
const push = css({ marginLeft: 'auto', display: 'flex', gap: t.s2, alignItems: 'center', flexWrap: 'wrap' });
const panel = css({ display: 'grid', gap: t.s3, paddingTop: t.s3, borderTop: `1px solid ${t.border}`, '& h4': { fontSize: t.textMd, fontWeight: '600', margin: '0' } });
const errLine = css({ padding: '6px 10px', borderRadius: t.rMd, background: t.dangerSoft, fontSize: t.textSm, overflowWrap: 'anywhere' });
const quoted = css({
  margin: '0',
  padding: `${t.s2} ${t.s3}`,
  borderLeft: `3px solid ${t.borderStrong}`,
  borderRadius: `0 ${t.rMd} ${t.rMd} 0`,
  background: t.bgApp,
  fontSize: t.textMd,
  overflowWrap: 'anywhere',
});
const items = css({ display: 'grid', gap: '0', margin: '0', padding: '0', listStyle: 'none', border: `1px solid ${t.border}`, borderRadius: t.rMd, overflow: 'hidden' });
const itemCss = css({ display: 'grid', gap: t.s2, padding: t.s3, borderTop: `1px solid ${t.border}`, '&:first-child': { borderTop: '0' }, '& p': { margin: '0' } });
const itemTitle = css({ fontWeight: '600', overflowWrap: 'anywhere' });
const preCss = css({ margin: '0', maxHeight: '260px', overflow: 'auto', whiteSpace: 'pre-wrap', overflowWrap: 'anywhere' });
const rowCard = css({ display: 'grid', gap: t.s2, padding: t.s3, border: `1px solid ${t.border}`, borderRadius: t.rMd, background: t.bgApp, '& p': { margin: '0' } });
const steps = css({ display: 'grid', gap: '4px', margin: '0', padding: '0', listStyle: 'none', fontSize: t.textMd, '& li': { display: 'flex', gap: t.s2, alignItems: 'baseline' } });
const reportCss = css({ display: 'grid', gap: t.s2, padding: t.s3, borderRadius: t.rMd, border: `1px solid ${t.border}`, background: t.bgApp, '& p': { margin: '0' } });
const serviceGrid = css({
  gridColumn: '1 / -1',
  display: 'grid',
  gridTemplateColumns: 'repeat(auto-fill, minmax(170px, 1fr))',
  gap: t.s2,
  margin: '0',
  padding: '0',
  border: '0',
  '& legend': { fontSize: t.textSm, fontWeight: '500', marginBottom: '6px', padding: '0' },
});
const serviceTile = css({
  display: 'flex',
  alignItems: 'center',
  gap: t.s2,
  padding: `${t.s2} ${t.s3}`,
  border: `1px solid ${t.border}`,
  borderRadius: t.rMd,
  background: t.bgSurface,
  cursor: 'pointer',
  transition: `border-color ${t.fast} ${t.ease}, background-color ${t.fast} ${t.ease}`,
  '&:hover': { borderColor: t.borderStrong },
  '&:has(input:checked)': { borderColor: t.accent, background: t.accentSoft },
  '&:has(input:focus-visible)': { boxShadow: t.focus },
  '& input': { position: 'absolute', opacity: '0', pointerEvents: 'none' },
  '& small': { display: 'block', color: t.textMuted, fontSize: t.textXs },
  '& strong': { fontWeight: '600' },
});
const full = css({ gridColumn: '1 / -1' });
const detailsCss = css({ gridColumn: '1 / -1', '& summary': { cursor: 'pointer', color: t.textMuted, fontSize: t.textSm, marginBottom: t.s2 } });

/** The injection-risk score the core computed for outside text, with the reasons. 0 is low; 3 and above are never turned into tasks by a rule. */
export function RiskBadge(handle: Handle<{ risk: number; flags: string[] | null }>) {
  return () => {
    const { risk, flags } = handle.props;
    return (
      <Badge tone={riskTone(risk)} testId="risk-badge" title="How much this text looks like an attempt to instruct an agent. Computed by Drydock, not by the vendor.">
        {riskText(risk, flags)}
      </Badge>
    );
  };
}

/** The quoted block exactly as it would appear in a task description. It is data, never instructions. */
export function QuotedBlock(handle: Handle<{ text: string }>) {
  return () => (
    <blockquote mix={quoted} data-testid="quoted-block">
      <Markdown source={handle.props.text} maxChars={20000} />
    </blockquote>
  );
}

function ItemRow(handle: Handle<{ it: ExternalItem; projects: Project[]; onChanged: () => void; openTask: OpenTask }>) {
  const s = services(handle);
  const act = action(handle);
  let detail: ExternalItemDetail | undefined;
  let page: NotionPageText | undefined;
  let project = '';
  return () => {
    const { it, projects, onChanged, openTask } = handle.props;
    const chosen = project || it.projectId || projects[0]?.id || '';
    const href = safeHref(it.url);
    return (
      <li mix={itemCss} data-testid="ext-item" data-item={it.id}>
        <div mix={head}>
          <span mix={itemTitle}>
            {it.issueRef ? `${it.issueRef} ` : ''}
            {it.title}
          </span>
          <Badge>{it.kind}</Badge>
          <Badge>{it.state}</Badge>
          {(it.labels ?? []).map((l) => (
            <Badge key={l} tone="accent">
              {l}
            </Badge>
          ))}
          <Badge tone="warn" title="Text from outside Drydock: data, never instructions">
            untrusted
          </Badge>
          <RiskBadge risk={it.risk} flags={it.riskFlags} />
          {it.gone ? <Badge tone="muted">gone from the vendor</Badge> : null}
          {href ? (
            <a href={href} target="_blank" rel="noopener noreferrer" data-rmx-document title={href} mix={[buttonStyle('link', 'sm')]}>
              <Icon icon={ExternalLink} size={13} />
              open
            </a>
          ) : null}
        </div>
        {it.excerpt ? <Markdown source={it.excerpt} maxChars={600} /> : null}
        {it.redactions > 0 ? <p mix={faint}>{it.redactions} secret(s) were redacted from this text before it was stored.</p> : null}
        {(it.warnings ?? []).map((w, i) => (
          <Notice key={i} tone="warn">
            {w}
          </Notice>
        ))}
        <ErrorNote error={act.error} />
        <div mix={toolbar}>
          <Button
            size="sm"
            variant="ghost"
            icon={FileText}
            mix={[
              on('click', async () => {
                detail = detail ? undefined : await act.run('detail', () => s.client.integrationItem(it.id));
                void handle.update();
              }),
            ]}
          >
            {detail ? 'Hide text' : 'Show full text'}
          </Button>
          {it.kind === 'page' ? (
            <Button
              size="sm"
              variant="ghost"
              icon={ListChecks}
              mix={[
                on('click', async () => {
                  page = await act.run('page', () => s.client.integrationNotionPage(it.id));
                  void handle.update();
                }),
              ]}
            >
              Read as requirements text
            </Button>
          ) : null}
          <span mix={push}>
            {it.taskId ? (
              <a href={routes.task.href({ id: it.taskId })}>Imported as a task</a>
            ) : it.kind !== 'page' ? (
              <>
                <select aria-label={`Project for ${it.title}`} value={chosen} style={{ width: 'auto' }} mix={on('change', (e) => ((project = e.currentTarget.value), void handle.update()))}>
                  {projects.map((p) => (
                    <option key={p.id} value={p.id} selected={p.id === chosen}>
                      {p.name}
                    </option>
                  ))}
                </select>
                <Button
                  size="sm"
                  icon={Plus}
                  disabled={act.busy !== '' || it.risk >= 3}
                  title={it.risk >= 3 ? 'High injection risk: import is blocked' : 'Create a backlog task. The text is quoted as data.'}
                  mix={[
                    on('click', async () => {
                      const r = await act.run('import', () => s.client.integrationImport(it.id, chosen));
                      if (r) {
                        onChanged();
                        openTask(routes.task.href({ id: r.taskId }));
                      }
                    }),
                  ]}
                >
                  Import as a task
                </Button>
              </>
            ) : null}
          </span>
        </div>
        {detail ? <QuotedBlock text={detail.quoted || detail.body} /> : null}
        {page ? (
          <div data-testid="notion-page" mix={stack}>
            <p mix={[muted]}>
              Requirements text from <code>{page.sourceRef}</code> <RiskBadge risk={page.risk} flags={page.riskFlags} />
              {page.truncated ? ' (truncated)' : ''}
            </p>
            <QuotedBlock text={page.text} />
          </div>
        ) : null}
      </li>
    );
  };
}

function Outbound(handle: Handle<{ conn: IntegrationView; projects: Project[] }>) {
  const s = services(handle);
  watch(handle, s.workspace);
  const act = action(handle);
  const q = query(handle, {
    load: () => s.client.integrationOutboundList({ integrationId: handle.props.conn.id, limit: 50 }),
    key: () => handle.props.conn.id,
    when: (e) => e.type.startsWith('integration.') || e.type.startsWith('approval.'),
  });
  let op = '';
  let target = '';
  let params = '';
  let project = '';
  const decide = async (o: Outbox, approve: boolean) => {
    await act.run(approve ? 'approve' : 'decline', () => s.client.integrationOutboundDecide({ id: o.id, approve }));
    q.reload();
  };

  return () => {
    const { conn, projects } = handle.props;
    const ro = s.workspace.license;
    const ops = conn.tools.ops ?? [];
    const curOp = op || ops[0]?.op || '';
    const cur = ops.find((o) => o.op === curOp);
    const proj = project || conn.projectId || projects[0]?.id || '';
    const rows: Outbox[] = q.data ?? [];
    return (
      <section aria-label="Outbound writes" mix={panel}>
        <h4>Outbound writes (need your approval)</h4>
        <p mix={faint}>Nothing is sent to the vendor until you approve the exact text below. Approval is bound to a hash of this exact call; a changed call is refused.</p>
        <ErrorNote error={q.error ?? act.error} />
        {ops.length === 0 ? (
          <p mix={muted}>This connection offers no write operations.</p>
        ) : (
          <div mix={formGrid}>
            <Field label="Operation">
              {(id) => (
                <select id={id} aria-label="Operation" value={curOp} mix={on('change', (e) => ((op = e.currentTarget.value), void handle.update()))}>
                  {ops.map((o) => (
                    <option key={o.op} value={o.op} selected={o.op === curOp}>
                      {o.label}
                    </option>
                  ))}
                </select>
              )}
            </Field>
            <Field label="Target (the external id of the item or page)">
              {(id) => <input id={id} value={target} spellcheck={false} mix={on('input', (e) => ((target = e.currentTarget.value), void handle.update()))} />}
            </Field>
            <Field label="Project">
              {(id) => (
                <select id={id} aria-label="Outbound project" value={proj} mix={on('change', (e) => ((project = e.currentTarget.value), void handle.update()))}>
                  {projects.map((p) => (
                    <option key={p.id} value={p.id} selected={p.id === proj}>
                      {p.name}
                    </option>
                  ))}
                </select>
              )}
            </Field>
            {cur ? (
              <p mix={[full, faint]}>
                {cur.description}
                {cur.neverAuto ? ' This operation can never be pre-approved.' : ''} Parameters: {(cur.params ?? []).join(', ') || 'none'}
              </p>
            ) : null}
            <div mix={full}>
              <Field label="Parameters (one key=value per line)">
                {(id) => <textarea id={id} value={params} rows={3} mix={on('input', (e) => ((params = e.currentTarget.value), void handle.update()))} />}
              </Field>
            </div>
            <div mix={formActions}>
              <Button
                icon={Send}
                disabled={act.busy !== '' || !curOp || !target.trim()}
                mix={[
                  on('click', async () => {
                    const r = await act.run('propose', () =>
                      s.client.integrationOutboundPropose({ integrationId: conn.id, op: curOp, target: target.trim(), projectId: proj, params: parseParams(params) }),
                    );
                    if (r) {
                      target = '';
                      params = '';
                      q.reload();
                    }
                  }),
                ]}
              >
                Propose this write
              </Button>
            </div>
          </div>
        )}
        {rows.map((o) => (
          <div key={o.id} mix={rowCard} data-testid="outbox" data-status={o.status} data-outbox={o.id}>
            <div mix={head}>
              <Badge tone={outboxTone(o.status)}>{o.status}</Badge>
              <strong>{o.op}</strong> on <code>{o.target}</code>
              <span mix={faint}>
                proposed by {o.createdBy}; approval <code data-testid="approval-id">{o.approvalId}</code>; hash {o.payloadHash.slice(0, 10)}
              </span>
            </div>
            <p mix={faint}>Exactly what would be sent:</p>
            <pre mix={preCss} tabIndex={0}>
              {o.preview}
            </pre>
            {o.error ? <p mix={errLine}>{o.error}</p> : null}
            {o.receipt ? <p mix={faint}>Receipt: {o.receipt}</p> : null}
            <div mix={toolbar}>
              {o.status === 'awaiting_approval' ? (
                <>
                  <Button
                    size="sm"
                    variant="primary"
                    icon={BadgeCheck}
                    disabled={act.busy !== '' || ro.readOnly}
                    title={ro.readOnly ? ro.reason : undefined}
                    mix={[on('click', () => void decide(o, true))]}
                  >
                    Approve this exact text
                  </Button>
                  <Button size="sm" disabled={act.busy !== ''} mix={[on('click', () => void decide(o, false))]}>
                    Decline
                  </Button>
                </>
              ) : null}
              {o.status === 'approved' ? (
                <Button
                  size="sm"
                  variant="primary"
                  icon={Send}
                  disabled={act.busy !== '' || ro.readOnly}
                  title={ro.readOnly ? ro.reason : undefined}
                  mix={[
                    on('click', async () => {
                      await act.run('execute', () => s.client.integrationOutboundExecute(o.id));
                      q.reload();
                    }),
                  ]}
                >
                  Send now
                </Button>
              ) : null}
            </div>
          </div>
        ))}
      </section>
    );
  };
}

function Rules(handle: Handle<{ conn: IntegrationView; projects: Project[] }>) {
  const s = services(handle);
  const act = action(handle);
  const q = query(handle, {
    load: () => s.client.integrationRulesList(handle.props.conn.id),
    key: () => handle.props.conn.id,
    when: (e) => e.type.startsWith('integration.'),
  });
  let name = '';
  let when = 'item.state == "open"';
  let maxPerDay = '5';
  let project = '';
  const change = async (r: IntegrationRule, patch: Partial<{ enabled: boolean; dryRun: boolean; delete: boolean }>) => {
    await act.run('rule', () =>
      s.client.integrationRulesSet({ id: r.id, version: r.version, integrationId: r.integrationId, projectId: r.projectId, name: r.name, when: r.when, action: r.action, ...patch }),
    );
    q.reload();
  };

  return () => {
    const { conn, projects } = handle.props;
    const rules: IntegrationRule[] = q.data ?? [];
    const proj = project || conn.projectId || projects[0]?.id || '';
    return (
      <section aria-label="Rules" mix={panel}>
        <h4>Rules (create tasks automatically)</h4>
        <p mix={faint}>
          A rule creates backlog tasks from matching items. Only you can create, change or enable a rule. A new rule starts disabled and in dry-run: it lists what it would create and
          creates nothing until you turn both on. Items with an injection risk of 3 or more are never created by a rule, and item text cannot set a rule parameter.
        </p>
        <ErrorNote error={q.error ?? act.error} what="Rules" />
        {rules.map((r) => (
          <div key={r.id} mix={rowCard} data-testid="int-rule" data-rule={r.id}>
            <div mix={head}>
              <strong>{r.name}</strong>
              <Badge tone={r.enabled ? 'ok' : 'muted'}>{r.enabled ? 'enabled' : 'disabled'}</Badge>
              <Badge tone={r.dryRun ? 'info' : 'warn'}>{r.dryRun ? 'dry-run' : 'live'}</Badge>
              <span mix={faint}>
                {r.action}, at most {r.maxPerDay}/day, {r.createdLast24h ?? 0} in 24h, v{r.version}
              </span>
            </div>
            <div>
              when <code>{r.when}</code>
            </div>
            {(r.matches ?? []).length > 0 ? <p mix={faint}>{(r.matches ?? []).length} match(es) recorded (dry-run listings included).</p> : null}
            <div mix={toolbar}>
              <Button size="sm" mix={[on('click', () => void change(r, { enabled: !r.enabled }))]}>
                {r.enabled ? 'Disable' : 'Enable'}
              </Button>
              <Button size="sm" mix={[on('click', () => void change(r, { dryRun: !r.dryRun }))]}>
                {r.dryRun ? 'Switch to live' : 'Switch to dry-run'}
              </Button>
              <Button size="sm" variant="ghost" icon={Trash2} mix={[on('click', () => void change(r, { delete: true }))]}>
                Delete
              </Button>
            </div>
          </div>
        ))}
        <div mix={formGrid}>
          <Field label="Rule name">{(id) => <input id={id} value={name} mix={on('input', (e) => ((name = e.currentTarget.value), void handle.update()))} />}</Field>
          <Field
            label={
              <>
                When (CEL over <code>item</code>: source, kind, state, title, labels, priority, level, risk, ...)
              </>
            }
          >
            {(id) => <input id={id} value={when} spellcheck={false} mix={[mono, on('input', (e) => ((when = e.currentTarget.value), void handle.update()))]} />}
          </Field>
          <Field label="At most per day">
            {(id) => <input id={id} inputMode="numeric" value={maxPerDay} mix={on('input', (e) => ((maxPerDay = e.currentTarget.value), void handle.update()))} />}
          </Field>
          <Field label="Project for the tasks">
            {(id) => (
              <select id={id} aria-label="Rule project" value={proj} mix={on('change', (e) => ((project = e.currentTarget.value), void handle.update()))}>
                {projects.map((p) => (
                  <option key={p.id} value={p.id} selected={p.id === proj}>
                    {p.name}
                  </option>
                ))}
              </select>
            )}
          </Field>
          <div mix={formActions}>
            <Button
              icon={Workflow}
              disabled={act.busy !== '' || !name.trim() || !when.trim()}
              mix={[
                on('click', async () => {
                  const v = maxPerDay.trim();
                  const n = v === '' ? undefined : Number(v);
                  const r = await act.run('create', () =>
                    s.client.integrationRulesSet({ integrationId: conn.id, projectId: proj, name: name.trim(), when, action: 'create_task', maxPerDay: n !== undefined && Number.isFinite(n) ? n : undefined }),
                  );
                  if (r) {
                    name = '';
                    q.reload();
                  }
                }),
              ]}
            >
              Add rule (starts disabled, dry-run)
            </Button>
          </div>
        </div>
      </section>
    );
  };
}

export function ReportView(handle: Handle<{ r: IntegrationTestReport }>) {
  return () => {
    const { r } = handle.props;
    return (
      <div mix={reportCss} data-testid="int-report" data-ok={String(r.ok)}>
        <p>
          <Badge tone={r.ok ? 'ok' : 'err'}>{r.ok ? 'passed' : 'did not pass'}</Badge> {r.summary}
        </p>
        <ol mix={steps}>
          {(r.steps ?? []).map((st) => (
            <li key={st.id}>
              <span style={{ color: st.ok ? tone.ok.fg : tone.err.fg, display: 'inline-flex' }}>
                <Icon icon={st.ok ? Check : X} size={14} />
              </span>
              <span>
                {st.name}: {st.ok ? 'ok' : 'failed'} <span mix={faint}>{st.detail}</span>
              </span>
            </li>
          ))}
        </ol>
        {(r.toolsMissing ?? []).length > 0 ? <Notice tone="warn">The server does not offer: {(r.toolsMissing ?? []).join(', ')}</Notice> : null}
        {(r.writesMissing ?? []).length > 0 ? <Notice tone="warn">Write operations unavailable: {(r.writesMissing ?? []).join(', ')}</Notice> : null}
      </div>
    );
  };
}

function ConnectionCard(handle: Handle<{ c: IntegrationView; projects: Project[]; onChanged: () => void; openTask: OpenTask }>) {
  const s = services(handle);
  const act = action(handle);
  let report: IntegrationTestReport | undefined;
  let sync: string | undefined;
  let confirm = false;
  let show: 'items' | 'rules' | 'outbound' | '' = '';
  const list = query(handle, {
    load: () => s.client.integrationItems({ integrationId: handle.props.c.id, limit: 50 }),
    key: () => [handle.props.c.id, handle.props.c.sync.itemsSeen],
    enabled: () => show === 'items',
    when: (e) => e.type.startsWith('integration.'),
  });
  const toggle = (v: typeof show) => {
    show = show === v ? '' : v;
    if (show === 'items' && list.data) list.reload();
    void handle.update();
  };

  return () => {
    const { c, projects, onChanged, openTask } = handle.props;
    const sy = c.sync;
    const label = KINDS.find((k) => k.kind === c.kind)?.label ?? c.kind;
    return (
      <article mix={[card, cardCss]} data-testid="integration" data-kind={c.kind} data-name={c.name}>
        <header mix={head}>
          <Avatar id={c.kind} name={label} size={32} square />
          <h3>{c.name}</h3>
          <Badge>{c.kind}</Badge>
          <Badge tone={c.status === 'ok' ? 'ok' : 'warn'}>{c.status}</Badge>
          <Badge tone={syncTone(sy.state)}>sync: {sy.state}</Badge>
        </header>
        {c.statusDetail ? <p mix={muted}>{c.statusDetail}</p> : null}
        <dl mix={stats}>
          <div>
            <dt>Credential</dt>
            <dd>{c.hasCredential ? 'stored in the credential store (never shown)' : 'none'}</dd>
          </div>
          <div>
            <dt>Items stored</dt>
            <dd>{sy.itemsSeen}</dd>
          </div>
          <div>
            <dt>Last successful sync</dt>
            <dd>{sy.lastSuccessAt ? formatDateTime(sy.lastSuccessAt) : 'never'}</dd>
          </div>
          <div>
            <dt>Polling every</dt>
            <dd>{c.pollIntervalS ? `${c.pollIntervalS} s` : 'default'}</dd>
          </div>
        </dl>
        {sy.lastError ? <p mix={errLine}>{sy.lastError}</p> : null}
        <ErrorNote error={act.error} />
        {report ? <ReportView r={report} /> : null}
        {sync ? (
          <Notice tone="info" role="status">
            {sync}
          </Notice>
        ) : null}
        <div mix={toolbar}>
          <Button
            size="sm"
            icon={Stethoscope}
            disabled={act.busy !== ''}
            mix={[
              on('click', async () => {
                const r = await act.run('test', () => s.client.integrationTest(c.id));
                if (r) report = r;
                onChanged();
              }),
            ]}
          >
            Test connection
          </Button>
          <Button
            size="sm"
            icon={RefreshCw}
            disabled={act.busy !== ''}
            mix={[
              on('click', async () => {
                const r = await act.run('sync', () => s.client.integrationSync(c.id, false));
                if (r) sync = syncText(r);
                onChanged();
              }),
            ]}
          >
            Sync now
          </Button>
          <span mix={segmented}>
            <button type="button" aria-expanded={show === 'items'} mix={on('click', () => toggle('items'))}>
              Items
            </button>
            <button type="button" aria-expanded={show === 'rules'} mix={on('click', () => toggle('rules'))}>
              Rules
            </button>
            <button type="button" aria-expanded={show === 'outbound'} mix={on('click', () => toggle('outbound'))}>
              Outbound
            </button>
          </span>
          <span mix={push}>
            {confirm ? (
              <>
                <Button
                  size="sm"
                  variant="danger"
                  mix={[
                    on('click', async () => {
                      await act.run('disconnect', () => s.client.integrationDisconnect(c.id));
                      onChanged();
                    }),
                  ]}
                >
                  Yes, disconnect {c.name}
                </Button>
                <Button size="sm" mix={[on('click', () => ((confirm = false), void handle.update()))]}>
                  Keep
                </Button>
              </>
            ) : (
              <Button size="sm" variant="ghost" icon={Unplug} mix={[on('click', () => ((confirm = true), void handle.update()))]}>
                Disconnect
              </Button>
            )}
          </span>
        </div>
        {show === 'items' ? (
          <section aria-label="Items" mix={panel}>
            <ErrorNote error={list.error} />
            {list.loading && !list.data ? <Loading /> : null}
            {list.data && list.data.length === 0 ? <p mix={muted}>No items yet. Run a sync.</p> : null}
            {(list.data ?? []).length > 0 ? (
              <ul mix={items}>
                {(list.data ?? []).map((it) => (
                  <ItemRow key={it.id} it={it} projects={projects} onChanged={list.reload} openTask={openTask} />
                ))}
              </ul>
            ) : null}
          </section>
        ) : null}
        {show === 'rules' ? <Rules conn={c} projects={projects} /> : null}
        {show === 'outbound' ? <Outbound conn={c} projects={projects} /> : null}
      </article>
    );
  };
}

function ConnectForm(handle: Handle<{ projects: Project[]; onConnected: () => void }>) {
  const s = services(handle);
  watch(handle, s.workspace);
  const act = action(handle);
  let tokenEl: HTMLInputElement | undefined;
  let kind = 'linear';
  let name = '';
  let project = '';
  let cfg: Record<string, string> = {};
  let baseUrl = '';
  let result: IntegrationTestReport | undefined;
  let oauth: { ref: string; url?: string; signedIn: boolean; error?: string } | undefined;
  let timer: ReturnType<typeof setInterval> | undefined;
  const stopPoll = () => {
    clearInterval(timer);
    timer = undefined;
  };
  handle.signal.addEventListener('abort', stopPoll);
  // while a sign-in is open, ask the core how it went
  const startPoll = () => {
    stopPoll();
    timer = setInterval(async () => {
      if (!oauth || oauth.signedIn) return stopPoll();
      try {
        const st = await s.client.mcpOAuthStatus(oauthLibraryId(oauth.ref));
        if (st.signedIn && oauth) {
          oauth = { ...oauth, signedIn: true, error: undefined };
          stopPoll();
          void handle.update();
        } else if (st.state === 'failed' && oauth) {
          // a sign-in that failed is said, and the polling stops (it would never succeed by itself)
          oauth = { ...oauth, error: st.error || 'the sign-in failed' };
          stopPoll();
          void handle.update();
        }
      } catch {
        /* the next poll tries again */
      }
    }, 1000);
  };
  const upd = (f: () => void) => {
    f();
    void handle.update();
  };

  const submit = async (e: SubmitEvent) => {
    e.preventDefault();
    const projects = handle.props.projects;
    const spec = KINDS.find((k) => k.kind === kind)!;
    const proj = project || projects[0]?.id || '';
    // The token lives only in the uncontrolled input: read once and cleared before the request leaves.
    const token = tokenEl?.value ?? '';
    if (tokenEl) tokenEl.value = '';
    const r = await act.run('connect', () =>
      s.client.integrationConnect({
        kind,
        name: name.trim() || spec.label,
        projectId: proj,
        token: oauth?.signedIn ? undefined : token || undefined,
        oauthRef: oauth?.signedIn ? oauth.ref : undefined,
        baseUrl: baseUrl.trim() || undefined,
        config: Object.fromEntries(Object.entries(cfg).filter(([, v]) => v.trim())),
      }),
    );
    if (r) {
      result = r.test;
      name = '';
      void handle.update();
      handle.props.onConnected();
    }
  };

  return () => {
    const { projects } = handle.props;
    const ro = s.workspace.license;
    const spec = KINDS.find((k) => k.kind === kind)!;
    const proj = project || projects[0]?.id || '';
    const signHref = oauth?.url ? safeHref(oauth.url) : undefined;
    return (
      <form mix={[card, formGrid, on<HTMLFormElement, 'submit'>('submit', submit)]} autocomplete="off" aria-label="Connect a service">
        <div mix={full}>
          <ErrorNote error={act.error} what="Not connected" />
        </div>
        <fieldset mix={serviceGrid}>
          <legend>Service</legend>
          {KINDS.map((k) => (
            <label key={k.kind} mix={serviceTile}>
              <input
                type="radio"
                name="integration-service"
                value={k.kind}
                checked={kind === k.kind}
                mix={on('change', () =>
                  upd(() => {
                    kind = k.kind;
                    cfg = {};
                  }),
                )}
              />
              <Avatar id={k.kind} name={k.label} size={26} square />
              <span>
                <strong>{k.label}</strong>
                <small>{k.blurb}</small>
              </span>
            </label>
          ))}
        </fieldset>
        <Field label="Name">{(id) => <input id={id} value={name} placeholder={spec.label} mix={on('input', (e) => upd(() => (name = e.currentTarget.value)))} />}</Field>
        <Field label="Project">
          {(id) => (
            <select id={id} aria-label="Project" value={proj} mix={on('change', (e) => upd(() => (project = e.currentTarget.value)))}>
              {projects.map((p) => (
                <option key={p.id} value={p.id} selected={p.id === proj}>
                  {p.name}
                </option>
              ))}
            </select>
          )}
        </Field>
        {oauth?.signedIn ? (
          <div data-testid="integration-oauth" mix={full}>
            <Notice tone="ok">Signed in to {spec.label} with OAuth. Drydock keeps and refreshes the sign-in; no token to paste.</Notice>
          </div>
        ) : (
          <Field label="Access token" hint="Stored in the credential store, never shown again.">
            {(id) => (
              <input
                id={id}
                type="password"
                autocomplete="new-password"
                spellcheck={false}
                name="integration-token"
                aria-label="Access token"
                mix={ref((n: HTMLInputElement) => (tokenEl = n))}
              />
            )}
          </Field>
        )}
        {canOAuth(kind) && !oauth?.signedIn ? (
          <div mix={[full, toolbar]}>
            <Button
              size="sm"
              icon={LogIn}
              mix={[
                on('click', async () => {
                  const r = await act.run('oauth', () => s.client.integrationOAuthBegin(kind, baseUrl.trim() || undefined));
                  if (r) {
                    oauth = { ref: r.oauthRef, url: r.status.url, signedIn: !!r.status.signedIn, error: r.status.state === 'failed' ? r.status.error || 'the sign-in failed' : undefined };
                    void handle.update();
                    if (!oauth.signedIn && !oauth.error) startPoll();
                  }
                }),
              ]}
            >
              Sign in with OAuth instead
            </Button>
            {oauth?.error ? (
              <div mix={full} data-testid="integration-oauth-error">
                <ErrorNote error={new Error(oauth.error)} what={`Could not sign in to ${spec.label}`} />
              </div>
            ) : null}
            {signHref && !oauth?.error ? (
              <a href={signHref} target="_blank" rel="noreferrer noopener" data-rmx-document data-testid="integration-oauth-link" mix={buttonStyle('link', 'sm')}>
                <Icon icon={ExternalLink} size={13} />
                Open the {spec.label} sign-in page
              </a>
            ) : null}
          </div>
        ) : null}
        {spec.config.map((f) => (
          <Field key={`${kind}-${f.key}`} label={f.label}>
            {(id) => <input id={id} value={cfg[f.key] ?? ''} mix={on('input', (e) => upd(() => (cfg = { ...cfg, [f.key]: e.currentTarget.value })))} />}
          </Field>
        ))}
        <details mix={detailsCss}>
          <summary>Advanced</summary>
          <Field label="Server URL (only if the vendor server is self-hosted)">
            {(id) => <input id={id} value={baseUrl} spellcheck={false} mix={on('input', (e) => upd(() => (baseUrl = e.currentTarget.value)))} />}
          </Field>
        </details>
        <div mix={full}>
          <ReadOnlyNote />
        </div>
        <div mix={formActions}>
          <Button variant="primary" type="submit" icon={Blocks} disabled={act.busy !== '' || ro.readOnly || !proj} title={ro.readOnly ? ro.reason : undefined}>
            Connect
          </Button>
        </div>
        {result ? (
          <div mix={full}>
            <ReportView r={result} />
          </div>
        ) : null}
      </form>
    );
  };
}

export function IntegrationsScreen(handle: Handle<{ openTask?: OpenTask }>) {
  const s = services(handle);
  watch(handle, s.workspace);
  const q = query(handle, { load: () => s.client.integrationList(), when: (e) => e.type.startsWith('integration.') });
  return () => {
    const projects = s.workspace.projectList;
    const list: IntegrationView[] = q.data ?? [];
    const openTask = handle.props.openTask ?? defaultOpen;
    return (
      <Page title="Integrations" icon={Blocks} subtitle={q.data ? `${list.length} connected` : undefined}>
        <p mix={introCss}>
          Linear, Notion, Sentry and Aikido are read through their MCP servers with a token you paste here. Everything they return is outside text: it is redacted, scored for
          injection risk, shown inert and quoted as data when you import it. Writing back needs your approval of the exact text. Nothing here was run against a live vendor account
          when this page was built; Test connection tells you what the server does and does not offer.
        </p>
        <ErrorNote error={q.error} what="Integrations" />
        {q.loading && !q.data ? <Loading /> : null}
        {q.data && list.length === 0 ? (
          <div mix={card}>
            <Empty title="Nothing is connected." icon={ShieldAlert}>
              <p>Pick a service below. Its items arrive as untrusted text you can read, score and import.</p>
            </Empty>
          </div>
        ) : null}
        <div mix={stack}>
          {list.map((c) => (
            <ConnectionCard key={c.id} c={c} projects={projects} onChanged={q.reload} openTask={openTask} />
          ))}
        </div>
        <Section title="Connect a service" description="The token goes to the Windows credential store and is never shown again.">
          <ConnectForm projects={projects} onConnected={q.reload} />
        </Section>
      </Page>
    );
  };
}
