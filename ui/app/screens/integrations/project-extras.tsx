// A project's side of decision P114, beside its assignment of a connection (assignments.tsx): the mappings a person chooses
// (the service's statuses and Drydock's task statuses, and for a Notion data source which property fills which field), the
// auto-approve classes (off until turned on, never anything destructive), an item's attachments (listed and downloaded only when
// the person asks; a text file is kept only as its redacted copy), a typed row's properties, and the service's status of a
// linked item offered as a change to its task (never applied by itself).

import { css, on, type Handle } from 'remix/component';
import { ArrowRightLeft, Download, FileText, Paperclip, Plus, RefreshCw, Save, Trash2 } from 'lucide';
import type { AttachmentText, ExternalItem, IntegrationAssignment, IntegrationAttachment, IntegrationMapping, IntegrationView, MappingField } from '../../api/extTypes.ts';
import { formatDateTime, plural } from '../../lib/format.ts';
import { action, query, services } from '../../state/services.tsx';
import { Button } from '../../ui/button.tsx';
import { Badge, faint, mono, muted } from '../../ui/data.tsx';
import { ErrorNote, Loading, Notice } from '../../ui/feedback.tsx';
import { Switch } from '../../ui/form.tsx';
import { safeHref } from '../../ui/markdown.tsx';
import { Select, selectOptions, selectValues } from '../../ui/select.tsx';
import { t } from '../../ui/tokens.ts';
import { directionText, kindLabel, PROPERTY_FIELDS, sizeText, TASK_STATUSES } from './integrations.ts';

const panel = css({ display: 'grid', gap: t.s3, paddingTop: t.s3, borderTop: `1px solid ${t.border}`, '& h4': { fontSize: t.textMd, fontWeight: '600', margin: '0' }, '& p': { margin: '0' } });
const line = css({ display: 'flex', gap: t.s2, alignItems: 'center', flexWrap: 'wrap', minWidth: '0' });
const list = css({ margin: '0', padding: '0', listStyle: 'none', display: 'grid', gap: t.s2 });
const propsCss = css({ display: 'grid', gridTemplateColumns: 'minmax(90px, max-content) 1fr', gap: `2px ${t.s3}`, margin: '0', fontSize: t.textSm, '& dt': { color: t.textMuted }, '& dd': { margin: '0', overflowWrap: 'anywhere' } });
const preCss = css({ margin: '0', maxHeight: '260px', overflow: 'auto', whiteSpace: 'pre-wrap', overflowWrap: 'anywhere' });
// where on the vendor's item an attachment was found (decision P122)
const ATTACHMENT_SOURCE: Record<string, string> = {
  issue: 'the issue',
  event: 'the latest event',
  property: 'a files property',
  'page body': 'the page body',
  'issue body': 'the issue text',
  finding: 'the finding',
};

const DIRECTIONS = selectOptions([
  ['both', 'both ways'],
  ['to_vendor', 'Drydock to the service'],
  ['from_vendor', 'the service to Drydock'],
]);

/** A typed row's properties (a Notion data source), as the person sees them in the database. Outside text, shown inert. */
export function PropsTable(handle: Handle<{ item: ExternalItem }>) {
  return () => {
    const props = handle.props.item.properties ?? {};
    const names = Object.keys(props).sort();
    if (names.length === 0) return null;
    return (
      <dl mix={propsCss} data-testid="item-props" aria-label="Properties">
        {names.map((n) => {
          const p = props[n] ?? { type: '', text: '' };
          return (
            <div key={n} style={{ display: 'contents' }}>
              <dt>{n}</dt>
              <dd data-type={p.type}>{p.text || '-'}</dd>
            </div>
          );
        })}
      </dl>
    );
  };
}

/** The service's status of a linked item, offered as a change to its task: the person applies it, the task's own rules decide. */
export function StatusSuggestionNote(handle: Handle<{ item: ExternalItem; onChanged: () => void }>) {
  const s = services(handle);
  const act = action(handle);
  return () => {
    const { item, onChanged } = handle.props;
    const sg = item.statusSuggestion;
    if (!sg) return null;
    return (
      <Notice tone="info" testId="status-suggestion">
        {kindLabel(item.source)} says <strong>{sg.upstream}</strong>, which your mapping reads as {sg.to}; the task is {sg.taskStatus}.{' '}
        <Button
          size="sm"
          icon={ArrowRightLeft}
          disabled={act.busy !== ''}
          mix={[
            on('click', async () => {
              const r = await act.run('apply', () => s.client.integrationStatusApply(item.id));
              if (r) onChanged();
            }),
          ]}
        >
          Move the task to {sg.to}
        </Button>
        <ErrorNote error={act.error} />
      </Notice>
    );
  };
}

