// Expert packs: bodies of reference text (documentation, standards) that agents search and cite. Everything inside a pack is
// outside text: it is shown inert, passages that look like instructions are quarantined until the human releases them, and
// nothing in a pack can grant a permission or make a decision.

import { css, on, ref, type Handle } from 'remix/component';
import { BookOpen, Download, ExternalLink, FilePlus, Library, MessageSquareText, RefreshCw, Search, ShieldAlert, Trash2, Upload } from 'lucide';
import type { Pack, PackAttachment, PackCitation, PackConsult, PackFile, PackSearchResult, PackUpload, QuarantinedChunk } from '../../api/extTypes.ts';
import { formatCost, formatDateTime, plural } from '../../lib/format.ts';
import { action, query, services, watch } from '../../state/services.tsx';
import { Button } from '../../ui/button.tsx';
import { Badge, faint, muted } from '../../ui/data.tsx';
import { Empty, ErrorNote, Loading, Notice } from '../../ui/feedback.tsx';
import { Checkbox, Field, errorAttrs, formActions } from '../../ui/form.tsx';
import { Icon } from '../../ui/icon.tsx';
import { Page, Section, card, spacer, stack } from '../../ui/layout.tsx';
import { Markdown, safeHref } from '../../ui/markdown.tsx';
import { t } from '../../ui/tokens.ts';
import { visible } from '../../lib/visible.ts';
import { ConfirmAction } from '../shared/confirm-action.tsx';
import {
  attachedText,
  checkPackFiles,
  fileSize,
  freshnessText,
  hasOwnFiles,
  PACK_FILE_ACCEPT,
  packIdFromTitle,
  packIds,
  refreshText,
  searchHeadline,
  validPackId,
  zeroTime,
} from './packs.ts';

// --- citations and results (pack text is untrusted) ---

const citeCss = css({
  display: 'inline-flex',
  alignItems: 'baseline',
  gap: '6px',
  flexWrap: 'wrap',
  fontSize: t.textSm,
  '& a': {
    display: 'inline-flex',
    alignItems: 'center',
    gap: '3px',
    fontWeight: '500',
  },
});

/** A citation as the pack search returned it. The URL is outside text: linked only when it is http or https. */
export function CitationLine(handle: Handle<{ c: PackCitation }>) {
  return () => {
    const c = handle.props.c;
    const href = safeHref(c.url);
    return (
      <span mix={citeCss} data-testid="citation" data-page={c.page ? String(c.page) : undefined}>
        <code>
          {c.pack}@{c.version}
        </code>
        {href ? (
          <a href={href} target="_blank" rel="noopener noreferrer" data-rmx-document title={href}>
            {c.headingPath || c.url}
            <Icon icon={ExternalLink} size={11} />
          </a>
        ) : (
          <span title="Local or non-web source">{c.headingPath || c.url}</span>
        )}
        <span mix={muted}>
          {c.origin}
          {!zeroTime(c.fetchedAt) ? `, fetched ${formatDateTime(c.fetchedAt)}` : ''}, hash {c.contentHash.slice(0, 8)}
        </span>
      </span>
    );
  };
}

const resultCss = css({ display: 'grid', gap: t.s3 });
const hitsCss = css({
  display: 'grid',
  gap: t.s2,
  margin: '0',
  padding: '0',
  listStyle: 'none',
  '& > li': {
    display: 'grid',
    gap: t.s2,
    padding: `${t.s3} ${t.s4}`,
    border: `1px solid ${t.border}`,
    borderRadius: t.rMd,
    background: t.bgSurface,
    minWidth: '0',
  },
});
const headlineCss = css({
  display: 'flex',
  alignItems: 'center',
  gap: t.s2,
  flexWrap: 'wrap',
  fontSize: t.textMd,
});

