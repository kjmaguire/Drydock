// Integrations: Drydock's own connections to Linear, Notion, Sentry, Aikido and GitHub (decision P42). A service is connected
// once, for the whole workspace; assigning it to a project is a separate step that says which part of the service that
// project imports. Each service's name and logo are fixed: clicking Linear opens Linear. What a connection reaches (account,
// workspace, scopes) is read from the service's API, never typed. Everything a service returns is outside text: redacted,
// scored for injection risk, shown inert and quoted as data when imported; writing back needs the human's approval of the
// exact text, and rules that create tasks start disabled and in dry-run. Giving agents these services' tools is the MCP
// screen's business; this page links there.

import { css, navigate, on, ref, type Handle } from 'remix/component';
import {
  BadgeCheck,
  Blocks,
  Check,
  ExternalLink,
  FileText,
  FolderPlus,
  KeyRound,
  ListChecks,
  LogIn,
  Pencil,
  Plus,
  RefreshCw,
  Send,
  Server,
  ShieldAlert,
  Stethoscope,
  Trash2,
  Unplug,
  Workflow,
  X,
} from 'lucide';
import type {
  ExternalItem,
  ExternalItemDetail,
  IntegrationAssignment,
  IntegrationAuthMethod,
  IntegrationChoice,
  IntegrationKindInfo,
  IntegrationOAuthSession,
  IntegrationRule,
  IntegrationTestReport,
  IntegrationView,
  NotionPageText,
  Outbox,
} from '../../api/extTypes.ts';
import type { Project } from '../../api/types.ts';
import { formatDateTime } from '../../lib/format.ts';
import { routes } from '../../routes.ts';
import { ReadOnlyNote } from '../../shell/license-banner.tsx';
import { action, query, services, watch } from '../../state/services.tsx';
import { BrandLogo } from '../../ui/brand-logo.tsx';
import { Button, buttonStyle } from '../../ui/button.tsx';
import { Badge, faint, mono, muted } from '../../ui/data.tsx';
import { ErrorNote, Loading, Notice } from '../../ui/feedback.tsx';
import { Field, formActions, formGrid } from '../../ui/form.tsx';
import { Icon } from '../../ui/icon.tsx';
import { card, Page, stack } from '../../ui/layout.tsx';
import { Markdown, safeHref } from '../../ui/markdown.tsx';
import { t, tone } from '../../ui/tokens.ts';
import { identityLine, KINDS, kindLabel, outboxTone, parseParams, riskText, riskTone, scopeSummary, statusTone, syncText, syncTone, tileState, type ServiceKind } from './integrations.ts';

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
  gridTemplateColumns: 'repeat(auto-fill, minmax(180px, 1fr))',
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
const full = css({ gridColumn: '1 / -1' });
const detailsCss = css({ gridColumn: '1 / -1', '& summary': { cursor: 'pointer', color: t.textMuted, fontSize: t.textSm, marginBottom: t.s2 } });
const tiles = css({
  display: 'grid',
  gridTemplateColumns: 'repeat(auto-fill, minmax(200px, 1fr))',
  gap: t.s2,
  margin: '0',
  padding: '0',
  listStyle: 'none',
});
const tileCss = css({
  display: 'flex',
  alignItems: 'center',
  gap: t.s3,
  width: '100%',
  padding: `${t.s3} ${t.s3}`,
  border: `1px solid ${t.border}`,
  borderRadius: t.rMd,
  background: t.bgSurface,
  color: t.text,
  textAlign: 'left',
  cursor: 'pointer',
  transition: `border-color ${t.fast} ${t.ease}, background-color ${t.fast} ${t.ease}`,
  '&:hover': { borderColor: t.borderStrong },
  '&[aria-pressed="true"]': { borderColor: t.accent, background: t.accentSoft },
  '&:focus-visible': { boxShadow: t.focus, outline: 'none' },
  '& strong': { display: 'block', fontWeight: '600' },
  '& small': { display: 'block', color: t.textMuted, fontSize: t.textXs, overflowWrap: 'anywhere' },
});
const serviceHead = css({
  display: 'flex',
  alignItems: 'center',
  gap: t.s3,
  flexWrap: 'wrap',
  '& h2': { fontSize: t.textXl, fontWeight: '600', margin: '0' },
});
const linkList = css({ display: 'flex', gap: t.s3, flexWrap: 'wrap', margin: '0', padding: '0', listStyle: 'none', fontSize: t.textSm });
const methodGrid = css({ gridColumn: '1 / -1', display: 'flex', gap: t.s2, flexWrap: 'wrap', margin: '0', padding: '0', border: '0', '& legend': { fontSize: t.textSm, fontWeight: '500', marginBottom: '6px', padding: '0' } });
const methodTile = css({
  display: 'flex',
  alignItems: 'center',
  gap: t.s2,
  padding: `6px ${t.s3}`,
  border: `1px solid ${t.border}`,
  borderRadius: t.rMd,
  cursor: 'pointer',
  fontSize: t.textSm,
  '&:has(input:checked)': { borderColor: t.accent, background: t.accentSoft },
  '&:has(input:disabled)': { opacity: '0.6', cursor: 'not-allowed' },
});
const emptyLine = css({ display: 'flex', alignItems: 'center', gap: t.s2, flexWrap: 'wrap', margin: '0', color: t.textMuted, fontSize: t.textMd, '& strong': { color: t.text } });
const identityGrid = css({ gridTemplateColumns: 'repeat(3, minmax(0, 1fr))' });
const codeCss = css({ fontFamily: t.mono, fontSize: t.textLg, fontWeight: '600', letterSpacing: '0.08em' });

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