/** An item's attachments: read from the service, downloaded and read as text only on the person's action. */
export function ItemAttachments(handle: Handle<{ item: ExternalItem }>) {
  const s = services(handle);
  const act = action(handle);
  const q = query(handle, { load: () => s.client.integrationAttachments(handle.props.item.id), key: () => handle.props.item.id });
  let text: AttachmentText | undefined;
  return () => {
    const { item } = handle.props;
    const rows: IntegrationAttachment[] = q.data ?? [];
    const vendor = kindLabel(item.source);
    return (
      <section aria-label="Attachments" mix={panel} data-testid="item-attachments">
        <p mix={faint}>Nothing is downloaded until you ask. A download is size-capped and kept in Drydock's data folder, not in the repository.</p>
        <p mix={faint}>A text file is kept only with its secrets removed, and that copy is the only text an agent is given. Links to other sites are never fetched.</p>
        <ErrorNote error={q.error ?? act.error} what="Attachments" />
        <div mix={line}>
          <Button
            size="sm"
            icon={RefreshCw}
            disabled={act.busy !== ''}
            mix={[
              on('click', async () => {
                const r = await act.run('refresh', () => s.client.integrationAttachmentsRefresh(item.id));
                if (r) q.set(r);
              }),
            ]}
          >
            {act.busy === 'refresh' ? 'Reading...' : `Read the list from ${vendor}`}
          </Button>
        </div>
        {q.loading && !q.data ? <Loading /> : null}
        {q.data && rows.length === 0 ? <p mix={muted}>No attachments listed yet.</p> : null}
        {rows.length > 0 ? (
          <ul mix={list}>
            {rows.map((a) => {
              const href = safeHref(a.url ?? '');
              return (
                <li key={a.id} mix={line} data-testid="attachment" data-downloadable={String(a.downloadable)} data-downloaded={String(!!a.downloadedAt)}>
                  <strong>{a.name}</strong>
                  <span mix={faint}>
                    {a.mime || 'type unknown'} · {sizeText(a.size)}
                    {a.source ? <span data-testid="attachment-source"> · in {ATTACHMENT_SOURCE[a.source] ?? a.source}</span> : null}
                  </span>
                  <Badge tone="warn">untrusted</Badge>
                  {a.downloadedAt ? (
                    <span mix={faint}>
                      downloaded {formatDateTime(a.downloadedAt)}
                      {a.redactions > 0 ? `, ${plural(a.redactions, 'secret')} removed` : ''}
                    </span>
                  ) : null}
                  {a.downloadable ? (
                    <Button
                      size="sm"
                      variant="ghost"
                      icon={Download}
                      disabled={act.busy !== ''}
                      mix={[
                        on('click', async () => {
                          const r = await act.run(`dl-${a.id}`, () => s.client.integrationAttachmentDownload(a.id));
                          if (r) q.reload();
                        }),
                      ]}
                    >
                      {a.downloadedAt ? 'Download again' : 'Download'}
                    </Button>
                  ) : href ? (
                    <a href={href} target="_blank" rel="noopener noreferrer" data-rmx-document>
                      open the link
                    </a>
                  ) : (
                    <span mix={faint}>a link, not a file</span>
                  )}
                  {a.textKept ? (
                    <Button
                      size="sm"
                      variant="ghost"
                      icon={FileText}
                      disabled={act.busy !== ''}
                      mix={[
                        on('click', async () => {
                          text = text?.id === a.id ? undefined : await act.run(`txt-${a.id}`, () => s.client.integrationAttachmentText(a.id));
                          void handle.update();
                        }),
                      ]}
                    >
                      {text?.id === a.id ? 'Hide text' : 'Show text'}
                    </Button>
                  ) : null}
                </li>
              );
            })}
          </ul>
        ) : null}
        {text ? (
          <pre mix={preCss} tabIndex={0} data-testid="attachment-text">
            {text.text}
          </pre>
        ) : null}
      </section>
    );
  };
}

/** One editable status mapping. */
interface Row {
  remote: string;
  local: string;
  direction: string;
}