export function SearchResultView(handle: Handle<{ r: PackSearchResult }>) {
  return () => {
    const r = handle.props.r;
    const hits = r.hits ?? [];
    return (
      <div mix={resultCss} data-testid="pack-search-result" data-found={String(r.found)}>
        <p mix={headlineCss}>
          <strong>{searchHeadline(r)}</strong>
          <span>confidence</span> <Badge tone={r.confidence === 'high' ? 'ok' : r.confidence === 'none' ? 'muted' : 'warn'}>{r.confidence}</Badge>
          <span mix={faint}>(a heuristic from the search, not calibrated); source age {plural(r.sourceAgeDays, 'day')}</span>
        </p>
        {Object.entries(r.unavailable ?? {}).map(([k, v]) => (
          <Notice key={k} tone="warn">
            {k} unavailable: {v}
          </Notice>
        ))}
        {(r.warn ?? []).map((w, i) => (
          <Notice key={i} tone="warn">
            {w}
          </Notice>
        ))}
        {(r.suspiciousDropped ?? 0) > 0 ? (
          <Notice tone="warn">{plural(r.suspiciousDropped ?? 0, 'result')} {r.suspiciousDropped === 1 ? 'was' : 'were'} dropped because they looked like instructions to an agent.</Notice>
        ) : null}
        {hits.length > 0 ? (
          <ol mix={hitsCss}>
            {hits.map((h, i) => (
              <li key={i} data-testid="pack-hit">
                <CitationLine c={h.citation} />
                <Markdown source={h.snippet || h.text} maxChars={4000} />
              </li>
            ))}
          </ol>
        ) : null}
        {(r.attributions ?? []).length > 0 ? <p mix={faint}>Attribution: {(r.attributions ?? []).join('; ')}</p> : null}
      </div>
    );
  };
}

const answerCss = css({
  padding: t.s4,
  border: `1px solid ${t.border}`,
  borderRadius: t.rMd,
  background: t.bgSurface,
});
const quoteCss = css({
  margin: '0',
  padding: `${t.s1} ${t.s3}`,
  borderLeft: `3px solid ${t.border}`,
  color: t.textMuted,
  fontSize: t.textSm,
  whiteSpace: 'pre-wrap',
  overflowWrap: 'anywhere',
});
const sourcesCss = css({
  display: 'grid',
  gap: t.s2,
  margin: '0',
  paddingLeft: '20px',
  '& li': { display: 'grid', gap: '2px' },
});

export function ConsultView(handle: Handle<{ r: PackConsult }>) {
  return () => {
    const r = handle.props.r;
    const used = (r.sources ?? []).filter((s) => s.used);
    const others = (r.sources ?? []).filter((s) => !s.used);
    return (
      <div mix={resultCss} data-testid="pack-consult" data-found={String(r.found)}>
        <p mix={headlineCss}>
          <Badge tone={r.confidence === 'high' ? 'ok' : r.confidence === 'none' ? 'muted' : 'warn'}>{r.confidence}</Badge>
          <span mix={faint}>
            route {r.route}
            {r.model ? `, model ${r.model}` : ''}
            {r.sourceAgeDays >= 0 ? `; source age ${plural(r.sourceAgeDays, 'day')}` : ''}
          </span>
        </p>
        {r.degraded ? <Notice tone="warn">{r.degraded}</Notice> : null}
        <div mix={answerCss}>
          <Markdown source={r.answer || '(no answer)'} />
        </div>
        {r.model ? (
          <>
            <h4>Quoted from</h4>
            <ol mix={sourcesCss} data-testid="consult-used">
              {used.map((s) => (
                <li key={s.n} value={s.n}>
                  <CitationLine c={s.citation} />
                  <blockquote mix={quoteCss}>{s.excerpt}</blockquote>
                </li>
              ))}
            </ol>
            {others.length > 0 ? (
              <>
                <h4>Also found, not used in the answer</h4>
                <ol mix={sourcesCss} data-testid="consult-other">
                  {others.map((s) => (
                    <li key={s.n} value={s.n}>
                      <CitationLine c={s.citation} />
                      <span mix={faint}>{s.excerpt}</span>
                    </li>
                  ))}
                </ol>
              </>
            ) : null}
          </>
        ) : (
          <>
            <h4>Citations</h4>
            <ol mix={sourcesCss}>
              {(r.sources ?? []).map((s) => (
                <li key={s.n}>
                  <CitationLine c={s.citation} />
                  <span mix={faint}>{s.excerpt}</span>
                </li>
              ))}
            </ol>
          </>
        )}
        {r.model ? (
          <p mix={faint}>
            Cost of this question: {formatCost(r.usage.costUsd)} ({r.usage.inputTokens} in / {r.usage.outputTokens} out tokens). The answer is generated from the
            cited text; check the citations.
          </p>
        ) : (
          <p mix={faint} data-testid="consult-no-model">
            No model wrote this answer: it is the cited passages themselves, at no cost.
          </p>
        )}
      </div>
    );
  };
}