function Outbound(handle: Handle<{ conn: IntegrationView; projectId: string }>) {
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
  const decide = async (o: Outbox, approve: boolean) => {
    await act.run(approve ? 'approve' : 'decline', () => s.client.integrationOutboundDecide({ id: o.id, approve }));
    q.reload();
  };

  return () => {
    const { conn, projectId } = handle.props;
    const ro = s.workspace.license;
    const ops = conn.tools.ops ?? [];
    const curOp = op || ops[0]?.op || '';
    const cur = ops.find((o) => o.op === curOp);
    const rows: Outbox[] = (q.data ?? []).filter((o) => !projectId || o.projectId === projectId);
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
                      s.client.integrationOutboundPropose({ integrationId: conn.id, op: curOp, target: target.trim(), projectId, params: parseParams(params) }),
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

function Rules(handle: Handle<{ conn: IntegrationView; projectId: string }>) {
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
  const change = async (r: IntegrationRule, patch: Partial<{ enabled: boolean; dryRun: boolean; delete: boolean }>) => {
    await act.run('rule', () =>
      s.client.integrationRulesSet({ id: r.id, version: r.version, integrationId: r.integrationId, projectId: r.projectId, name: r.name, when: r.when, action: r.action, ...patch }),
    );
    q.reload();
  };

  return () => {
    const { conn, projectId } = handle.props;
    const rules: IntegrationRule[] = (q.data ?? []).filter((r) => r.projectId === projectId);
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
          <div mix={formActions}>
            <Button
              icon={Workflow}
              disabled={act.busy !== '' || !name.trim() || !when.trim()}
              mix={[
                on('click', async () => {
                  const v = maxPerDay.trim();
                  const n = v === '' ? undefined : Number(v);
                  const r = await act.run('create', () =>
                    s.client.integrationRulesSet({ integrationId: conn.id, projectId, name: name.trim(), when, action: 'create_task', maxPerDay: n !== undefined && Number.isFinite(n) ? n : undefined }),
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

/** One service on the overview: its fixed logo and name, and whether it is connected. */
function ServiceTile(handle: Handle<{ kind: ServiceKind; conn?: IntegrationView; selected: boolean; onSelect: () => void }>) {
  return () => {
    const { kind, conn, selected, onSelect } = handle.props;
    const assigned = conn?.assignments?.length ?? 0;
    return (
      <li>
        <button
          type="button"
          mix={[tileCss, on('click', onSelect)]}
          aria-pressed={selected ? 'true' : 'false'}
          data-testid="integration-tile"
          data-kind={kind.kind}
          data-connected={conn ? 'true' : 'false'}
        >
          <BrandLogo id={kind.kind} size={32} />
          <span>
            <strong>{kind.label}</strong>
            <small>{conn ? identityLine(conn) : kind.blurb}</small>
            {conn ? <small>{assigned === 0 ? 'Not assigned to a project' : assigned === 1 ? 'Assigned to 1 project' : `Assigned to ${assigned} projects`}</small> : <small>Not connected</small>}
          </span>
        </button>
      </li>
    );
  };
}

/** The test report: each step, and what the service said the credential reaches. */
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
      </div>
    );
  };
}

/** Connect (or reconnect) one service. The secret fields are uncontrolled: read once, cleared before the request leaves. */
function ConnectForm(handle: Handle<{ kind: string; info?: IntegrationKindInfo; reconnectId?: string; onDone: (r: { test?: IntegrationTestReport }) => void }>) {
  const s = services(handle);
  watch(handle, s.workspace);
  const act = action(handle);
  const secrets: Record<string, HTMLInputElement | undefined> = {};
  let method = '';
  let values: Record<string, string> = {};
  let oauth: IntegrationOAuthSession | undefined;
  let timer: ReturnType<typeof setTimeout> | undefined;
  let pollGen = 0; // a poll answers only for the sign-in it was started for
  const gh = query(handle, { load: () => s.client.githubStatus(), enabled: () => handle.props.kind === 'github' });
  const stopPoll = () => {
    pollGen++;
    clearTimeout(timer);
    timer = undefined;
  };
  handle.signal.addEventListener('abort', stopPoll);
  const upd = (f: () => void) => {
    f();
    void handle.update();
  };
  const finish = async (req: Record<string, unknown>) => {
    const r = await act.run('connect', () => s.client.integrationConnect({ kind: handle.props.kind, id: handle.props.reconnectId, ...req } as never));
    if (r) handle.props.onDone(r);
  };
  // while a sign-in is open, ask the core how it went; once signed in, connect with it. One question at a time: the next is
  // asked a second after the last answer, so a slow answer never overlaps another and the sign-in connects once.
  const startPoll = () => {
    stopPoll();
    const gen = pollGen;
    const tick = async () => {
      timer = undefined;
      if (gen !== pollGen || !oauth || oauth.state !== 'waiting') return;
      try {
        const st = await s.client.integrationOAuthStatus(oauth.id);
        if (gen !== pollGen) return;
        if (st.state !== 'waiting') {
          stopPoll();
          oauth = st;
          void handle.update();
          if (st.state === 'signed_in') await finish({ oauthSession: st.id, baseUrl: (values.baseUrl ?? '').trim() || undefined });
          return;
        }
      } catch {
        if (gen !== pollGen) return; // the next poll tries again
      }
      timer = setTimeout(tick, 1000);
    };
    timer = setTimeout(tick, 1000);
  };

  return () => {
    const { kind, info, reconnectId } = handle.props;
    const ro = s.workspace.license;
    const label = info?.label ?? kindLabel(kind);
    const methods: IntegrationAuthMethod[] = info?.auth ?? [];
    const cur = methods.find((m) => m.id === method) ?? methods.find((m) => m.available) ?? methods[0];
    const fields = cur?.fields ?? [];
    const plain = fields.filter((f) => !f.advanced);
    const advanced = fields.filter((f) => f.advanced);
    const signHref = oauth?.url ? safeHref(oauth.url) : undefined;
    const submit = async (e: SubmitEvent) => {
      e.preventDefault();
      if (!cur || cur.id === 'oauth') return;
      const req: Record<string, unknown> = {};
      for (const f of fields) {
        if (f.secret) {
          const el = secrets[f.key];
          req[f.key] = el?.value || undefined;
          if (el) el.value = '';
        } else {
          const v = (values[f.key] ?? f.options?.[0]?.value ?? '').trim();
          req[f.key] = v || undefined;
        }
      }
      await finish(req);
    };
    const renderField = (f: NonNullable<IntegrationAuthMethod['fields']>[number]) => (
      <Field key={`${kind}-${cur?.id}-${f.key}`} label={f.label} hint={f.hint}>
        {(id) =>
          f.secret ? (
            <input
              id={id}
              type="password"
              autocomplete="new-password"
              spellcheck={false}
              name={`integration-${f.key}`}
              aria-label={f.label}
              mix={ref((n: HTMLInputElement) => (secrets[f.key] = n))}
            />
          ) : f.options && f.options.length > 0 ? (
            <select id={id} aria-label={f.label} value={values[f.key] ?? f.options[0]!.value} mix={on('change', (e) => upd(() => (values = { ...values, [f.key]: e.currentTarget.value })))}>
              {f.options.map((o) => (
                <option key={o.value} value={o.value} selected={o.value === (values[f.key] ?? f.options![0]!.value)}>
                  {o.label}
                </option>
              ))}
            </select>
          ) : (
            <input id={id} value={values[f.key] ?? ''} spellcheck={false} mix={on('input', (e) => upd(() => (values = { ...values, [f.key]: e.currentTarget.value })))} />
          )
        }
      </Field>
    );
    return (
      <form mix={[card, formGrid, on<HTMLFormElement, 'submit'>('submit', submit)]} autocomplete="off" aria-label={reconnectId ? `Reconnect ${label}` : `Connect ${label}`}>
        <div mix={full}>
          <ErrorNote error={act.error} what="Not connected" />
        </div>
        {methods.length > 1 ? (
          <fieldset mix={methodGrid}>
            <legend>How to connect</legend>
            {methods.map((m) => (
              <label key={m.id} mix={methodTile} title={m.note}>
                <input type="radio" name={`int-method-${kind}`} value={m.id} checked={cur?.id === m.id} disabled={!m.available} mix={on('change', () => upd(() => (method = m.id)))} />
                {m.label}
              </label>
            ))}
          </fieldset>
        ) : null}
        {cur?.note ? <p mix={[full, faint]}>{cur.note}</p> : null}
        {kind === 'github' ? (
          <div mix={full} data-testid="gh-status">
            {gh.loading && !gh.data ? <Loading label="Checking gh" /> : null}
            {gh.data?.loggedIn ? (
              <Notice tone="ok">gh is signed in as {gh.data.login}. Drydock uses that sign-in and stores no GitHub token.</Notice>
            ) : gh.data ? (
              <Notice tone="warn">{gh.data.error || 'gh is not signed in'}. The Connections screen shows the GitHub CLI.</Notice>
            ) : null}
          </div>
        ) : null}
        {cur?.id === 'oauth' ? (
          <div mix={[full, stack]}>
            {oauth?.state === 'failed' ? <ErrorNote error={new Error(oauth.error || 'the sign-in failed')} what={`Could not sign in to ${label}`} /> : null}
            {oauth?.userCode && oauth.state === 'waiting' ? (
              <Notice tone="info" title="Enter this code on the sign-in page">
                <span mix={codeCss} data-testid="oauth-user-code">
                  {oauth.userCode}
                </span>
              </Notice>
            ) : null}
            <div mix={toolbar}>
              <Button
                icon={LogIn}
                disabled={act.busy !== '' || ro.readOnly || !cur.available}
                mix={[
                  on('click', async () => {
                    const r = await act.run('oauth', () => s.client.integrationOAuthStart(kind, (values.baseUrl ?? '').trim() || undefined));
                    if (r) {
                      oauth = r;
                      void handle.update();
                      startPoll();
                    }
                  }),
                ]}
              >
                {cur.label}
              </Button>
              {signHref && oauth?.state === 'waiting' ? (
                <a href={signHref} target="_blank" rel="noreferrer noopener" data-rmx-document data-testid="integration-oauth-link" mix={buttonStyle('link', 'sm')}>
                  <Icon icon={ExternalLink} size={13} />
                  Open the {label} sign-in page
                </a>
              ) : null}
            </div>
          </div>
        ) : (
          plain.map(renderField)
        )}
        {advanced.length > 0 ? (
          <details mix={detailsCss}>
            <summary>Advanced</summary>
            <div mix={formGrid}>{advanced.map(renderField)}</div>
          </details>
        ) : null}
        <div mix={full}>
          <ReadOnlyNote />
        </div>
        {cur?.id !== 'oauth' ? (
          <div mix={formActions}>
            <Button variant="primary" type="submit" icon={kind === 'github' ? Blocks : KeyRound} disabled={act.busy !== '' || ro.readOnly || !cur} title={ro.readOnly ? ro.reason : undefined}>
              {reconnectId ? `Reconnect ${label}` : kind === 'github' ? 'Use the GitHub CLI sign-in' : `Connect ${label}`}
            </Button>
          </div>
        ) : null}
      </form>
    );
  };
}

/** Assign a connection to a project, or change an assignment's settings. Choices are read from the service on request. */
function AssignForm(handle: Handle<{ conn: IntegrationView; info?: IntegrationKindInfo; projects: Project[]; existing?: IntegrationAssignment; onDone: () => void; onCancel: () => void }>) {
  const s = services(handle);
  const act = action(handle);
  let project = '';
  let cfg: Record<string, string> = { ...(handle.props.existing?.config ?? {}) };
  const choices: Record<string, IntegrationChoice[]> = {};
  return () => {
    const { conn, info, projects, existing, onDone, onCancel } = handle.props;
    const label = info?.label ?? kindLabel(conn.kind);
    const taken = new Set((conn.assignments ?? []).map((a) => a.projectId));
    const free = projects.filter((p) => !taken.has(p.id));
    const proj = existing?.projectId ?? (project || free[0]?.id || '');
    const settings = info?.settings ?? [];
    return (
      <form
        mix={[formGrid, on<HTMLFormElement, 'submit'>('submit', (e) => e.preventDefault())]}
        aria-label={existing ? `Settings of ${label} for this project` : `Assign ${label} to a project`}
        data-testid="assign-form"
      >
        <div mix={full}>
          <ErrorNote error={act.error} what="Not assigned" />
        </div>
        {existing ? null : (
          <Field label="Project">
            {(id) =>
              free.length === 0 ? (
                <p id={id} mix={muted}>
                  Every project already has {label}.
                </p>
              ) : (
                <select id={id} aria-label="Project" value={proj} mix={on('change', (e) => ((project = e.currentTarget.value), void handle.update()))}>
                  {free.map((p) => (
                    <option key={p.id} value={p.id} selected={p.id === proj}>
                      {p.name}
                    </option>
                  ))}
                </select>
              )
            }
          </Field>
        )}
        {settings.map((st) => {
          const listId = `choices-${handle.id}-${st.key}`;
          const opts = choices[st.key];
          return (
            <Field key={st.key} label={`${st.label}${st.required ? '' : ' (optional)'}`} hint={st.hint}>
              {(id) => (
                <div mix={toolbar}>
                  <input
                    id={id}
                    value={cfg[st.key] ?? ''}
                    spellcheck={false}
                    list={opts ? listId : undefined}
                    style={{ flex: '1', minWidth: '0' }}
                    mix={on('input', (e) => ((cfg = { ...cfg, [st.key]: e.currentTarget.value }), void handle.update()))}
                  />
                  {opts ? (
                    <datalist id={listId}>
                      {opts.map((o) => (
                        <option key={o.value} value={o.value}>
                          {o.label}
                        </option>
                      ))}
                    </datalist>
                  ) : null}
                  {st.choices ? (
                    <Button
                      size="sm"
                      variant="ghost"
                      disabled={act.busy !== ''}
                      title={`Read the ${st.label.toLowerCase()} choices from ${label}`}
                      aria-label={`List the ${st.label} choices from ${label}`}
                      mix={[
                        on('click', async () => {
                          const r = await act.run(`choices-${st.key}`, () => s.client.integrationChoices(conn.id, st.key, cfg));
                          if (r) {
                            choices[st.key] = r;
                            void handle.update();
                          }
                        }),
                      ]}
                    >
                      {act.busy === `choices-${st.key}` ? 'Loading...' : `List from ${label}`}
                    </Button>
                  ) : null}
                </div>
              )}
            </Field>
          );
        })}
        <div mix={formActions}>
          <Button
            variant="primary"
            icon={existing ? Check : FolderPlus}
            disabled={act.busy !== '' || !proj}
            mix={[
              on('click', async () => {
                const config = Object.fromEntries(Object.entries(cfg).filter(([, v]) => v.trim()));
                const r = await act.run('assign', () => s.client.integrationAssign({ integrationId: conn.id, projectId: proj, config, version: existing?.version }));
                if (r) onDone();
              }),
            ]}
          >
            {existing ? 'Save settings' : 'Assign'}
          </Button>
          <Button mix={[on('click', onCancel)]}>Cancel</Button>
        </div>
      </form>
    );
  };
}

/** The items of one assignment. */
function AssignmentItems(handle: Handle<{ a: IntegrationAssignment; projects: Project[]; openTask: OpenTask }>) {
  const s = services(handle);
  const list = query(handle, {
    load: () => s.client.integrationItems({ assignmentId: handle.props.a.id, limit: 50 }),
    key: () => [handle.props.a.id, handle.props.a.sync.itemsSeen],
    when: (e) => e.type.startsWith('integration.'),
  });
  return () => {
    const { projects, openTask } = handle.props;
    const rows: ExternalItem[] = list.data ?? [];
    return (
      <section aria-label="Items" mix={panel}>
        <ErrorNote error={list.error} />
        {list.loading && !list.data ? <Loading /> : null}
        {list.data && rows.length === 0 ? <p mix={muted}>No items yet. Run a sync.</p> : null}
        {rows.length > 0 ? (
          <ul mix={items}>
            {rows.map((it) => (
              <ItemRow key={it.id} it={it} projects={projects} onChanged={list.reload} openTask={openTask} />
            ))}
          </ul>
        ) : null}
      </section>
    );
  };
}

/** One project a connection is assigned to: its scope, sync state, items, rules and outbound writes. */
function AssignmentRow(handle: Handle<{ conn: IntegrationView; info?: IntegrationKindInfo; a: IntegrationAssignment; projects: Project[]; onChanged: () => void; openTask: OpenTask }>) {
  const s = services(handle);
  const act = action(handle);
  let show: 'items' | 'rules' | 'outbound' | 'settings' | '' = '';
  let confirm = false;
  let sync: string | undefined;
  const toggle = (v: typeof show) => {
    show = show === v ? '' : v;
    void handle.update();
  };
  return () => {
    const { conn, info, a, projects, onChanged, openTask } = handle.props;
    const proj = projects.find((p) => p.id === a.projectId);
    const name = proj?.name ?? a.projectId;
    const sy = a.sync;
    return (
      <div mix={rowCard} data-testid="assignment" data-project={a.projectId}>
        <div mix={head}>
          <strong>{name}</strong>
          <Badge tone={syncTone(sy.state)}>sync: {sy.state}</Badge>
          <span mix={faint}>
            {scopeSummary(info, a)} · {sy.itemsSeen} {info?.items ?? 'items'} · last sync {sy.lastSuccessAt ? formatDateTime(sy.lastSuccessAt) : 'never'}
          </span>
        </div>
        {sy.lastError ? <p mix={errLine}>{sy.lastError}</p> : null}
        <ErrorNote error={act.error} />
        {sync ? (
          <Notice tone="info" role="status">
            {sync}
          </Notice>
        ) : null}
        <div mix={toolbar}>
          <Button
            size="sm"
            icon={RefreshCw}
            disabled={act.busy !== ''}
            mix={[
              on('click', async () => {
                const r = await act.run('sync', () => s.client.integrationSyncAssignment(a.id, false));
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
            {(conn.tools.ops ?? []).length > 0 ? (
              <button type="button" aria-expanded={show === 'outbound'} mix={on('click', () => toggle('outbound'))}>
                Outbound
              </button>
            ) : null}
          </span>
          <span mix={push}>
            <Button size="sm" variant="ghost" icon={Pencil} mix={[on('click', () => toggle('settings'))]}>
              Settings
            </Button>
            {confirm ? (
              <>
                <Button
                  size="sm"
                  variant="danger"
                  mix={[
                    on('click', async () => {
                      await act.run('unassign', () => s.client.integrationUnassign(a.id));
                      onChanged();
                    }),
                  ]}
                >
                  Yes, unassign from {name}
                </Button>
                <Button size="sm" mix={[on('click', () => ((confirm = false), void handle.update()))]}>
                  Keep
                </Button>
              </>
            ) : (
              <Button size="sm" variant="ghost" icon={Unplug} mix={[on('click', () => ((confirm = true), void handle.update()))]}>
                Unassign
              </Button>
            )}
          </span>
        </div>
        {confirm ? <p mix={faint}>Unassigning removes this project's items that never became tasks, its rules and its cursor. Tasks stay. The connection stays.</p> : null}
        {show === 'settings' ? (
          <AssignForm conn={conn} info={info} projects={projects} existing={a} onDone={() => ((show = ''), onChanged())} onCancel={() => toggle('settings')} />
        ) : null}
        {show === 'items' ? <AssignmentItems a={a} projects={projects} openTask={openTask} /> : null}
        {show === 'rules' ? <Rules conn={conn} projectId={a.projectId} /> : null}
        {show === 'outbound' ? <Outbound conn={conn} projectId={a.projectId} /> : null}
      </div>
    );
  };
}

/** One service, opened from its tile: its fixed name and logo, what the connection is, and the projects it is assigned to. */
function ServicePanel(handle: Handle<{ kind: ServiceKind; info?: IntegrationKindInfo; conn?: IntegrationView; projects: Project[]; onChanged: () => void; openTask: OpenTask }>) {
  const s = services(handle);
  const act = action(handle);
  let report: IntegrationTestReport | undefined;
  let reconnect = false;
  let assigning = false;
  let confirm = false;
  return () => {
    const { kind, info, conn, projects, onChanged, openTask } = handle.props;
    const label = info?.label ?? kind.label;
    const id = conn?.identity ?? {};
    const assignments = conn?.assignments ?? [];
    return (
      <article mix={[card, cardCss]} data-testid="integration" data-kind={kind.kind} data-name={label} aria-labelledby={`svc-${handle.id}`}>
        <header mix={serviceHead}>
          <BrandLogo id={kind.kind} size={40} label />
          <h2 id={`svc-${handle.id}`} data-testid="integration-name">
            {label}
          </h2>
          {conn ? <Badge tone={statusTone(conn.status)}>{conn.status}</Badge> : <Badge tone="muted">not connected</Badge>}
          {conn ? <Badge tone={syncTone(conn.sync.state)}>sync: {conn.sync.state}</Badge> : null}
        </header>
        <p mix={muted}>
          {info?.blurb ?? kind.blurb}. Drydock reads {info?.items ?? 'items'} through {info?.api ?? 'the service API'} and imports them into the projects you assign.
        </p>
        <p mix={faint}>
          <a href={routes.mcp.href()} data-testid="mcp-link">
            Also give agents {label}'s tools: MCP
          </a>
        </p>
        {!conn ? (
          <ConnectForm kind={kind.kind} info={info} onDone={(r) => ((report = r.test), onChanged())} />
        ) : (
          <>
            {conn.statusDetail ? <p mix={muted}>{conn.statusDetail}</p> : null}
            <dl mix={[stats, identityGrid]} data-testid="connection-identity">
              <div>
                <dt>Account</dt>
                <dd>{id.account || (conn.checkedAt ? 'not reported by the API' : 'not checked yet')}</dd>
              </div>
              <div>
                <dt>Workspace</dt>
                <dd>
                  {id.workspaceUrl && safeHref(id.workspaceUrl) ? (
                    <a href={safeHref(id.workspaceUrl)} target="_blank" rel="noopener noreferrer" data-rmx-document>
                      {id.workspace || id.workspaceUrl}
                    </a>
                  ) : (
                    id.workspace || (conn.checkedAt ? 'not reported by the API' : 'not checked yet')
                  )}
                </dd>
              </div>
              <div>
                <dt>Connected with</dt>
                <dd>{id.method || conn.authKind}</dd>
              </div>
              <div>
                <dt>Scopes</dt>
                <dd>{(id.scopes ?? []).length > 0 ? (id.scopes ?? []).join(', ') : 'not reported by the API'}</dd>
              </div>
              <div>
                <dt>Last checked</dt>
                <dd>{conn.checkedAt ? formatDateTime(conn.checkedAt) : 'never'}</dd>
              </div>
              <div>
                <dt>Credential</dt>
                <dd>{conn.kind === 'github' ? 'your gh sign-in (Drydock stores no GitHub token)' : conn.hasCredential ? 'stored in the credential store (never shown)' : 'none'}</dd>
              </div>
            </dl>
            <ErrorNote error={act.error} />
            {report ? <ReportView r={report} /> : null}
            <div mix={toolbar}>
              <Button
                size="sm"
                icon={Stethoscope}
                disabled={act.busy !== ''}
                mix={[
                  on('click', async () => {
                    const r = await act.run('test', () => s.client.integrationTest(conn.id));
                    if (r) report = r;
                    onChanged();
                  }),
                ]}
              >
                Test
              </Button>
              <Button size="sm" icon={KeyRound} aria-expanded={reconnect ? 'true' : 'false'} mix={[on('click', () => ((reconnect = !reconnect), void handle.update()))]}>
                Reconnect
              </Button>
              <Button size="sm" icon={FolderPlus} aria-expanded={assigning ? 'true' : 'false'} mix={[on('click', () => ((assigning = !assigning), void handle.update()))]}>
                Assign to project
              </Button>
              <span mix={push}>
                {confirm ? (
                  <>
                    <Button
                      size="sm"
                      variant="danger"
                      mix={[
                        on('click', async () => {
                          await act.run('disconnect', () => s.client.integrationDisconnect(conn.id));
                          report = undefined;
                          confirm = false;
                          onChanged();
                        }),
                      ]}
                    >
                      Yes, disconnect {label}
                    </Button>
                    <Button size="sm" mix={[on('click', () => ((confirm = false), void handle.update()))]}>
                      Keep
                    </Button>
                  </>
                ) : (
                  <Button size="sm" variant="ghost" icon={Trash2} mix={[on('click', () => ((confirm = true), void handle.update()))]}>
                    Disconnect
                  </Button>
                )}
              </span>
            </div>
            {confirm ? <p mix={faint}>Disconnecting removes the credential, every project assignment and the items that never became tasks.</p> : null}
            {reconnect ? <ConnectForm kind={kind.kind} info={info} reconnectId={conn.id} onDone={(r) => ((report = r.test), (reconnect = false), onChanged())} /> : null}
            <section aria-label="Projects" mix={panel}>
              <h4>Projects</h4>
              {(conn.legacyItems ?? 0) > 0 ? (
                <Notice tone="info">
                  {conn.legacyItems} item(s) were stored before connections and projects were separated; the first project you assign adopts them and the stored settings.
                </Notice>
              ) : null}
              {assignments.length === 0 && !assigning ? <p mix={muted}>Not assigned to a project yet. Assign it to import {info?.items ?? 'items'}.</p> : null}
              {assigning ? <AssignForm conn={conn} info={info} projects={projects} onDone={() => ((assigning = false), onChanged())} onCancel={() => ((assigning = false), void handle.update())} /> : null}
              {assignments.map((a) => (
                <AssignmentRow key={a.id} conn={conn} info={info} a={a} projects={projects} onChanged={onChanged} openTask={openTask} />
              ))}
            </section>
          </>
        )}
        {info ? (
          <details mix={detailsCss} data-testid="api-info">
            <summary>About the {label} API</summary>
            <div mix={stack}>
              <p mix={faint}>
                {info.api}, {info.apiVersion}. {info.sdk}
              </p>
              <p mix={faint}>Rate limit: {info.rateLimit}.</p>
              {(conn?.tools.syncTools ?? []).length > 0 ? <p mix={[faint, mono]}>{(conn?.tools.syncTools ?? []).join(' · ')}</p> : null}
              <ul mix={linkList}>
                {(info.docs ?? []).map((d) => (
                  <li key={d.url}>
                    <a href={d.url} target="_blank" rel="noopener noreferrer" data-rmx-document>
                      {d.title}
                    </a>
                  </li>
                ))}
              </ul>
            </div>
          </details>
        ) : null}
        {report && !conn ? <ReportView r={report} /> : null}
      </article>
    );
  };
}

export function IntegrationsScreen(handle: Handle<{ openTask?: OpenTask; initial?: string }>) {
  const s = services(handle);
  watch(handle, s.workspace);
  const q = query(handle, { load: () => s.client.integrationList(), when: (e) => e.type.startsWith('integration.') });
  const kinds = query(handle, { load: () => s.client.integrationKinds() });
  let selected = handle.props.initial ?? '';
  return () => {
    const projects = s.workspace.projectList;
    const list: IntegrationView[] = q.data ?? [];
    const openTask = handle.props.openTask ?? defaultOpen;
    const kind = KINDS.find((k) => k.kind === selected);
    const info = (kinds.data ?? []).find((k) => k.kind === selected);
    return (
      <Page title="Integrations" icon={Blocks} subtitle={q.data ? `${list.length} connected` : undefined}>
        <p mix={introCss}>
          Connect a service once for the whole workspace, then assign it to the projects that should import its items. Everything a service returns is outside text: it is
          redacted, scored for injection risk, shown inert and quoted as data when you import it. Writing back needs your approval of the exact text. Giving agents these services'
          tools is on the{' '}
          <a href={routes.mcp.href()}>
            <Icon icon={Server} size={12} /> MCP screen
          </a>
          .
        </p>
        <ErrorNote error={q.error ?? kinds.error} what="Integrations" />
        {q.loading && !q.data ? <Loading /> : null}
        {q.data && list.length === 0 ? (
          <p mix={emptyLine}>
            <Icon icon={ShieldAlert} size={14} />
            <strong>Nothing is connected.</strong> Choose a service below. Its items arrive as untrusted text you can read, score and import.
          </p>
        ) : null}
        <ul mix={tiles} aria-label="Services">
          {KINDS.map((k) => (
            <ServiceTile key={k.kind} kind={k} conn={tileState(list, k.kind)} selected={selected === k.kind} onSelect={() => ((selected = selected === k.kind ? '' : k.kind), void handle.update())} />
          ))}
        </ul>
        {kind ? <ServicePanel key={kind.kind} kind={kind} info={info} conn={tileState(list, kind.kind)} projects={projects} onChanged={q.reload} openTask={openTask} /> : null}
      </Page>
    );
  };
}