/** The mappings of an assignment: the service's statuses and Drydock's task statuses; a Notion data source's property fields. */
export function Mappings(handle: Handle<{ conn: IntegrationView; a: IntegrationAssignment }>) {
  const s = services(handle);
  const act = action(handle);
  const q = query(handle, { load: () => s.client.integrationMappings(handle.props.a.id), key: () => handle.props.a.id });
  let rows: Row[] | undefined;
  let props: Record<string, string> | undefined;
  let statusFields: MappingField[] = [];
  let propFields: MappingField[] = [];
  let saved = '';
  const set = (f: () => void) => {
    f();
    void handle.update();
  };
  return () => {
    const { conn, a } = handle.props;
    const label = kindLabel(conn.kind);
    const stored: IntegrationMapping[] = q.data ?? [];
    if (q.data && rows === undefined) {
      rows = stored.filter((m) => m.scope === 'status').map((m) => ({ remote: m.remote, local: m.local, direction: m.direction ?? 'both' }));
      props = Object.fromEntries(stored.filter((m) => m.scope === 'property').map((m) => [m.remote, m.local]));
    }
    const listId = `states-${handle.id}`;
    const propListId = `props-${handle.id}`;
    const isNotion = conn.kind === 'notion';
    return (
      <section aria-label="Mappings" mix={panel} data-testid="mappings">
        <h4>Status sync</h4>
        <p mix={faint}>
          When a linked task enters a status mapped toward {label}, Drydock proposes the change; it waits for your approval unless you turned on Status sync under Auto-approve.
        </p>
        <p mix={faint}>A status {label} reports is offered to you on the item. It never moves a task by itself.</p>
        <ErrorNote error={q.error ?? act.error} what="Mappings" />
        {q.loading && !q.data ? <Loading /> : null}
        <div mix={line}>
          <Button
            size="sm"
            variant="ghost"
            disabled={act.busy !== ''}
            mix={[
              on('click', async () => {
                const r = await act.run('fields', () => s.client.integrationMappingFields(a.id, 'status'));
                if (r) set(() => (statusFields = r));
              }),
            ]}
          >
            {act.busy === 'fields' ? 'Loading...' : `List ${label}'s statuses`}
          </Button>
        </div>
        <datalist id={listId}>
          {statusFields.map((f) => (
            <option key={`${f.property ?? ''}/${f.name}`} value={f.name}>
              {f.property ? `${f.property}: ${f.name}` : f.name}
            </option>
          ))}
        </datalist>
        {(rows ?? []).map((r, i) => (
          <div key={i} mix={line} data-testid="status-mapping">
            <input
              aria-label={`${label} status`}
              list={listId}
              value={r.remote}
              placeholder={`${label} status`}
              mix={on('input', (e) => set(() => (rows![i] = { ...rows![i]!, remote: e.currentTarget.value })))}
            />
            <Select size="sm" label="Direction" value={r.direction} options={DIRECTIONS} onChange={(v) => set(() => (rows![i] = { ...rows![i]!, direction: v }))} />
            <Select size="sm" label="Task status" value={r.local} placeholder="Task status" options={selectValues([...TASK_STATUSES])} onChange={(v) => set(() => (rows![i] = { ...rows![i]!, local: v }))} />
            <Button size="sm" variant="ghost" icon={Trash2} aria-label="Remove this mapping" mix={[on('click', () => set(() => rows!.splice(i, 1)))]} />
          </div>
        ))}
        <div mix={line}>
          <Button size="sm" icon={Plus} mix={[on('click', () => set(() => (rows = [...(rows ?? []), { remote: '', local: 'COMPLETED', direction: 'to_vendor' }])))]}>
            Add a status
          </Button>
          <Button
            size="sm"
            variant="primary"
            icon={Save}
            disabled={act.busy !== '' || !rows}
            mix={[
              on('click', async () => {
                const r = await act.run('save', () => s.client.integrationMappingsSet(a.id, 'status', (rows ?? []).filter((x) => x.remote.trim())));
                if (r) set(() => ((saved = `Saved ${plural(r.length, 'status mapping')}.`), (rows = r.map((m) => ({ remote: m.remote, local: m.local, direction: m.direction ?? 'both' })))));
              }),
            ]}
          >
            Save status mappings
          </Button>
        </div>
        {(rows ?? []).length > 0 ? (
          <p mix={faint}>
            {(rows ?? [])
              .filter((r) => r.remote)
              .map((r) => `${r.remote} = ${r.local} (${directionText(r.direction)})`)
              .join('; ')}
          </p>
        ) : null}
        {isNotion ? (
          <>
            <h4>Typed rows</h4>
            <p mix={faint}>Which property of the data source's rows fills each field. Without one, the first status property is the state and the first people property the assignee.</p>
            <div mix={line}>
              <Button
                size="sm"
                variant="ghost"
                disabled={act.busy !== ''}
                mix={[
                  on('click', async () => {
                    const r = await act.run('pfields', () => s.client.integrationMappingFields(a.id, 'property'));
                    if (r) set(() => (propFields = r));
                  }),
                ]}
              >
                List the data source's properties
              </Button>
            </div>
            <datalist id={propListId}>
              {propFields.map((f) => (
                <option key={f.name} value={f.name}>
                  {f.name} ({f.type})
                </option>
              ))}
            </datalist>
            {PROPERTY_FIELDS.map((field) => (
              <div key={field} mix={line} data-testid="property-mapping">
                <span mix={mono} style={{ minWidth: '80px' }}>
                  {field}
                </span>
                <input
                  aria-label={`Property for ${field}`}
                  list={propListId}
                  value={props?.[field] ?? ''}
                  placeholder="Notion property"
                  mix={on('input', (e) => set(() => (props = { ...(props ?? {}), [field]: e.currentTarget.value })))}
                />
              </div>
            ))}
            <div>
              <Button
                size="sm"
                variant="primary"
                icon={Save}
                disabled={act.busy !== ''}
                mix={[
                  on('click', async () => {
                    const ms = Object.entries(props ?? {})
                      .filter(([, v]) => v.trim())
                      .map(([remote, local]) => ({ remote, local: local.trim() }));
                    const r = await act.run('psave', () => s.client.integrationMappingsSet(a.id, 'property', ms));
                    if (r) set(() => (saved = `Saved ${plural(r.length, 'property mapping')}. The next sync reads every row again.`));
                  }),
                ]}
              >
                Save property mappings
              </Button>
            </div>
          </>
        ) : null}
        {saved ? (
          <Notice tone="ok" role="status">
            {saved}
          </Notice>
        ) : null}
      </section>
    );
  };
}