// --- quarantine ---

const quarCss = css({
  display: 'grid',
  gap: t.s2,
  padding: t.s3,
  borderRadius: t.rMd,
  background: t.bgApp,
  border: `1px solid ${t.border}`,
});
const chunkCss = css({
  display: 'grid',
  gap: t.s2,
  padding: t.s3,
  borderRadius: t.rMd,
  border: `1px solid color-mix(in srgb, ${t.warning} 35%, ${t.border})`,
  background: t.bgSurface,
  '& .head': {
    display: 'flex',
    alignItems: 'center',
    gap: t.s2,
    flexWrap: 'wrap',
    fontSize: t.textSm,
  },
});

function QuarantineList(handle: Handle<{ packId: string; onChanged: () => void }>) {
  const s = services(handle);
  const act = action(handle);
  const q = query(handle, {
    load: () => s.client.packQuarantined(handle.props.packId),
    key: () => handle.props.packId,
    when: (e) => e.type.startsWith('pack.'),
  });
  return () => {
    const rows: QuarantinedChunk[] = q.data ?? [];
    return (
      <div mix={quarCss} data-testid="quarantine">
        <p mix={faint}>These passages looked like instructions to an agent and were kept out of search. Only you can release one.</p>
        <ErrorNote error={q.error ?? act.error} />
        {q.data && rows.length === 0 ? <p mix={faint}>Nothing is quarantined.</p> : null}
        {rows.map((c) => (
          <div key={c.chunkId} mix={chunkCss} data-testid="quarantined-chunk">
            <div class="head">
              <Badge tone="warn">risk {c.risk}</Badge>
              <span>{(c.flags ?? []).join(', ')}</span>
              <span mix={muted}>{c.headingPath}</span>
            </div>
            <Markdown source={c.body} maxChars={3000} />
            <div>
              <ConfirmAction
                label="Release this passage to search"
                icon={ShieldAlert}
                variant="secondary"
                yes="Yes, release it"
                yesName={`Yes, release the passage ${passageName(c.headingPath)}`}
                keep="Keep it out"
                keepName={`Keep the passage ${passageName(c.headingPath)} out of search`}
                question={`Release the passage ${passageName(c.headingPath)}? Agents can then find and read it as part of this pack.`}
                disabled={act.busy !== ''}
                testId="pack-release"
                onConfirm={async () => {
                  await act.run('release', () => s.client.packReleaseChunk(c.chunkId));
                  q.reload();
                  handle.props.onChanged();
                }}
              />
            </div>
          </div>
        ))}
      </div>
    );
  };
}

/** Which passage a button speaks of, for its accessible name: the heading it sits under (the pack's own words, so hidden characters are written out and a long chain is cut). */
function passageName(headingPath: string): string {
  const where = visible(headingPath).trim();
  return where ? `under "${where.length > 80 ? `${where.slice(0, 79)}…` : where}"` : 'of this pack';
}

// --- one pack ---

