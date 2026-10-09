// Requirements: upload or paste requirements text, link its acceptance criteria to tasks, and trace each requirement to the
// tasks' test evidence. The text is outside text: stored with secrets redacted, shown inert, and passages that look like
// instructions to an agent are quarantined until the human releases them. "Verified" is the core's judgement only.

import { css, on, type Handle } from 'remix/component';
import { Archive, ArchiveRestore, Check, ChevronRight, FileText, Link2, Search, ShieldAlert, Upload } from 'lucide';
import type { Requirement, RequirementHit, RequirementTrace } from '../../api/extTypes.ts';
import type { Task } from '../../api/types.ts';
import { formatDateTime } from '../../lib/format.ts';
import { routes } from '../../routes.ts';
import { action, query, services, watch } from '../../state/services.tsx';
import { Button, buttonStyle } from '../../ui/button.tsx';
import { Badge, StatusChip, faint, muted } from '../../ui/data.tsx';
import { Empty, ErrorNote, Loading, Notice } from '../../ui/feedback.tsx';
import { Field, formActions } from '../../ui/form.tsx';
import { Icon } from '../../ui/icon.tsx';
import { Page, Section, card, spacer } from '../../ui/layout.tsx';
import { Markdown } from '../../ui/markdown.tsx';
import { t } from '../../ui/tokens.ts';
import { criteriaLines, evidenceOutcome, fileProblem, linkedTaskIds, titleFromFile } from './requirements.ts';

// --- trace ---

const traceCss = css({ display: 'grid', gap: t.s3 });
const traceHead = css({
  display: 'flex',
  alignItems: 'center',
  gap: t.s2,
  flexWrap: 'wrap',
  fontSize: t.textMd,
});
const traceTask = css({
  display: 'grid',
  gap: t.s2,
  padding: `${t.s3} ${t.s4}`,
  border: `1px solid ${t.border}`,
  borderRadius: t.rMd,
  background: t.bgSurface,
  '& .head': {
    display: 'flex',
    alignItems: 'center',
    gap: t.s2,
    flexWrap: 'wrap',
  },
  '& .head a': { fontWeight: '600' },
});
const criteriaCss = css({
  display: 'grid',
  gap: '4px',
  margin: '0',
  padding: '0',
  listStyle: 'none',
  fontSize: t.textMd,
  '& li': { display: 'flex', alignItems: 'flex-start', gap: t.s2 },
  '& li svg': { marginTop: '3px', color: t.textMuted, flex: 'none' },
});
const evidenceCss = css({
  display: 'grid',
  gap: '4px',
  margin: '0',
  padding: `${t.s2} 0 0`,
  listStyle: 'none',
  borderTop: `1px dashed ${t.border}`,
  fontSize: t.textSm,
  '& li': {
    display: 'flex',
    alignItems: 'center',
    gap: t.s2,
    flexWrap: 'wrap',
  },
});

