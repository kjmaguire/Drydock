// Integrations (decisions P42, P43 and P47): one page for everything Drydock connects to outside itself, a tile per vendor.
// A vendor's tile opens its panel with up to three sections. App sync is Drydock's own connection to Linear, Notion, Sentry,
// Aikido or GitHub (P42): connected once for the whole workspace, assigned to projects in a separate step that says which part
// of the service each project imports; the name and logo are fixed, what a connection reaches (account, workspace, scopes) is
// read from the service's API, never typed, and everything a service returns is outside text: redacted, scored for injection
// risk, shown inert and quoted as data when imported; writing back needs the human's approval of the exact text, and rules that
// create tasks start disabled and in dry-run. Agent tools (MCP) are the vendor's connector cards (P43, screens/mcp/gallery.tsx):
// connected once, put into projects and granted per agent. Skills from this vendor imports the vendor's skill registries into
// the current project. Below the tiles: your own MCP servers and, with a project, the calls waiting for approval, the project's
// servers and Add from the library (screens/mcp/mcp-screen.tsx). /mcp is an alias of this page.

import { css, navigate, on, ref, type Handle } from 'remix/component';
import {
  BadgeCheck,
  Blocks,
  Check,
  CirclePlus,
  Cpu,
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
  Wrench,
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
  SkillLibraryAddAll,
  SkillRegistry,
} from '../../api/extTypes.ts';
import type { McpGalleryCard } from '../../api/mcpClient.ts';
import type { Agent, Project } from '../../api/types.ts';
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
import { emptyLine, Tile, tileGrid } from '../../ui/tiles.tsx';
import { card, Page, Section, stack } from '../../ui/layout.tsx';
import { Markdown, safeHref } from '../../ui/markdown.tsx';
import { t, tone } from '../../ui/tokens.ts';
import { ConnectorCard, connectorGrid, OwnServers } from '../mcp/gallery.tsx';
import { AddFromLibrary, Approvals, ServerCard } from '../mcp/mcp-screen.tsx';
import { addAllText } from '../skills/skills.ts';
import { KINDS, kindLabel, outboxTone, parseParams, riskText, riskTone, scopeSummary, statusTone, syncText, syncTone, tileState, vendors, vendorStateText, vendorTone, type ServiceKind, type Vendor } from './integrations.ts';

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
const identityGrid = css({ gridTemplateColumns: 'repeat(3, minmax(0, 1fr))' });
const codeCss = css({ fontFamily: t.mono, fontSize: t.textLg, fontWeight: '600', letterSpacing: '0.08em' });
const panelStack = css({ display: 'grid', gap: t.s5, minWidth: '0' });
const regList = css({
  display: 'grid',
  gap: t.s2,
  margin: '0',
  padding: '0',
  listStyle: 'none',
  '& > li': { display: 'grid', gap: t.s2, padding: t.s3, border: `1px solid ${t.border}`, borderRadius: t.rMd, background: t.bgSurface },
  '& p': { margin: '0' },
});

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

/** One vendor on the overview: its fixed logo and name, what it offers, and the state of each channel it has. */
function VendorTile(handle: Handle<{ v: Vendor; conn?: IntegrationView; selected: boolean; onSelect: () => void }>) {
  return () => {
    const { v, conn, selected, onSelect } = handle.props;
    const data: Record<string, string> = { kind: v.key };
    if (v.kind) data.connected = conn ? 'true' : 'false';
    else data.mcp = 'true';
    return <Tile title={v.label} logo={v.logo} icon={Server} blurb={v.blurb} state={vendorStateText(v, conn)} tone={vendorTone(v, conn)} selected={selected} onSelect={onSelect} testId="integration-tile" data={data} />;
  };
}

/**
 * The skill registries a vendor publishes, imported whole into the current project (skill.importAll; the skills are offered,
 * not accepted). A registry marked selective_only is picked from one skill at a time on the Skills page.
 */