const packCss = css({
  display: 'grid',
  gap: t.s3,
  padding: t.s4,
  border: `1px solid ${t.border}`,
  borderRadius: t.rLg,
  background: t.bgSurface,
  minWidth: '0',
  '& header': {
    display: 'flex',
    alignItems: 'center',
    gap: t.s2,
    flexWrap: 'wrap',
    minWidth: '0',
  },
  '& h3': { fontSize: t.textLg, fontWeight: '600', overflowWrap: 'anywhere' },
  '& .attached': { fontSize: t.textMd },
});
const statsCss = css({
  display: 'grid',
  gridTemplateColumns: 'repeat(auto-fill, minmax(170px, 1fr))',
  gap: t.s3,
  margin: '0',
  padding: t.s3,
  borderRadius: t.rMd,
  background: t.bgApp,
  '& div': { display: 'grid', gap: '2px', minWidth: '0' },
  '& dt': {
    color: t.textMuted,
    fontSize: t.textXs,
    fontWeight: '500',
    textTransform: 'uppercase',
    letterSpacing: '0.04em',
  },
  '& dd': { margin: '0', fontSize: t.textMd, overflowWrap: 'anywhere' },
});
const actionsCss = css({
  display: 'flex',
  alignItems: 'center',
  gap: t.s2,
  flexWrap: 'wrap',
  '& input': { width: '150px' },
});
const packIcon = css({
  display: 'inline-grid',
  placeItems: 'center',
  width: '28px',
  height: '28px',
  borderRadius: t.rMd,
  background: t.accentSoft,
  color: t.accentText,
});

function PackCard(
  handle: Handle<{
    p: Pack;
    projectId: string;
    attached: PackAttachment[];
    onChanged: () => void;
  }>,
) {
  const s = services(handle);
  const act = action(handle);
  let role = '';
  let showQ = false;
  let confirm = false;
  let refreshed: string | undefined;
  const set = (fn: () => void) => {
    fn();
    void handle.update();
  };
  return () => {
    const { p, projectId, attached, onChanged } = handle.props;
    const mine = attached.filter((a) => a.packId === p.id);
    const busy = act.busy !== '';
    return (
      <article mix={packCss} data-testid="pack" data-pack={p.id}>
        <header>
          <span mix={packIcon}>
            <Icon icon={Library} size={15} />
          </span>
          <h3>{p.title}</h3>
          <code>{p.id}</code>
          <Badge>{p.kind}</Badge>
          <Badge tone={p.visibility === 'private' ? 'ok' : 'warn'}>{p.visibility}</Badge>
          <Badge tone={p.status === 'ready' ? 'ok' : 'muted'}>{p.status}</Badge>
          <span mix={spacer} />
          <Button
            size="sm"
            icon={RefreshCw}
            disabled={busy}
            mix={on('click', async () => {
              const r = await act.run('refresh', () => s.client.packRefresh(p.id, true));
              if (r) set(() => (refreshed = refreshText(r)));
              onChanged();
            })}
          >
            {act.busy === 'refresh' ? 'Refreshing...' : 'Refresh now'}
          </Button>
        </header>
        <dl mix={statsCss}>
          <div>
            <dt>Version</dt>
            <dd>{p.version || 'not fetched yet'}</dd>
          </div>
          <div>
            <dt>Content</dt>
            <dd>
              {p.pages} pages, {p.chunks} passages, {p.quarantined} quarantined
            </dd>
          </div>
          <div>
            <dt>Freshness</dt>
            <dd>{freshnessText(p)}</dd>
          </div>
          <div>
            <dt>Last refresh</dt>
            <dd>{zeroTime(p.lastSuccessAt) ? 'never' : formatDateTime(p.lastSuccessAt)}</dd>
          </div>
          {p.manifest?.license?.content ? (
            <div>
              <dt>License</dt>
              <dd>{p.manifest.license.content}</dd>
            </div>
          ) : null}
        </dl>
        {p.lastError ? <ErrorNote error={p.lastError} what="Last refresh failed" /> : null}
        {p.manifestDrift ? <Notice tone="warn">pack.yaml on disk differs from the installed manifest.</Notice> : null}
        <ErrorNote error={act.error} />
        {refreshed ? (
          <Notice tone="ok" role="status">
            {refreshed}
          </Notice>
        ) : null}
        <p class="attached">
          <span mix={muted}>Attached to this project:</span> {attachedText(mine)}
        </p>
        <div mix={actionsCss}>
          <input aria-label={`Role for ${p.id}`} value={role} placeholder="all roles" mix={on('input', (e) => set(() => (role = e.currentTarget.value)))} />
          <Button
            size="sm"
            disabled={busy}
            mix={on('click', async () => {
              await act.run('attach', () => s.client.packAttach(projectId, p.id, '', role.trim()));
              onChanged();
            })}
          >
            Attach to this project
          </Button>
          {mine.length > 0 ? (
            <Button
              size="sm"
              disabled={busy}
              mix={on('click', async () => {
                await act.run('detach', () => s.client.packDetach(projectId, p.id, mine[0]!.agentId ?? '', mine[0]!.role ?? ''));
                onChanged();
              })}
            >
              Detach
            </Button>
          ) : null}
          <Button size="sm" variant="ghost" icon={ShieldAlert} aria-expanded={showQ ? 'true' : 'false'} mix={on('click', () => set(() => (showQ = !showQ)))}>
            Quarantined ({p.quarantined})
          </Button>
          <span mix={spacer} />
          {confirm ? (
            <>
              <Button
                size="sm"
                variant="danger"
                disabled={busy}
                mix={on('click', async () => {
                  await act.run('remove', () => s.client.packRemove(p.id));
                  onChanged();
                })}
              >
                Yes, remove {p.id}
              </Button>
              <Button size="sm" variant="ghost" mix={on('click', () => set(() => (confirm = false)))}>
                Keep
              </Button>
            </>
          ) : (
            <Button size="sm" variant="ghost" icon={Trash2} mix={on('click', () => set(() => (confirm = true)))}>
              Remove
            </Button>
          )}
        </div>
        {hasOwnFiles(p) ? <PackFiles packId={p.id} onChanged={onChanged} /> : null}
        {showQ ? <QuarantineList packId={p.id} onChanged={onChanged} /> : null}
      </article>
    );
  };
}