/** requirement -> tasks -> criteria -> evidence. "Verified" is the core's judgement; stale links are shown as stale. */
export function TraceView(handle: Handle<{ t: RequirementTrace }>) {
  return () => {
    const tr = handle.props.t;
    const tasks = tr.tasks ?? [];
    return (
      <div mix={traceCss} data-testid="trace" data-verified={String(tr.verified)}>
        <p mix={traceHead}>
          <Badge tone={tr.verified ? 'ok' : 'warn'}>{tr.verified ? 'verified' : 'not verified'}</Badge>
          <span>
            {tr.criteria} criteria linked, {tr.tasksCompleted} of {tasks.length} task(s) completed
          </span>
        </p>
        {tr.tasksStale > 0 ? <Notice tone="warn">{tr.tasksStale} task(s) were linked to an older version of this requirement.</Notice> : null}
        {tasks.length === 0 ? <p mix={faint}>No task is linked to this requirement.</p> : null}
        {tasks.map((tt) => (
          <div key={tt.taskId} mix={traceTask} data-testid="trace-task">
            <div class="head">
              <a href={routes.task.href({ id: tt.taskId })}>{tt.title}</a>
              <StatusChip status={tt.status} />
              <span mix={faint}>linked to v{tt.linkedVersion}</span>
              {tt.stale ? <Badge tone="warn">stale link</Badge> : null}
              <span mix={spacer} />
              {tt.hasTests ? (
                <Badge tone={tt.testsPassing ? 'ok' : 'err'}>{tt.testsPassing ? 'tests passing' : 'tests failing'}</Badge>
              ) : (
                <Badge>no test evidence</Badge>
              )}
            </div>
            {(tt.criteria ?? []).length > 0 ? (
              <ul mix={criteriaCss} aria-label={`Acceptance criteria for ${tt.title}`}>
                {(tt.criteria ?? []).map((c, i) => (
                  <li key={i}>
                    <Icon icon={Check} size={13} />
                    <span>{c}</span>
                  </li>
                ))}
              </ul>
            ) : null}
            {(tt.evidence ?? []).length > 0 ? (
              <ul mix={evidenceCss} aria-label={`Evidence for ${tt.title}`}>
                {(tt.evidence ?? []).map((e) => {
                  const o = evidenceOutcome(e);
                  return (
                    <li key={e.id}>
                      <Badge tone={o.tone}>{o.label}</Badge>
                      <strong>{e.kind}</strong>
                      <span>{e.summary}</span>
                    </li>
                  );
                })}
              </ul>
            ) : null}
          </div>
        ))}
      </div>
    );
  };
}

// --- one requirement ---

const detailCss = css({
  display: 'grid',
  gap: t.s5,
  padding: t.s5,
  border: `1px solid ${t.border}`,
  borderRadius: t.rLg,
  background: t.bgSurface,
  minWidth: '0',
  '& > header': {
    display: 'flex',
    alignItems: 'center',
    gap: t.s3,
    flexWrap: 'wrap',
  },
  '& > header h2': {
    fontSize: t.textXl,
    fontWeight: '600',
    overflowWrap: 'anywhere',
  },
  '& h3': { fontSize: t.textMd, fontWeight: '600', marginBottom: t.s2 },
});
const textBox = css({
  maxHeight: '420px',
  overflow: 'auto',
  padding: t.s4,
  border: `1px solid ${t.border}`,
  borderRadius: t.rMd,
  background: t.bgApp,
  '& > summary': { marginBottom: t.s2 },
});
const quarCss = css({
  display: 'grid',
  gap: t.s2,
  padding: t.s3,
  borderRadius: t.rMd,
  background: t.warningSoft,
  '& .chunk': {
    display: 'grid',
    gap: t.s2,
    padding: t.s3,
    borderRadius: t.rMd,
    background: t.bgSurface,
  },
});
const versionsCss = css({
  display: 'grid',
  gap: '2px',
  margin: '0',
  padding: '0',
  listStyle: 'none',
  fontSize: t.textMd,
  '& li': { display: 'flex', gap: t.s2, alignItems: 'baseline' },
});
const linksCss = css({
  display: 'grid',
  gap: '2px',
  margin: '0',
  padding: '0',
  listStyle: 'none',
  fontSize: t.textMd,
  '& li': {
    display: 'flex',
    gap: t.s2,
    alignItems: 'baseline',
    flexWrap: 'wrap',
    padding: `4px 0`,
    borderTop: `1px solid ${t.border}`,
  },
  '& li:first-child': { borderTop: '0' },
});
const twoCol = css({
  display: 'grid',
  gridTemplateColumns: 'repeat(auto-fit, minmax(300px, 1fr))',
  gap: t.s5,
  alignItems: 'start',
});
const subForm = css({ display: 'grid', gap: t.s3 });