/** The auto-approve classes of a connection in this project: each off until the person turns it on. */
export function AutoApprove(handle: Handle<{ conn: IntegrationView; projectId: string }>) {
  const s = services(handle);
  const act = action(handle);
  const q = query(handle, {
    load: () => s.client.integrationAutoApprove(handle.props.conn.id, handle.props.projectId),
    key: () => [handle.props.conn.id, handle.props.projectId],
    when: (e) => e.type === 'integration.auto_approve.changed',
  });
  return () => {
    const { conn, projectId } = handle.props;
    const rows = q.data ?? [];
    return (
      <section aria-label="Auto-approve" mix={panel} data-testid="auto-approve">
        <h4>Auto-approve</h4>
        <p mix={faint}>
          Every write to {kindLabel(conn.kind)} waits for your approval. A class lets one narrow kind of write Drydock itself makes go out without asking, approved in your name.
        </p>
        <p mix={faint}>Each is off until you turn it on. None covers a delete, an ignore, a snooze or a bulk change, and nothing an agent proposes is ever in one.</p>
        <ErrorNote error={q.error ?? act.error} what="Auto-approve" />
        {q.loading && !q.data ? <Loading /> : null}
        {rows.map((c) => (
          <div key={c.class} data-testid="auto-class" data-class={c.class} data-enabled={String(c.enabled)}>
            <Switch
              label={c.label}
              checked={c.enabled}
              disabled={act.busy !== ''}
              onChange={async (v) => {
                const r = await act.run(c.class, () => s.client.integrationAutoApproveSet({ integrationId: conn.id, projectId, class: c.class, enabled: v, version: c.version }));
                if (r) q.reload();
              }}
            />
            <p mix={faint}>
              {c.description}
              {c.updatedBy && c.updated ? ` Last changed by ${c.updatedBy} on ${formatDateTime(c.updated)}.` : ''}
            </p>
          </div>
        ))}
        {q.data && rows.length === 0 ? <p mix={muted}>{kindLabel(conn.kind)} has no write that can be approved this way.</p> : null}
      </section>
    );
  };
}

/** The attachments toggle of an item row. */
export function AttachmentsButton(handle: Handle<{ open: boolean; onToggle: () => void }>) {
  return () => (
    <Button size="sm" variant="ghost" icon={Paperclip} aria-expanded={handle.props.open} mix={[on('click', handle.props.onToggle)]}>
      Attachments
    </Button>
  );
}