// --- a pack's own files ---

const fileInputCss = css({ minHeight: '28px' });

const filesCss = css({
  display: 'grid',
  gap: t.s2,
  padding: t.s3,
  borderRadius: t.rMd,
  background: t.bgApp,
  border: `1px solid ${t.border}`,
  '& ul': { display: 'grid', gap: t.s1, margin: '0', padding: '0', listStyle: 'none' },
  '& li': { display: 'flex', alignItems: 'center', gap: t.s2, flexWrap: 'wrap', minWidth: '0', fontSize: t.textMd },
  '& li code': { overflowWrap: 'anywhere' },
});

/** Reads the files a person picked, after checking their names and sizes. */
async function readPicked(list: FileList | null): Promise<{ files: PackUpload[]; error: string }> {
  const picked = Array.from(list ?? []);
  const error = checkPackFiles(picked);
  if (error) return { files: [], error };
  const files: PackUpload[] = [];
  for (const f of picked) files.push({ name: f.name, data: new Uint8Array(await f.arrayBuffer()) });
  return { files, error: '' };
}

/** The files of a pack made from files: add more, remove one; each change is indexed at once (a refresh). */
function PackFiles(handle: Handle<{ packId: string; onChanged: () => void }>) {
  const s = services(handle);
  const act = action(handle);
  const q = query(handle, {
    load: () => s.client.packFiles(handle.props.packId),
    key: () => handle.props.packId,
    when: (e) => e.type === 'pack.files_changed',
  });
  let pickError = '';
  let done = '';
  let input: HTMLInputElement | undefined;
  const set = (fn: () => void) => {
    fn();
    void handle.update();
  };
  const reindex = async (what: string) => {
    const r = await act.run('refresh', () => s.client.packRefresh(handle.props.packId));
    if (r) set(() => (done = `${what} ${refreshText(r)}`));
    q.reload();
    handle.props.onChanged();
  };
  return () => {
    const files: PackFile[] = q.data ?? [];
    const busy = act.busy !== '';
    return (
      <div mix={filesCss} data-testid="pack-files">
        <ErrorNote error={q.error ?? act.error} />
        {q.data && files.length === 0 ? <p mix={faint}>This pack has no files.</p> : null}
        <ul>
          {files.map((f) => (
            <li key={f.name} data-testid="pack-file">
              <code>{f.name}</code>
              <span mix={muted}>{fileSize(f.size)}</span>
              <span mix={spacer} />
              <ConfirmAction
                label={`Remove ${f.name}`}
                icon={Trash2}
                variant="ghost"
                yes={`Yes, remove ${f.name}`}
                keep="Keep it"
                question={`Remove ${f.name} from this pack? Its passages leave search when the pack is read again, which happens now.`}
                disabled={busy}
                testId="pack-file-remove"
                onConfirm={async () => {
                  const ok = await act.run('remove', async () => (await s.client.packRemoveFile(handle.props.packId, f.name), true));
                  if (ok) await reindex(`Removed ${f.name}.`);
                }}
              />
            </li>
          ))}
        </ul>
        <div mix={actionsCss}>
          <input
            type="file"
            multiple
            hidden
            accept={PACK_FILE_ACCEPT}
            data-testid="pack-file-input"
            mix={[
              ref((n) => (input = n as HTMLInputElement)),
              on('change', async (e) => {
                const el = e.currentTarget as HTMLInputElement;
                const { files: picked, error } = await readPicked(el.files);
                el.value = '';
                set(() => ((pickError = error), (done = '')));
                if (error) return;
                const ok = await act.run('add', () => s.client.packAddFiles(handle.props.packId, picked));
                if (ok) await reindex(`Added ${picked.length === 1 ? picked[0]!.name : `${picked.length} files`}.`);
              }),
            ]}
          />
          <Button size="sm" icon={FilePlus} disabled={busy} aria-label={`Add files to ${handle.props.packId}`} mix={on('click', () => input?.click())}>
            {act.busy === 'add' || act.busy === 'refresh' ? 'Adding...' : 'Add files'}
          </Button>
        </div>
        {pickError ? <ErrorNote error={pickError} /> : null}
        {done ? (
          <Notice tone="ok" role="status">
            {done}
          </Notice>
        ) : null}
      </div>
    );
  };
}