function RequirementDetail(handle: Handle<{ r: Requirement; tasks: Task[]; onChanged: () => void }>) {
  const s = services(handle);
  const act = action(handle);
  const key = () => [handle.props.r.id, handle.props.r.version];
  const id = () => handle.props.r.id;
  const text = query(handle, {
    load: () => s.client.requirementText(id()),
    key,
  });
  const versions = query(handle, {
    load: () => s.client.requirementVersions(id()),
    key,
  });
  const trace = query(handle, {
    load: () => s.client.requirementTrace(id()),
    key,
    when: (e) => e.type.startsWith('task.') || e.type.startsWith('requirement.'),
  });
  const quarantined = query(handle, {
    load: () => s.client.requirementQuarantined(id()),
    key,
  });
  const links = query(handle, {
    load: () => s.client.requirementLinks({ requirementId: id() }),
    key: id,
    when: (e) => e.type.startsWith('requirement.'),
  });
  let newText = '';
  let taskId = '';
  let criteria = '';
  const set = (fn: () => void) => {
    fn();
    void handle.update();
  };
  const done = () => {
    handle.props.onChanged();
    trace.reload();
    links.reload();
    versions.reload();
    text.reload();
  };

  return () => {
    const { r, tasks } = handle.props;
    const busy = act.busy !== '';
    const linkList = links.data ?? [];
    return (
      <section mix={detailCss} aria-label={`Requirement ${r.title}`} data-testid="requirement-detail">
        <header>
          <span style={{ display: 'inline-flex', color: 'var(--text-muted)' }}>
            <Icon icon={FileText} size={18} />
          </span>
          <h2>{r.title}</h2>
          <span mix={muted}>
            v{r.version}, {r.source}, {r.chunks} passages
          </span>
          <span mix={spacer} />
          <Button
            size="sm"
            variant="ghost"
            icon={r.archived ? ArchiveRestore : Archive}
            disabled={busy}
            mix={on('click', async () => {
              await act.run('archive', () => s.client.requirementArchive(r.id, !r.archived));
              done();
            })}
          >
            {r.archived ? 'Unarchive' : 'Archive'}
          </Button>
        </header>
        <ErrorNote error={act.error} />
        {r.quarantined > 0 ? (
          <div mix={quarCss} data-testid="req-quarantine">
            <p>
              <Icon icon={ShieldAlert} size={14} /> {r.quarantined} passage(s) look like instructions to an agent and are kept out of search.
            </p>
            {(quarantined.data ?? []).map((c) => (
              <div key={c.chunkId} class="chunk">
                <Markdown source={c.body} maxChars={1500} />
                <div>
                  <Button
                    size="sm"
                    disabled={busy}
                    mix={on('click', async () => {
                      await act.run('release', () => s.client.requirementReleaseChunk(c.chunkId));
                      done();
                      quarantined.reload();
                    })}
                  >
                    Release this passage
                  </Button>
                </div>
              </div>
            ))}
          </div>
        ) : null}

        <div>
          <h3>Trace</h3>
          {trace.data ? <TraceView t={trace.data} /> : trace.error ? <ErrorNote error={trace.error} /> : <Loading />}
        </div>

        <div mix={twoCol}>
          <div mix={subForm}>
            <h3>Link criteria to a task</h3>
            <form
              mix={[
                subForm,
                on('submit', async (e) => {
                  e.preventDefault();
                  if (busy || !taskId || !criteria.trim()) return;
                  const x = await act.run('link', () => s.client.requirementLink(r.id, taskId, criteriaLines(criteria)));
                  if (x) {
                    criteria = '';
                    done();
                  }
                }),
              ]}
            >
              <Field label="Task">
                {(fid) => (
                  <select id={fid} aria-label="Task to link" value={taskId} mix={on('change', (e) => set(() => (taskId = e.currentTarget.value)))}>
                    <option value="" selected={taskId === ''}>
                      Choose a task
                    </option>
                    {tasks.map((tk) => (
                      <option key={tk.id} value={tk.id} selected={tk.id === taskId}>
                        #{tk.seq} {tk.title}
                      </option>
                    ))}
                  </select>
                )}
              </Field>
              <Field label="Criteria (one per line; they must appear in this requirement)">
                {(fid) => <textarea id={fid} rows={3} value={criteria} mix={on('input', (e) => set(() => (criteria = e.currentTarget.value)))} />}
              </Field>
              <div mix={formActions}>
                <Button type="submit" icon={Link2} disabled={busy || !taskId || !criteria.trim()}>
                  Link
                </Button>
              </div>
            </form>
            {linkList.length > 0 ? (
              <>
                <ul mix={linksCss} aria-label="Linked criteria">
                  {linkList.map((l) => (
                    <li key={l.id} data-testid="req-link">
                      <a href={routes.task.href({ id: l.taskId })}>{l.taskId}</a>: <span>{l.criterion}</span> <span mix={faint}>v{l.version}</span>{' '}
                      {l.stale ? <Badge tone="warn">stale</Badge> : null}
                    </li>
                  ))}
                </ul>
                <div>
                  <Button
                    size="sm"
                    variant="ghost"
                    disabled={busy}
                    mix={on('click', async () => {
                      for (const tid of linkedTaskIds(linkList)) await act.run('unlink', () => s.client.requirementUnlink(r.id, tid));
                      done();
                    })}
                  >
                    Remove all links
                  </Button>
                </div>
              </>
            ) : null}
          </div>

          <div mix={subForm}>
            <h3>Versions</h3>
            <ul mix={versionsCss}>
              {(versions.data ?? []).map((v) => (
                <li key={v.version}>
                  <Badge tone={v.version === r.version ? 'accent' : 'muted'}>v{v.version}</Badge>
                  <span mix={faint}>
                    {formatDateTime(v.created)}, {v.chunks} passages, by {v.createdBy || 'unknown'}
                  </span>
                </li>
              ))}
            </ul>
            <h3>New version</h3>
            <form
              mix={[
                subForm,
                on('submit', async (e) => {
                  e.preventDefault();
                  if (busy || !newText.trim()) return;
                  const x = await act.run('update', () => s.client.requirementUpdate(r.id, newText));
                  if (x) {
                    newText = '';
                    done();
                  }
                }),
              ]}
            >
              <Field label="Replacement text">
                {(fid) => <textarea id={fid} rows={4} value={newText} mix={on('input', (e) => set(() => (newText = e.currentTarget.value)))} />}
              </Field>
              <div mix={formActions}>
                <Button type="submit" disabled={busy || !newText.trim()}>
                  Save as a new version
                </Button>
              </div>
            </form>
          </div>
        </div>

        <details open mix={textBox}>
          <summary>Text (outside text, shown inert)</summary>
          <Markdown source={text.data ?? ''} maxChars={30000} />
        </details>
      </section>
    );
  };
}

