// One project's side of an app-sync connection (decision P42): the assignment of a connection to the project (its scope, read
// from the service on request), its sync state, its items (outside text: redacted, scored for injection risk, shown inert and
// quoted as data when imported as a task), its rules (they start disabled and in dry-run) and its outbound writes (nothing is
// sent before the human approves the exact text). Shown on the project's Integrations page (project-integrations-screen.tsx);
// connecting a service once for the workspace stays on the workspace's Integrations page (integrations-screen.tsx). Decision P114
// adds, per assignment, its mappings and auto-approve classes, and per item its attachments, typed properties and the status the
// service reports for a linked task (project-extras.tsx); a rule may link items to an existing task; a failed write can be retried.

import { css, on, type Handle } from 'remix/component';
import { BadgeCheck, Check, ExternalLink, FileText, FolderPlus, ListChecks, Pencil, Plus, RefreshCw, Send, Trash2, Unplug, Workflow } from 'lucide';
import type { ExternalItem, ExternalItemDetail, IntegrationAssignment, IntegrationChoice, IntegrationKindInfo, IntegrationRule, IntegrationView, NotionPageText, Outbox } from '../../api/extTypes.ts';
import { formatDateTime, plural } from '../../lib/format.ts';
import { visible } from '../../lib/visible.ts';
import { routes } from '../../routes.ts';
import { action, query, services, watch } from '../../state/services.tsx';
import { Button, buttonStyle } from '../../ui/button.tsx';
import { Badge, faint, mono, muted } from '../../ui/data.tsx';
import { ErrorNote, Loading, Notice } from '../../ui/feedback.tsx';
import { Field, formActions, formGrid } from '../../ui/form.tsx';
import { Select } from '../../ui/select.tsx';
import { Icon } from '../../ui/icon.tsx';
import { stack } from '../../ui/layout.tsx';
import { Markdown, safeHref } from '../../ui/markdown.tsx';
import { t } from '../../ui/tokens.ts';
import { ShownNotes } from '../shared/approval-text.tsx';
import { ConfirmAction } from '../shared/confirm-action.tsx';
import { kindLabel, outboxTone, parseParams, riskText, riskTone, scopeSummary, syncText, syncTone } from './integrations.ts';
import { AttachmentsButton, AutoApprove, ItemAttachments, Mappings, PropsTable, StatusSuggestionNote } from './project-extras.tsx';

/** Opens a task (the page passes navigate; tests record the address). */
export type OpenTask = (href: string) => void;

export const head = css({
  display: 'flex',
  alignItems: 'center',
  gap: t.s2,
  flexWrap: 'wrap',
  minWidth: '0',
  '& h3': { fontSize: t.textLg, fontWeight: '600', margin: '0' },
});
export const toolbar = css({ display: 'flex', alignItems: 'center', gap: t.s2, flexWrap: 'wrap' });
const segmented = css({
  display: 'inline-flex',
  padding: '2px',
  gap: '2px',
  borderRadius: t.rMd,
  background: t.bgActive,
  '& > button': { height: '28px', padding: '0 10px', border: '0', borderRadius: t.rSm, background: 'transparent', color: t.textMuted, fontSize: t.textSm, fontWeight: '500' },
  '& > button:hover': { color: t.text },
  '& > button[aria-expanded="true"]': { background: t.bgRaised, color: t.text, boxShadow: t.shadowSm },
});
export const push = css({ marginLeft: 'auto', display: 'flex', gap: t.s2, alignItems: 'center', flexWrap: 'wrap' });
export const panel = css({ display: 'grid', gap: t.s3, paddingTop: t.s3, borderTop: `1px solid ${t.border}`, '& h4': { fontSize: t.textMd, fontWeight: '600', margin: '0' } });
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
export const full = css({ gridColumn: '1 / -1' });

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