/** Makes a private pack of the files a person picks, then reads them (the first refresh). */
function FilesPackForm(handle: Handle<{ onMade: () => void }>) {
  const s = services(handle);
  const act = action(handle);
  let title = '';
  let id = '';
  let idEdited = false;
  let picked: PackUpload[] = [];
  let pickError = '';
  let idError = '';
  let done = '';
  let form: HTMLFormElement | undefined;
  let filesInput: HTMLInputElement | undefined;
  const set = (fn: () => void) => {
    fn();
    void handle.update();
  };
  // a field the page refused gets the keyboard, as every form here does
  const focusInvalid = () => handle.queueTask(() => form?.querySelector<HTMLElement>('[aria-invalid="true"]')?.focus());
  return () => {
    const busy = act.busy !== '';
    const effectiveId = idEdited ? id : packIdFromTitle(title);
    return (
      <form
        mix={[
          card,
          formCss,
          ref((n) => (form = n as HTMLFormElement)),
          on('submit', async (e) => {
            e.preventDefault();
            if (busy) return;
            if (!validPackId(effectiveId)) {
              set(() => (idError = 'The id is 2 to 40 lowercase letters, digits and hyphens, starting with a letter.'));
              focusInvalid();
              return;
            }
            const err = checkPackFiles(picked.map((f) => ({ name: f.name, size: f.data.length })));
            if (err) {
              set(() => (pickError = err));
              focusInvalid();
              return;
            }
            const made = await act.run('make', () => s.client.packCreateFromFiles({ id: effectiveId, title: title.trim() || effectiveId }, picked));
            if (!made) return;
            const r = await act.run('refresh', () => s.client.packRefresh(made.id));
            set(() => {
              done = r ? `Made ${made.id}. ${refreshText(r)}` : `Made ${made.id}; reading its files failed (see the pack).`;
              title = '';
              id = '';
              idEdited = false;
              picked = [];
              if (filesInput) filesInput.value = '';
            });
            handle.props.onMade();
          }),
        ]}
        data-testid="files-pack-form"
      >
        <div mix={inlineForm}>
          <Field label="Title">
            {(fid) => <input id={fid} value={title} placeholder="Operations handbook" mix={on('input', (e) => set(() => ((title = e.currentTarget.value), (done = ''))))} />}
          </Field>
          <Field label="Pack id" error={idError} hint="Made from the title; agents and citations name the pack by it.">
            {(fid) => (
              <input
                id={fid}
                value={effectiveId}
                {...errorAttrs(fid, idError)}
                mix={on('input', (e) => set(() => ((id = e.currentTarget.value), (idEdited = true), (idError = ''))))}
              />
            )}
          </Field>
        </div>
        <Field label="Files" error={pickError} hint={picked.length ? picked.map((f) => `${f.name} (${fileSize(f.data.length)})`).join(', ') : 'Up to 50 files; a PDF or DOCX up to 32 MB.'}>
          {(fid) => (
            <input
              id={fid}
              type="file"
              multiple
              accept={PACK_FILE_ACCEPT}
              {...errorAttrs(fid, pickError)}
              mix={[
                fileInputCss,
                ref((n) => (filesInput = n as HTMLInputElement)),
                on('change', async (e) => {
                  const el = e.currentTarget as HTMLInputElement;
                  const { files, error } = await readPicked(el.files);
                  set(() => ((picked = files), (pickError = error), (done = '')));
                }),
              ]}
            />
          )}
        </Field>
        <ErrorNote error={act.error} />
        {done ? (
          <Notice tone="ok" role="status">
            {done}
          </Notice>
        ) : null}
        <div mix={formActions}>
          <Button type="submit" variant="primary" icon={Upload} disabled={busy || picked.length === 0}>
            {act.busy === 'make' ? 'Making the pack...' : act.busy === 'refresh' ? 'Reading the files...' : 'Make the pack'}
          </Button>
        </div>
      </form>
    );
  };
}