// --- the screen ---

const listCss = css({
  margin: '0',
  padding: '0',
  listStyle: 'none',
  border: `1px solid ${t.border}`,
  borderRadius: t.rLg,
  background: t.bgSurface,
  overflow: 'hidden',
});
const rowCss = css({
  display: 'grid',
  gridTemplateColumns: '20px minmax(0, 1fr) auto auto auto',
  alignItems: 'center',
  gap: t.s3,
  padding: `10px ${t.s4}`,
  borderTop: `1px solid ${t.border}`,
  fontSize: t.textMd,
  '&:first-child': { borderTop: '0' },
  '&[data-open="true"]': { background: t.bgActive },
  '&[data-archived="true"] .name': { color: t.textMuted },
  '& .name': {
    display: 'flex',
    alignItems: 'center',
    gap: t.s2,
    minWidth: '0',
    fontWeight: '500',
  },
  '& .name span': {
    overflow: 'hidden',
    textOverflow: 'ellipsis',
    whiteSpace: 'nowrap',
  },
  '& .num': {
    color: t.textMuted,
    fontSize: t.textSm,
    fontVariantNumeric: 'tabular-nums',
    display: 'flex',
    alignItems: 'center',
    gap: '6px',
  },
});
const formCss = css({ display: 'grid', gap: t.s4 });
const uploadCss = css({
  display: 'flex',
  alignItems: 'center',
  gap: t.s3,
  flexWrap: 'wrap',
  '& label:focus-within': { boxShadow: t.focus },
  '& .hint': { color: t.textMuted, fontSize: t.textSm },
});
const searchForm = css({
  display: 'flex',
  alignItems: 'center',
  gap: t.s2,
  '& input': { flex: '1' },
});
const hitsCss = css({
  display: 'grid',
  gap: t.s2,
  margin: '0',
  padding: '0',
  listStyle: 'none',
  '& li': {
    display: 'grid',
    gap: t.s2,
    padding: `${t.s3} ${t.s4}`,
    border: `1px solid ${t.border}`,
    borderRadius: t.rMd,
    background: t.bgSurface,
  },
  '& .head': {
    display: 'flex',
    gap: t.s2,
    alignItems: 'baseline',
    flexWrap: 'wrap',
  },
});