/** One imported item of the project: inert outside text with its risk score; Import as a task makes a backlog task of this project. */
function ItemRow(handle: Handle<{ it: ExternalItem; projectId: string; onChanged: () => void; openTask: OpenTask }>) {
  const s = services(handle);
  const act = action(handle);
  let detail: ExternalItemDetail | undefined;
  let page: NotionPageText | undefined;
  let attachments = false;
  return () => {
    const { it, projectId, onChanged, openTask } = handle.props;
    const chosen = it.projectId || projectId;
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
        <PropsTable item={it} />
        <StatusSuggestionNote item={it} onChanged={onChanged} />
        {it.redactions > 0 ? <p mix={faint}>{plural(it.redactions, 'secret')} {it.redactions === 1 ? 'was' : 'were'} redacted from this text before it was stored.</p> : null}
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
          {it.source !== 'aikido' && it.source !== 'github' ? <AttachmentsButton open={attachments} onToggle={() => ((attachments = !attachments), void handle.update())} /> : null}
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
              <Button
                size="sm"
                icon={Plus}
                disabled={act.busy !== '' || it.risk >= 3 || !chosen}
                title={it.risk >= 3 ? 'High injection risk: import is blocked' : 'Create a backlog task in this project. The text is quoted as data.'}
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
            ) : null}
          </span>
        </div>
        {detail ? <QuotedBlock text={detail.quoted || detail.body} /> : null}
        {attachments ? <ItemAttachments item={it} /> : null}
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
                <Select id={id} label="Operation" value={curOp} options={ops.map((o) => ({ value: o.op, label: o.label }))} onChange={(v) => ((op = v), void handle.update())} />
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
              <strong>{visible(o.op)}</strong> on <code>{visible(o.target)}</code>
              <span mix={faint}>
                proposed by {o.createdBy}; approval <code data-testid="approval-id">{o.approvalId}</code>; hash {o.payloadHash.slice(0, 10)}
              </span>
            </div>
            <p mix={faint}>Exactly what would be sent:</p>
            <ShownNotes texts={[o.preview, o.target, o.op]} testId="outbox" />
            <pre mix={preCss} tabIndex={0}>
              {visible(o.preview)}
            </pre>
            {o.error ? <p mix={errLine}>{o.error}</p> : null}
            {o.receipt ? <p mix={faint}>Receipt: {o.receipt}</p> : null}
            {o.autoClass ? (
              <p mix={faint} data-testid="outbox-auto">
                Approved automatically under the auto-approve class {o.autoClass}, in the name of {o.decidedBy}.
              </p>
            ) : null}
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
              {o.status === 'failed' ? (
                <Button
                  size="sm"
                  icon={RefreshCw}
                  disabled={act.busy !== '' || ro.readOnly}
                  title={o.op === 'comment' ? 'Looks for the comment on the service first; posts it again only after a new approval' : 'Proposes the same write again; it needs a new approval'}
                  mix={[
                    on('click', async () => {
                      await act.run('retry', () => s.client.integrationOutboundRetry(o.id));
                      q.reload();
                    }),
                  ]}
                >
                  Retry
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
  let ruleAction = '';
  let linkTask = '';
  const change = async (r: IntegrationRule, patch: Partial<{ enabled: boolean; dryRun: boolean; delete: boolean }>) => {
    await act.run('rule', () =>
      s.client.integrationRulesSet({ id: r.id, version: r.version, integrationId: r.integrationId, projectId: r.projectId, name: r.name, when: r.when, action: r.action, ...patch }),
    );
    q.reload();
  };

  const actionOf = () => ruleAction || (handle.props.conn.kind === 'notion' ? 'link_task' : 'create_task');
  return () => {
    const { conn, projectId } = handle.props;
    const rules: IntegrationRule[] = (q.data ?? []).filter((r) => r.projectId === projectId);
    return (
      <section aria-label="Rules" mix={panel}>
        <h4>Rules (create or link tasks automatically)</h4>
        <p mix={faint}>
          A rule creates backlog tasks from matching items, or links them to a task that already exists. Only you can create, change or enable a rule. A new rule starts disabled and in dry-run: it lists what it would create and
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
            {(r.matches ?? []).length > 0 ? <p mix={faint}>{plural((r.matches ?? []).length, 'match', 'matches')} recorded (dry-run listings included).</p> : null}
            <div mix={toolbar}>
              <Button size="sm" mix={[on('click', () => void change(r, { enabled: !r.enabled }))]}>
                {r.enabled ? 'Disable' : 'Enable'}
              </Button>
              <Button size="sm" mix={[on('click', () => void change(r, { dryRun: !r.dryRun }))]}>
                {r.dryRun ? 'Switch to live' : 'Switch to dry-run'}
              </Button>
              <ConfirmAction
                label="Delete"
                icon={Trash2}
                yes="Yes, delete"
                yesName={`Yes, delete the rule "${r.name || r.id}"`}
                keepName={`Keep the rule "${r.name || r.id}"`}
                question={`Delete the rule "${r.name || r.id}"? It stops creating tasks and cannot be brought back.`}
                disabled={act.busy !== ''}
                testId="int-rule-delete"
                onConfirm={() => change(r, { delete: true })}
              />
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
          <Field label="Action">
            {(id) => (
              <Select
                id={id}
                label="Action"
                value={actionOf()}
                options={[
                  ...(conn.kind === 'notion' ? [] : [{ value: 'create_task', label: 'Create a backlog task' }]),
                  { value: 'link_task', label: 'Link to an existing task' },
                  { value: 'notify', label: 'Only record the match' },
                ]}
                onChange={(v) => ((ruleAction = v), void handle.update())}
              />
            )}
          </Field>
          {actionOf() === 'link_task' ? (
            <Field label="Task id (empty: the task with the item's reference)">
              {(id) => <input id={id} value={linkTask} spellcheck={false} mix={[mono, on('input', (e) => ((linkTask = e.currentTarget.value), void handle.update()))]} />}
            </Field>
          ) : null}
          <div mix={formActions}>
            <Button
              icon={Workflow}
              disabled={act.busy !== '' || !name.trim() || !when.trim()}
              mix={[
                on('click', async () => {
                  const v = maxPerDay.trim();
                  const n = v === '' ? undefined : Number(v);
                  const r = await act.run('create', () =>
                    s.client.integrationRulesSet({
                      integrationId: conn.id,
                      projectId,
                      name: name.trim(),
                      when,
                      action: actionOf(),
                      params: actionOf() === 'link_task' && linkTask.trim() ? { task: linkTask.trim() } : undefined,
                      maxPerDay: n !== undefined && Number.isFinite(n) ? n : undefined,
                    }),
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

/**
 * Assign a connection to this project, or change the assignment's settings: what part of the service the project imports.
 * Choices are read from the service on request.
 */
export function AssignForm(
  handle: Handle<{ conn: IntegrationView; info?: IntegrationKindInfo; projectId: string; projectName: string; existing?: IntegrationAssignment; onDone: () => void; onCancel: () => void }>,
) {
  const s = services(handle);
  const act = action(handle);
  let cfg: Record<string, string> = { ...(handle.props.existing?.config ?? {}) };
  const choices: Record<string, IntegrationChoice[]> = {};
  return () => {
    const { conn, info, projectId, projectName, existing, onDone, onCancel } = handle.props;
    const label = info?.label ?? kindLabel(conn.kind);
    const proj = existing?.projectId ?? projectId;
    const settings = info?.settings ?? [];
    return (
      <form
        mix={[formGrid, on<HTMLFormElement, 'submit'>('submit', (e) => e.preventDefault())]}
        aria-label={existing ? `Settings of ${label} for this project` : `Assign ${label} to ${projectName}`}
        data-testid="assign-form"
      >
        <div mix={full}>
          <ErrorNote error={act.error} what="Not assigned" />
        </div>
        {settings.length === 0 ? <p mix={[full, muted]}>{label} needs no settings here: the project imports what the connection reaches.</p> : null}
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
function AssignmentItems(handle: Handle<{ a: IntegrationAssignment; openTask: OpenTask }>) {
  const s = services(handle);
  const list = query(handle, {
    load: () => s.client.integrationItems({ assignmentId: handle.props.a.id, limit: 50 }),
    key: () => [handle.props.a.id, handle.props.a.sync.itemsSeen],
    when: (e) => e.type.startsWith('integration.'),
  });
  return () => {
    const { a, openTask } = handle.props;
    const rows: ExternalItem[] = list.data ?? [];
    return (
      <section aria-label="Items" mix={panel}>
        <ErrorNote error={list.error} />
        {list.loading && !list.data ? <Loading /> : null}
        {list.data && rows.length === 0 ? <p mix={muted}>No items yet. Run a sync.</p> : null}
        {rows.length > 0 ? (
          <ul mix={items}>
            {rows.map((it) => (
              <ItemRow key={it.id} it={it} projectId={a.projectId} onChanged={list.reload} openTask={openTask} />
            ))}
          </ul>
        ) : null}
      </section>
    );
  };
}

/** The project's assignment of a connection: its scope, sync state, items, rules and outbound writes. */
export function AssignmentRow(
  handle: Handle<{ conn: IntegrationView; info?: IntegrationKindInfo; a: IntegrationAssignment; projectName: string; onChanged: () => void; openTask: OpenTask }>,
) {
  const s = services(handle);
  const act = action(handle);
  let show: 'items' | 'rules' | 'outbound' | 'settings' | 'mappings' | 'auto' | '' = '';
  let confirm = false;
  let sync: string | undefined;
  const toggle = (v: typeof show) => {
    show = show === v ? '' : v;
    void handle.update();
  };
  return () => {
    const { conn, info, a, projectName, onChanged, openTask } = handle.props;
    const name = projectName || a.projectId;
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
          <Button
            size="sm"
            variant="ghost"
            disabled={act.busy !== ''}
            title="Reads the whole scope again and marks what is gone upstream (the weekly sweep does this by itself)"
            mix={[
              on('click', async () => {
                const r = await act.run('full', () => s.client.integrationSyncAssignment(a.id, true));
                if (r) sync = syncText(r);
                onChanged();
              }),
            ]}
          >
            Full sync
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
            {conn.kind === 'linear' || conn.kind === 'notion' || conn.kind === 'sentry' ? (
              <>
                <button type="button" aria-expanded={show === 'mappings'} mix={on('click', () => toggle('mappings'))}>
                  Mappings
                </button>
                <button type="button" aria-expanded={show === 'auto'} mix={on('click', () => toggle('auto'))}>
                  Auto-approve
                </button>
              </>
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
          <AssignForm conn={conn} info={info} projectId={a.projectId} projectName={name} existing={a} onDone={() => ((show = ''), onChanged())} onCancel={() => toggle('settings')} />
        ) : null}
        {show === 'items' ? <AssignmentItems a={a} openTask={openTask} /> : null}
        {show === 'rules' ? <Rules conn={conn} projectId={a.projectId} /> : null}
        {show === 'outbound' ? <Outbound conn={conn} projectId={a.projectId} /> : null}
        {show === 'mappings' ? <Mappings conn={conn} a={a} /> : null}
        {show === 'auto' ? <AutoApprove conn={conn} projectId={a.projectId} /> : null}
      </div>
    );
  };
}