// --- the screen ---

const formCss = css({ display: 'grid', gap: t.s3 });
const inlineForm = css({
  display: 'flex',
  alignItems: 'flex-end',
  gap: t.s2,
  flexWrap: 'wrap',
  '& > div': { flex: '1', minWidth: '240px' },
});
const choiceCss = css({
  display: 'flex',
  flexWrap: 'wrap',
  gap: `${t.s2} ${t.s4}`,
  '& label': { margin: '0' },
});
const yamlCss = css({ fontFamily: t.mono, fontSize: t.textSm });

export function PacksScreen(handle: Handle) {
  const s = services(handle);
  watch(handle, s.workspace);
  const act = action(handle);
  const pid = () => s.workspace.projectId;
  const packs = query(handle, {
    load: () => s.client.packList(),
    when: (e) => e.type.startsWith('pack.'),
  });
  const attachments = query(handle, {
    load: () => s.client.packAttachments(pid()!),
    when: (e) => e.type.startsWith('pack.'),
    key: pid,
    enabled: () => !!pid(),
  });
  let yaml = '';
  let search = '';
  let question = '';
  let selected: string[] = [];
  let result: PackSearchResult | undefined;
  let consult: PackConsult | undefined;
  const set = (fn: () => void) => {
    fn();
    void handle.update();
  };
  const reload = () => {
    packs.reload();
    attachments.reload();
  };

  return () => {
    const list = packs.data ?? [];
    const ids = packIds(selected, list);
    const busy = act.busy !== '';
    const projectId = pid() ?? '';
    return (
      <Page title="Expert packs" icon={BookOpen} subtitle={packs.data ? `${list.length} installed` : undefined} guide="skills">
        <ErrorNote error={packs.error ?? act.error} what="Packs" />
        {packs.loading && !packs.data ? <Loading /> : null}
        {packs.data && list.length === 0 ? (
          <div mix={card}>
            <Empty title="No packs installed." icon={BookOpen}>
              <p>Install a pack from its manifest below, then refresh it to fetch the text.</p>
            </Empty>
          </div>
        ) : null}
        {list.length > 0 ? (
          <div mix={stack}>
            {list.map((p) => (
              <PackCard key={p.id} p={p} projectId={projectId} attached={attachments.data ?? []} onChanged={reload} />
            ))}
          </div>
        ) : null}

        <Section
          title="Search and ask"
          description="Search returns cited passages. A question is answered from them by the model the router picks (never one that may train on a private pack), or with the passages themselves."
        >
          <div mix={[card, formCss]}>
            {list.length > 0 ? (
              <fieldset>
                <legend>Packs to use (none selected means all)</legend>
                <div mix={choiceCss}>
                  {list.map((p) => (
                    <Checkbox
                      key={p.id}
                      label={p.id}
                      checked={selected.includes(p.id)}
                      onChange={(v) => set(() => (selected = v ? [...selected, p.id] : selected.filter((x) => x !== p.id)))}
                    />
                  ))}
                </div>
              </fieldset>
            ) : null}
            <form
              mix={[
                inlineForm,
                on('submit', async (e) => {
                  e.preventDefault();
                  if (busy || !search.trim() || ids.length === 0) return;
                  const r = await act.run('search', () => s.client.packSearch(ids, search.trim(), 8));
                  set(() => (result = r));
                }),
              ]}
            >
              <Field label="Search">
                {(id) => (
                  <input id={id} type="search" aria-label="Search packs" value={search} mix={on('input', (e) => set(() => (search = e.currentTarget.value)))} />
                )}
              </Field>
              <Button type="submit" icon={Search} disabled={busy || !search.trim() || ids.length === 0}>
                Search
              </Button>
            </form>
            {result ? <SearchResultView r={result} /> : null}
            <form
              mix={[
                inlineForm,
                on('submit', async (e) => {
                  e.preventDefault();
                  if (busy || !question.trim() || ids.length === 0) return;
                  const r = await act.run('consult', () => s.client.packConsult(ids, question.trim()));
                  set(() => (consult = r));
                }),
              ]}
            >
              <Field label="Ask a question">
                {(id) => <input id={id} aria-label="Ask the packs" value={question} mix={on('input', (e) => set(() => (question = e.currentTarget.value)))} />}
              </Field>
              <Button type="submit" icon={MessageSquareText} disabled={busy || !question.trim() || ids.length === 0}>
                {act.busy === 'consult' ? 'Consulting...' : 'Consult'}
              </Button>
            </form>
            {consult ? <ConsultView r={consult} /> : null}
          </div>
        </Section>

        <Section
          title="Make a pack from your files"
          description="PDF, Word (DOCX), Markdown, text or HTML files. The pack is private and is read now; a PDF's passages name their page. A scanned PDF has no text to read: run it through OCR first."
        >
          <FilesPackForm onMade={reload} />
        </Section>

        <Section
          title="Install a pack"
          description="Paste a pack manifest (YAML). A source is a folder, web pages, an llms.txt or sitemap, or a repository's docs (type git_docs: url, ref, path). Refresh fetches it."
        >
          <form
            mix={[
              card,
              formCss,
              on('submit', async (e) => {
                e.preventDefault();
                if (busy || !yaml.trim()) return;
                const r = await act.run('install', () => s.client.packInstall(yaml));
                if (r) {
                  set(() => (yaml = ''));
                  reload();
                }
              }),
            ]}
          >
            <Field label="Pack manifest (YAML)">
              {(id) => (
                <textarea
                  id={id}
                  rows={8}
                  spellcheck="false"
                  placeholder={'schema: 1\nid: godocs\ntitle: Go docs\nkind: indexed\nvisibility: private\nsources:\n  - {type: local_dir, path: "C:/docs"}'}
                  value={yaml}
                  mix={[yamlCss, on('input', (e) => set(() => (yaml = e.currentTarget.value)))]}
                />
              )}
            </Field>
            <div mix={formActions}>
              <Button type="submit" variant="primary" icon={Download} disabled={busy || !yaml.trim()}>
                Install pack
              </Button>
            </div>
          </form>
        </Section>
      </Page>
    );
  };
}