export function RequirementsScreen(handle: Handle) {
  const s = services(handle);
  watch(handle, s.workspace);
  watch(handle, s.project);
  const act = action(handle);
  const pid = () => s.workspace.projectId;
  const q = query(handle, {
    load: () => s.client.requirementList(pid()!, true),
    when: (e) => e.type.startsWith('requirement.'),
    key: pid,
    enabled: () => !!pid(),
  });
  let title = '';
  let text = '';
  let fileErr = '';
  let fileName = '';
  let open: string | undefined;
  let search = '';
  let hits: RequirementHit[] | undefined;
  const set = (fn: () => void) => {
    fn();
    void handle.update();
  };

  const onFile = async (input: HTMLInputElement) => {
    const f = input.files?.[0];
    if (!f) return;
    fileErr = fileProblem(f.size);
    fileName = '';
    if (fileErr) return set(() => undefined);
    text = await f.text();
    fileName = f.name;
    if (!title.trim()) title = titleFromFile(f.name);
    void handle.update();
  };
  const add = async () => {
    const projectId = pid();
    if (!projectId) return;
    const r = await act.run('add', () => s.client.requirementAdd(projectId, title.trim(), text));
    if (r) {
      title = '';
      text = '';
      fileName = '';
      open = r.id;
      q.reload();
      void handle.update();
    }
  };

  return () => {
    const list = q.data ?? [];
    const sel = list.find((r) => r.id === open);
    const busy = act.busy !== '';
    const projectId = pid();
    return (
      <Page title="Requirements" icon={FileText} subtitle={q.data ? `${list.length} requirement${list.length === 1 ? '' : 's'}` : undefined}>
        <p mix={muted}>
          Upload or paste requirements text, link its criteria to tasks, and see which tasks have passing test evidence. The text is outside text: it is stored
          with secrets redacted, shown inert, and passages that look like instructions to an agent are quarantined.
        </p>
        <ErrorNote error={q.error ?? act.error} what="Requirements" />
        {q.loading && !q.data ? <Loading /> : null}
        {q.data && list.length === 0 ? (
          <div mix={card}>
            <Empty title="No requirements yet." icon={FileText}>
              <p>Add requirements text below: a Markdown file or pasted text.</p>
            </Empty>
          </div>
        ) : null}
        {list.length > 0 ? (
          <ul mix={listCss} aria-label="Requirements">
            {list.map((r) => (
              <li key={r.id} mix={rowCss} data-testid="requirement" data-title={r.title} data-open={String(open === r.id)} data-archived={String(r.archived)}>
                <Icon icon={FileText} size={15} />
                <div class="name">
                  <span>{r.title}</span>
                  {r.archived ? <Badge>archived</Badge> : null}
                  {r.quarantined > 0 ? <Badge tone="warn">{r.quarantined} quarantined</Badge> : null}
                </div>
                <span class="num" title="Version">
                  v{r.version}
                </span>
                <span class="num" title="Links to tasks">
                  <Icon icon={Link2} size={13} />
                  {r.links}
                  {r.staleLinks > 0 ? <Badge tone="warn">{r.staleLinks} stale</Badge> : null}
                </span>
                <Button
                  size="sm"
                  variant="ghost"
                  aria-expanded={open === r.id ? 'true' : 'false'}
                  mix={on('click', () => set(() => (open = open === r.id ? undefined : r.id)))}
                >
                  {open === r.id ? 'Close' : 'Open'}
                  <Icon icon={ChevronRight} size={13} />
                </Button>
              </li>
            ))}
          </ul>
        ) : null}
        {sel ? <RequirementDetail key={sel.id} r={sel} tasks={s.project.taskList} onChanged={() => q.reload()} /> : null}

        <Section title="Add requirements">
          <form
            mix={[
              card,
              formCss,
              on('submit', (e) => {
                e.preventDefault();
                if (!busy && title.trim() && text.trim()) void add();
              }),
            ]}
          >
            <Field label="Title">{(id) => <input id={id} value={title} mix={on('input', (e) => set(() => (title = e.currentTarget.value)))} />}</Field>
            <div mix={uploadCss}>
              <label mix={buttonStyle('secondary', 'sm')}>
                <Icon icon={Upload} size={14} />
                Choose a file
                <input
                  type="file"
                  class="sr-only"
                  accept=".md,.markdown,.txt,text/plain,text/markdown"
                  aria-label="Upload a text or Markdown file (read in this browser, then sent to the core)"
                  mix={on('change', (e) => void onFile(e.currentTarget))}
                />
              </label>
              <span class="hint">{fileName ? `Read ${fileName}.` : 'A text or Markdown file, read in this browser, then sent to the core.'}</span>
            </div>
            {fileErr ? <ErrorNote error={fileErr} /> : null}
            <Field label="Text">{(id) => <textarea id={id} rows={8} value={text} mix={on('input', (e) => set(() => (text = e.currentTarget.value)))} />}</Field>
            <div mix={formActions}>
              <Button type="submit" variant="primary" disabled={busy || !title.trim() || !text.trim()}>
                Add requirement
              </Button>
            </div>
          </form>
        </Section>

        <Section title="Search requirements">
          <form
            mix={[
              searchForm,
              on('submit', async (e) => {
                e.preventDefault();
                if (!search.trim() || !projectId) return;
                const r = await act.run('search', () => s.client.requirementSearch(projectId, search.trim(), '', 8));
                set(() => (hits = r));
              }),
            ]}
          >
            <input
              type="search"
              aria-label="Search requirements"
              placeholder="Search the text of every requirement"
              value={search}
              mix={on('input', (e) => set(() => (search = e.currentTarget.value)))}
            />
            <Button type="submit" icon={Search} disabled={!search.trim()}>
              Search
            </Button>
          </form>
          {hits ? (
            <ol mix={hitsCss} data-testid="req-hits">
              {hits.length === 0 ? <li mix={muted}>Nothing found.</li> : null}
              {hits.map((h) => (
                <li key={h.chunkId}>
                  <div class="head">
                    <code>{h.citation}</code>
                    <strong>{h.title}</strong>
                    <span mix={faint}>
                      v{h.version}, {h.headingPath}
                    </span>
                  </div>
                  <Markdown source={h.text} maxChars={2000} />
                </li>
              ))}
            </ol>
          ) : null}
        </Section>
      </Page>
    );
  };
}