function VendorSkills(handle: Handle<{ v: Vendor; registries: SkillRegistry[] | undefined; loading: boolean; error?: Error; project?: Project }>) {
  const s = services(handle);
  const act = action(handle);
  let results: Record<string, SkillLibraryAddAll> = {};
  return () => {
    const { v, registries, loading, error, project } = handle.props;
    const skillsLink = (
      <a href={routes.skills.href()} data-testid="skills-link">
        Skills page
      </a>
    );
    return (
      <Section
        label="Skills from this vendor"
        testId="integration-skills"
        title="Skills from this vendor"
        description={`Skill registries ${v.label} publishes. The Library holds every skill of each registry; adding them to a project accepts them, and the Skills page is where you narrow or remove them.`}
      >
        <ErrorNote error={error ?? act.error} what="Skills" />
        {loading && !registries ? <Loading label="Loading the registries..." /> : null}
        {!project ? <p mix={muted}>Choose a project to add this vendor's skills to.</p> : null}
        <ul mix={regList} aria-label={`Skill registries of ${v.label}`}>
          {v.skillRegistries.map((id) => {
            const r = (registries ?? []).find((x) => x.id === id);
            const result = results[id];
            const selective = r?.importMode === 'selective_only';
            return (
              <li key={id} data-testid="vendor-registry" data-registry={id}>
                <div mix={head}>
                  <Icon icon={Cpu} size={14} />
                  <strong>{r?.repo ?? id}</strong>
                  {r ? (
                    <span mix={faint}>
                      {r.license ? `license ${r.license}` : ''}
                      {r.enabled ? '' : ' · disabled'}
                    </span>
                  ) : registries ? (
                    <span mix={faint}>not in this workspace's registry list</span>
                  ) : null}
                </div>
                {r && project ? (
                  <div mix={toolbar}>
                    <Button
                      size="sm"
                      icon={Plus}
                      disabled={selective || !r.enabled || act.busy !== ''}
                      data-testid="vendor-add-all"
                      title={selective ? 'This registry is added one skill at a time' : undefined}
                      mix={[
                        on('click', async () => {
                          const res = await act.run(`add-${id}`, () => s.client.skillLibraryAddAll(project.id, id));
                          if (res) {
                            results = { ...results, [id]: res };
                            void handle.update();
                          }
                        }),
                      ]}
                    >
                      {act.busy === `add-${id}` ? 'Adding...' : `Add all to ${project.name}`}
                    </Button>
                    {selective ? (
                      <span mix={muted} data-testid="vendor-selective">
                        {r.notes ? `${r.notes}: ` : ''}pick its skills one at a time on the {skillsLink}.
                      </span>
                    ) : null}
                  </div>
                ) : null}
                {result ? (
                  <p mix={muted} role="status" data-testid="vendor-add-result">
                    {addAllText(result)}
                  </p>
                ) : null}
              </li>
            );
          })}
        </ul>
        <p mix={faint}>Every registry, with its skills one by one and who gets each skill, is on the {skillsLink}.</p>
      </Section>
    );
  };
}

/** The panel of the selected vendor: app sync (an integration kind), agent tools (its connector cards) and its skill registries. */
function VendorPanel(
  handle: Handle<{
    v: Vendor;
    info?: IntegrationKindInfo;
    conn?: IntegrationView;
    projects: Project[];
    project?: Project;
    agents: readonly Agent[];
    registries: SkillRegistry[] | undefined;
    registriesLoading: boolean;
    registriesError?: Error;
    onSyncChanged: () => void;
    onAgentsChanged: () => void;
    openTask: OpenTask;
  }>,
) {
  return () => {
    const { v, info, conn, projects, project, agents, registries, registriesLoading, registriesError, onSyncChanged, onAgentsChanged, openTask } = handle.props;
    return (
      <div mix={panelStack} data-testid="integration-panel" data-kind={v.key}>
        {v.kind ? (
          <Section
            label="App sync"
            testId="integration-sync"
            title="App sync"
            description={`Drydock itself reads ${info?.items ?? 'items'} from ${v.label} and imports them into the projects you assign, as untrusted text.`}
          >
            <ServicePanel kind={v.kind} info={info} conn={conn} projects={projects} onChanged={onSyncChanged} openTask={openTask} />
          </Section>
        ) : null}
        {v.cards.length > 0 ? (
          <Section
            label="Agent tools (MCP)"
            testId="integration-agents"
            title="Agent tools (MCP)"
            description={`${v.label}'s MCP server${v.cards.length === 1 ? '' : 's'}, checked against the vendor's own documentation. Connect once; then add it to projects and grant it to agents. Every tool not marked read-only waits for your approval, call by call.`}
          >
            <ul mix={connectorGrid}>
              {v.cards.map((c) => (
                <ConnectorCard key={c.id} card={c} projects={projects} projectId={project?.id} agents={agents} onChanged={onAgentsChanged} />
              ))}
            </ul>
          </Section>
        ) : null}
        {v.skillRegistries.length > 0 ? <VendorSkills v={v} registries={registries} loading={registriesLoading} error={registriesError} project={project} /> : null}
      </div>
    );
  };
}

export function IntegrationsScreen(handle: Handle<{ openTask?: OpenTask; initial?: string }>) {
  const s = services(handle);
  watch(handle, s.workspace);
  watch(handle, s.project);
  const q = query(handle, { load: () => s.client.integrationList(), when: (e) => e.type.startsWith('integration.') });
  const kinds = query(handle, { load: () => s.client.integrationKinds() });
  const gallery = query(handle, { load: () => s.client.mcpGallery(), when: (e) => e.type.startsWith('mcp.server') });
  // the registries are read once, and only when a vendor on the page publishes some
  const regs = query(handle, { load: () => s.client.skillRegistries(), enabled: () => (gallery.data ?? []).some((c) => (c.skillRegistries ?? []).length > 0) });
  const servers = query(handle, {
    load: () => s.client.mcpServerList(s.workspace.projectId!),
    key: () => s.workspace.projectId,
    enabled: () => !!s.workspace.projectId,
    when: (e) => e.type.startsWith('mcp.server') || e.type === 'mcp.access_changed' || e.type.startsWith('agent.'),
  });
  let selected = handle.props.initial ?? '';
  const agentsChanged = () => {
    gallery.reload();
    if (s.workspace.projectId) servers.reload();
  };
  return () => {
    const projects = s.workspace.projectList;
    const project = s.workspace.project;
    const projectId = s.workspace.projectId;
    const agents = projectId ? s.project.agentList : [];
    const list: IntegrationView[] = q.data ?? [];
    const cards: McpGalleryCard[] = gallery.data ?? [];
    const openTask = handle.props.openTask ?? defaultOpen;
    const all = vendors(KINDS, cards);
    const current = all.find((v) => v.key === selected);
    const info = (kinds.data ?? []).find((k) => k.kind === selected);
    const connected = list.length + cards.filter((c) => c.status === 'connected').length;
    const loaded = !!q.data && !!gallery.data;
    const inProject = servers.data ?? [];
    return (
      <Page
        title="Integrations"
        icon={Blocks}
        subtitle={loaded ? `${connected} connected` : undefined}
        actions={
          <a href={routes.library.href()} mix={buttonStyle('ghost', 'sm')}>
            <Icon icon={Wrench} size={14} />
            Connect servers in the Library
          </a>
        }
      >
        <p mix={introCss}>
          Connect a service once for the whole workspace. App sync imports its items into the projects you assign: everything a service returns is outside text, redacted, scored
          for injection risk and quoted as data, and writing back needs your approval of the exact text. Agent tools reach agents through MCP: a server is connected once, put into
          projects and granted per agent, a server nobody was granted is never reached, and every tool you have not marked read-only waits for your approval, call by call. Secrets
          stay in the Windows credential store. Claude Code agents receive MCP servers; another agent CLI receives them only after you allow it under Agent CLIs in the{' '}
          <a href={routes.library.href()}>Library</a>.
        </p>
        <div data-testid="connector-gallery" mix={panelStack}>
          <ErrorNote error={q.error ?? kinds.error ?? gallery.error} what="Integrations" />
          {(q.loading && !q.data) || (gallery.loading && !gallery.data) ? <Loading /> : null}
          {loaded && connected === 0 ? (
            <p mix={emptyLine}>
              <Icon icon={ShieldAlert} size={14} />
              <strong>Nothing is connected.</strong> Choose a service below. Its items arrive as untrusted text you can read, score and import; its tools reach only the agents you grant.
            </p>
          ) : null}
          <ul mix={tileGrid} aria-label="Services">
            {all.map((v) => (
              <VendorTile
                key={v.key}
                v={v}
                conn={v.kind ? tileState(list, v.key) : undefined}
                selected={selected === v.key}
                onSelect={() => ((selected = selected === v.key ? '' : v.key), void handle.update())}
              />
            ))}
          </ul>
          {current ? (
            <VendorPanel
              key={current.key}
              v={current}
              info={info}
              conn={current.kind ? tileState(list, current.key) : undefined}
              projects={projects}
              project={project}
              agents={agents}
              registries={regs.data}
              registriesLoading={regs.loading}
              registriesError={regs.error}
              onSyncChanged={q.reload}
              onAgentsChanged={agentsChanged}
              openTask={openTask}
            />
          ) : null}
        </div>
        <OwnServers linked={cards.map((c) => c.libraryId)} onChanged={agentsChanged} />
        {projectId ? (
          <>
            <Approvals projectId={projectId} agents={agents} />
            <Section label="In this project" title="In this project" description={project ? `The connected servers ${project.name} has, and which of its agents may use each one.` : undefined}>
              <ErrorNote error={servers.error} what="Could not load the servers" />
              {servers.loading && !servers.data ? <Loading /> : null}
              {servers.data && inProject.length === 0 ? (
                <p mix={emptyLine} data-testid="no-project-servers">
                  <Icon icon={CirclePlus} size={14} />
                  <strong>No MCP servers in this project yet.</strong> Add a connected server to it from its card above or from the library below.
                </p>
              ) : null}
              <div mix={stack}>
                {inProject.map((srv) => (
                  <ServerCard key={srv.id} s={srv} agents={agents} onChanged={servers.reload} />
                ))}
              </div>
            </Section>
            <AddFromLibrary projectId={projectId} have={inProject.map((x) => x.id)} onAdded={servers.reload} />
          </>
        ) : s.workspace.projects.data ? (
          <p mix={muted} data-testid="no-project">
            MCP servers are put into a project and granted to its agents: connect them now, then <a href={routes.projectNew.href()}>create a project</a> or choose one to add them to.
          </p>
        ) : null}
      </Page>
    );
  };
}
