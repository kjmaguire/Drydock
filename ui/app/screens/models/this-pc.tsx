// Models on this PC (decisions P65, P97, P98): the "this-pc" detail of the Models page (/models/this-pc). The engine (a
// llama.cpp build) is part of Drydock and never named on the page (decision P95): Drydock sets the TurboQuant build up itself
// (from the download, else from GitHub, checked against the digest GitHub recorded); a person finds GGUF models on the Hugging
// Face Hub (the search above the model table, "/" goes to it), downloads them (checked against the Hub's SHA-256) and serves
// one; the server is then a provider the core registers ("Models on this PC"), which is this row of the page, not a second one.
// The detail is drawn from settings groups: Server (the model, its address, the agents that use it, its KV cache and context,
// when it started; Stop, Switch model, Log), Memory (each GPU split into the model, its KV cache and the rest, then the RAM and
// the disk), Engine (the build and backend, Check for updates / Update to <tag>), Models (name and quantization, repository,
// size, where it fits, the action; the Hub), then the server settings. Installs and downloads run in the background: their
// progress lives in the view (local.get, read by the page), which the event local.changed re-reads; methods that change things
// answer with the fresh view, shown at once (setView).

import { css, on, ref, type Handle } from 'remix/component';
import { ChevronDown, ChevronRight, Cpu, Search } from 'lucide';
import type { LocalBackend, LocalJob, LocalModel, LocalSettingsEdit, LocalView, Repo, Variant } from '../../api/localClient.ts';
import { formatDate } from '../../lib/format.ts';
import { typing } from '../../lib/keys.ts';
import { routes } from '../../routes.ts';
import { action, services } from '../../state/services.tsx';
import { Button } from '../../ui/button.tsx';
import { Badge, mono, muted } from '../../ui/data.tsx';
import { ErrorNote, Loading, Notice } from '../../ui/feedback.tsx';
import { Disclosure } from '../../ui/disclosure.tsx';
import { Checkbox } from '../../ui/form.tsx';
import { Icon } from '../../ui/icon.tsx';
import { LearnMore } from '../../ui/learn-more.tsx';
import { DetailHeader, SettingRow, SettingsGroup } from '../../ui/master-detail.tsx';
import { Menu } from '../../ui/menu.tsx';
import { Select, selectValues } from '../../ui/select.tsx';
import { t, tone, type Tone } from '../../ui/tokens.ts';
import { Meter } from '../budgets/meter.tsx';
import { ConfirmAction } from '../shared/confirm-action.tsx';
import {
  type Bar,
  barLabel,
  compactCount,
  contextWords,
  deviceLine,
  diskLine,
  DRAFT_KINDS,
  draftChoices,
  draftOnly,
  draftWords,
  type FitWords,
  fitWords,
  gb,
  gpus,
  hostPort,
  jobFraction,
  licenceUrl,
  loadedIds,
  loadLine,
  loadToo,
  loadTooTitle,
  localRow,
  memoryBars,
  modelName,
  noGpuWords,
  progressText,
  runtimeLine,
  servedName as servedLabel,
  serverTone,
  splitModelId,
  startedWords,
  unloadTitle,
  switchChoices,
  tokenWords,
  usedByWords,
  usedOf,
  wholeNumber,
} from './models.ts';

/** The build Drydock sets up (decision P95): the TurboQuant one, with Google's TurboQuant KV cache types. */
const TURBOQUANT = 'turboquant';
const RESTART_FIELDS = new Set(['cacheType', 'contextSize', 'port']);
const STATE_WORDS: Record<string, string> = { stopped: 'stopped', starting: 'starting', running: 'running', failed: 'failed' };

const detailCss = css({ display: 'grid', gap: t.s5, minWidth: '0' });
const searchCss = css({
  display: 'flex',
  alignItems: 'center',
  gap: t.s2,
  width: '340px',
  maxWidth: '100%',
  height: '30px',
  padding: '0 10px',
  border: `1px solid ${t.border}`,
  borderRadius: t.rMd,
  background: t.bgInput,
  color: t.textFaint,
  '&:focus-within': { borderColor: t.borderStrong },
  '& input': { flex: '1', minWidth: '0', height: '28px', padding: '0', border: '0', outline: '0', background: 'transparent', color: t.text, font: 'inherit', fontSize: t.textMd },
  '& input:focus-visible': { outline: '0', boxShadow: 'none' },
  '& .busy': { fontSize: t.textXs },
});
// Memory and Engine side by side on a wide detail, one under the other on a narrow one
const topCss = css({ display: 'grid', gridTemplateColumns: 'repeat(auto-fit, minmax(320px, 1fr))', gap: t.s5, alignItems: 'start', minWidth: '0' });
// what a group holds besides its rows (a bar, a note, the log), inside the group's surface
// the engine's own lines, wrapped: a driver path is long
const noteCss = css({ margin: '0', whiteSpace: 'pre-wrap', overflowWrap: 'anywhere', fontSize: t.textSm });
const blockCss = css({
  display: 'grid',
  gap: t.s2,
  padding: `${t.s3} ${t.s4}`,
  minWidth: '0',
  '&:not(:first-child)': { borderTop: `1px solid ${t.border}` },
  '& p': { margin: '0' },
  '& .note': { color: t.textFaint, fontSize: t.textXs, lineHeight: '1.5' },
  '& pre': { margin: '0', maxHeight: '220px', overflow: 'auto', padding: t.s2, borderRadius: t.rMd, background: t.bgApp, fontSize: t.textXs },
});
const titleCss = css({ display: 'inline-flex', alignItems: 'center', gap: t.s2, minWidth: '0', fontWeight: '600', overflowWrap: 'anywhere' });
const lineCss = css({ display: 'grid', gap: '2px', minWidth: '0', '& .note': { color: t.textFaint, fontSize: t.textXs, lineHeight: '1.5' } });
const addrCss = css({ color: t.textFaint, fontSize: t.textXs });
const dotCss = css({ width: '8px', height: '8px', borderRadius: '50%', flex: 'none' });
const barCss = css({
  display: 'flex',
  flexDirection: 'column',
  gap: '6px',
  '& .top': { display: 'flex', justifyContent: 'space-between', gap: t.s2, color: t.textMuted, fontSize: t.textXs },
  '& .track': { display: 'flex', height: '10px', borderRadius: '5px', background: t.bgActive, overflow: 'hidden' },
  '& .legend': { display: 'flex', flexWrap: 'wrap', gap: '14px', color: t.textFaint, fontSize: t.textXs },
  '& .legend > span': { display: 'inline-flex', alignItems: 'center', gap: '6px' },
  '& .swatch': { width: '9px', height: '9px', borderRadius: '2px' },
});
const PART_COLOR: Record<'model' | 'kv' | 'other', string> = { model: t.success, kv: t.successSolid, other: t.textFaint };

// the model table: Model (name and quantization over the repository), Size, On this PC (the fit), the action
const tableCss = css({
  minWidth: '0',
  border: `1px solid ${t.border}`,
  borderRadius: t.rLg,
  background: t.bgSurface,
  overflowX: 'auto',
  '& ul': { margin: '0', padding: '0', listStyle: 'none' },
  '& .m': {
    display: 'grid',
    gridTemplateColumns: 'minmax(220px, 1fr) 90px 150px minmax(120px, auto)',
    alignItems: 'center',
    gap: t.s3,
    padding: '11px 16px',
    borderTop: `1px solid ${t.border}`,
  },
  '& li.m:hover': { background: t.bgHover },
  '& .m.th': { borderTop: '0', color: t.textFaint, fontSize: t.textXs, fontWeight: '500' },
  '& .m[data-served="true"]': { background: t.bgHover },
  '& .m[data-offered="false"]': { opacity: '0.75' },
  '& .who': { display: 'flex', flexDirection: 'column', gap: '2px', minWidth: '0' },
  '& .name': { fontWeight: '500', overflowWrap: 'anywhere' },
  '& .quant': { color: t.textFaint, fontWeight: '400' },
  '& .repo': { color: t.textFaint, fontFamily: t.mono, fontSize: t.textXs, overflowWrap: 'anywhere' },
  '& .tags': { display: 'flex', flexWrap: 'wrap', gap: '4px' },
  '& .tags:empty': { display: 'none' },
  '& .size': { color: t.textMuted, fontFamily: t.mono, fontSize: t.textXs },
  '& .act': { display: 'flex', alignItems: 'center', justifyContent: 'flex-end', gap: t.s2, flexWrap: 'wrap' },
  '& .act > .word': { color: t.textFaint, fontSize: t.textXs },
  '& .what': { color: t.textMuted, fontSize: t.textXs, overflowWrap: 'anywhere' },
  '& .wide': { gridColumn: '2 / 4', display: 'grid', gap: '4px', minWidth: '0' },
  '& .group, & .foot': { display: 'flex', alignItems: 'center', gap: t.s2, flexWrap: 'wrap', padding: '8px 16px', borderTop: `1px solid ${t.border}`, color: t.textMuted, fontSize: t.textXs },
  '& .group .grow, & .foot .grow': { marginRight: 'auto' },
  '& .empty': { margin: '0', padding: '11px 16px', borderTop: `1px solid ${t.border}`, color: t.textMuted },
});
const repoBtn = css({
  gridColumn: '1 / -1',
  display: 'flex',
  alignItems: 'center',
  justifyContent: 'flex-start',
  gap: t.s2,
  flexWrap: 'wrap',
  width: '100%',
  minHeight: '28px',
  padding: '0',
  border: '0',
  background: 'transparent',
  color: t.text,
  textAlign: 'left',
  font: 'inherit',
  cursor: 'pointer',
  '& .count': { color: t.textFaint, fontSize: t.textXs },
});
const fitCss = css({
  display: 'inline-flex',
  alignItems: 'center',
  justifyContent: 'center',
  justifySelf: 'start',
  height: '22px',
  padding: '0 8px',
  borderRadius: t.rSm,
  fontSize: t.textXs,
  fontWeight: '600',
  whiteSpace: 'nowrap',
});
const numberCss = css({ width: '120px' });
const tokenFormCss = css({ display: 'flex', alignItems: 'center', gap: t.s2, '& input': { width: '220px', maxWidth: '100%' } });


/** The fit badge: its short words, the sentence as its tooltip. */
function FitBadge(handle: Handle<{ fit: FitWords | undefined }>) {
  return () => {
    const f = handle.props.fit;
    if (!f) return null;
    const c = tone[f.tone];
    return (
      <span mix={fitCss} data-testid="local-fit" data-tone={f.tone} title={f.long} style={{ color: c.fg, background: c.bg }}>
        {f.text}
      </span>
    );
  };
}

/** A memory bar: the name and "x of y GB" over the track, its parts in colour, and the legend of the parts when a model is in it. */
function MemoryBar(handle: Handle<{ name: string; title?: string; bar: Bar; legend: boolean; kvNote?: string; testId: string; id?: string }>) {
  return () => {
    const { name, title, bar, legend, kvNote, testId, id } = handle.props;
    const pct = (b: number) => (bar.total > 0 ? `${Math.round((b / bar.total) * 1000) / 10}%` : '0%');
    return (
      <div mix={barCss} data-testid={testId} data-id={id}>
        <div class="top">
          <span title={title}>{name}</span>
          <span mix={mono} data-testid="local-bar-used">
            {usedOf(bar.used, bar.total)}
          </span>
        </div>
        <div class="track" role="img" aria-label={barLabel(name, bar)}>
          {bar.parts.map((p) => (
            <span key={p.kind} data-part={p.kind} style={{ width: pct(p.bytes), background: PART_COLOR[p.kind] }} />
          ))}
        </div>
        {legend ? (
          <div class="legend">
            <span>
              <span class="swatch" style={{ background: PART_COLOR.model }} />
              model
            </span>
            <span title={kvNote}>
              <span class="swatch" style={{ background: PART_COLOR.kv }} />
              {kvNote ? 'KV cache not estimated' : 'KV cache'}
            </span>
          </div>
        ) : null}
      </div>
    );
  };
}

/**
 * The detail. The page reads the view (it also draws the row from it) and hands it down: `view` is the last answer of local.get,
 * `setView` shows an action's own answer at once.
 */
export function ThisPcDetail(
  handle: Handle<{ view?: LocalView; loading: boolean; error?: unknown; setView: (v: LocalView) => void; /** "now" for the Started line (tests). */ now?: number }>,
) {
  const s = services(handle);
  const act = action(handle);
  // Stop has an action of its own: a start waits for the core until the model is loaded (up to 15 minutes) and holds `act` all
  // that time, and Stop has to work meanwhile (audit 2026-10-16, L44)
  const stopAct = action(handle);
  const ids = {
    cache: `local-cache-${handle.id}`,
    context: `local-context-${handle.id}`,
    port: `local-port-${handle.id}`,
    embedPort: `local-embed-port-${handle.id}`,
    token: `local-hf-token-${handle.id}`,
  };
  // the Hugging Face token being typed (decision P111): sent once on Save, never kept on the page after
  let tokenText = '';
  let showLog = false;
  let restartNeeded = false; // a cache, context or port change while the server ran
  // Stop was pressed while a start was waiting: the core answers that start with an error ("the server was stopped before
  // llama-server was ready"), which is what the person asked for, so the answer is dropped and a calm note is shown
  let cancelledStart = false;
  let stoppedNote = false;
  // the Hub: suggestions and search results, the one repository open and its variants
  let suggested: Repo[] | undefined;
  let results: Repo[] | undefined;
  let searched = '';
  let searchText = '';
  let openRepo = '';
  let variants: Variant[] | undefined;
  let hubInput: HTMLInputElement | undefined;
  const redraw = () => void handle.update();

  // "/" goes to the Hub search, from anywhere but a field (the mockup's hint in the box)
  window.addEventListener(
    'keydown',
    (e) => {
      if (e.key !== '/' || typing(e) || !hubInput) return;
      e.preventDefault();
      hubInput.focus();
    },
    { signal: handle.signal },
  );

  // an action that answers with the view: shown at once, no second read
  const change = async (label: string, fn: () => Promise<LocalView | undefined>) => {
    stoppedNote = false;
    const v = await act.run(label, fn);
    if (v) handle.props.setView(v);
    return v;
  };
  // Serve: the core answers when the model is loaded. A Stop pressed meanwhile ends that start, and its answer is dropped. Several
  // models (Load too, Unload: decision P111) are kept loaded together by the router, which loads or unloads the one model while
  // the others keep running (decision P120); a server of one model starts again in router mode.
  const serve = (modelId: string) => serveSet([modelId], `serve:${modelId}`);
  const serveSet = (modelIds: string[], label: string) => {
    cancelledStart = false;
    return change(label, async () => {
      let view: LocalView;
      try {
        view = modelIds.length === 1 ? await s.client.localServerStart(modelIds[0]!) : await s.client.localServerStartMany(modelIds);
      } catch (e) {
        if (cancelledStart) return undefined; // stopped on purpose: the start was asked to end, so its error is no failure
        throw e;
      }
      // ready just before the Stop reached the core: the Stop's own answer says what the server is now, not this one
      return cancelledStart ? undefined : view;
    });
  };
  // Stop / Cancel start. While a start waits, the core stops the server it is loading and answers that start with an error.
  const stop = async () => {
    const cancelling = act.busy.startsWith('serve:');
    if (cancelling) cancelledStart = true;
    stoppedNote = false;
    const v = await stopAct.run('stop', () => s.client.localServerStop());
    if (v) {
      handle.props.setView(v);
      stoppedNote = cancelling;
    } else if (cancelling) {
      cancelledStart = false; // the Stop did not go through: the start goes on, and its answer counts after all
    }
    redraw();
  };
  const install = (src: string, be: LocalBackend | '') => change('install', () => s.client.localInstall(src, be));
  // the newest build now, instead of at the next twice-daily check (decision P77); a newer one is installed by the core
  let checked = false;
  const checkUpdates = async () => {
    const v = await change('check-updates', () => s.client.localCheckUpdates());
    if (v) checked = true;
    redraw();
  };
  const setSetting = async (e: LocalSettingsEdit) => {
    const state = handle.props.view?.server.state;
    const running = state === 'running' || state === 'starting';
    const v = await change('settings', () => s.client.localSettingsSet(e));
    if (v && running && Object.keys(e).some((k) => RESTART_FIELDS.has(k))) restartNeeded = true;
    redraw();
  };
  const open = async (repo: Repo) => {
    if (openRepo === repo.id) {
      openRepo = '';
      variants = undefined;
      redraw();
      return;
    }
    openRepo = repo.id;
    variants = undefined;
    redraw();
    const vs = await act.run(`variants:${repo.id}`, () => s.client.localModelVariants(repo.id));
    if (vs && openRepo === repo.id) variants = vs;
    redraw();
  };
  const search = async () => {
    const text = searchText.trim();
    if (!text || act.busy !== '' || stopAct.busy !== '') return;
    const r = await act.run('search', () => s.client.localModelsSearch(text));
    if (r) {
      results = r;
      searched = text;
      openRepo = '';
      variants = undefined;
    }
    redraw();
  };
  const suggest = async () => {
    const r = await act.run('suggested', () => s.client.localModelsSuggested());
    if (r) {
      suggested = r;
      results = undefined; // the suggestions replace an earlier search, as a search replaces them
      openRepo = '';
      variants = undefined;
    }
    redraw();
  };
  // Close suggestions / Close results: the list and the repository open in it go away, the table shrinks back
  const closeRepos = () => {
    suggested = undefined;
    results = undefined;
    openRepo = '';
    variants = undefined;
    redraw();
  };

  const nameCell = (repo: string, quant: string, extra?: string) => (
    <div class="who">
      <span class="name">
        {modelName(repo)}
        {quant ? <span class="quant"> · {quant}</span> : null}
      </span>
      <span class="repo">{repo}</span>
      {extra ? <span class="what">{extra}</span> : null}
    </div>
  );

  const jobRow = (job: LocalJob, busy: boolean) => {
    const f = jobFraction(job);
    const { repo, variant } = splitModelId(job.target);
    return (
      <li key={job.target} class="m" data-testid="local-download-row" data-id={job.target} data-state={job.state}>
        {nameCell(repo, variant)}
        {job.state === 'running' ? (
          <div class="wide">
            <Meter value={f} label={`${job.target}: ${progressText(job)}`} />
            <span class="what">
              {progressText(job)}
              {job.file ? ` · ${job.file}` : ''}
            </span>
          </div>
        ) : (
          <>
            <span class="size">{job.total > 0 ? gb(job.total) : ''}</span>
            {job.state === 'failed' ? (
              <span class="what">
                {job.error || 'failed'}
                {job.licence ? (
                  <>
                    {' '}
                    <a href={job.licence} target="_blank" rel="noopener noreferrer" data-rmx-document data-testid="local-licence">
                      Accept the licence on Hugging Face
                    </a>
                  </>
                ) : null}
              </span>
            ) : (
              <span mix={fitCss} data-tone={job.state === 'done' ? 'ok' : 'muted'} style={{ color: tone[job.state === 'done' ? 'ok' : 'muted'].fg, background: tone[job.state === 'done' ? 'ok' : 'muted'].bg }}>
                {job.state === 'done' ? 'downloaded' : job.state}
              </span>
            )}
          </>
        )}
        <span class="act">
          <Button
            size="sm"
            variant="ghost"
            disabled={busy}
            mix={on('click', () => void change(`cancel:${job.target}`, () => s.client.localModelDownloadCancel(job.target)))}
          >
            {job.state === 'running' ? 'Cancel' : 'Clear'}
          </Button>
        </span>
      </li>
    );
  };

  return () => {
    const { view: v, loading, error } = handle.props;
    const busy = act.busy !== '' || stopAct.busy !== '';
    const row = v ? localRow(v) : undefined;

    return (
      <section mix={detailCss} data-testid="local-models" aria-label="Models on this PC">
        <DetailHeader
          title="Models on this PC"
          icon={Cpu}
          status={row ? { text: row.text, tone: row.tone === 'none' ? 'muted' : row.tone } : undefined}
          sub={
            <>
              Private: prompts and code never leave this computer. <LearnMore topic="local-models" />
            </>
          }
          testId="local-head"
        />
        <ErrorNote error={error ?? act.error ?? stopAct.error} what="Models on this PC" />
        {stoppedNote ? (
          <Notice tone="info" role="status" testId="local-stopped">
            The server was stopped.
          </Notice>
        ) : null}
        {!v ? (
          loading ? <Loading /> : null
        ) : !v.supported ? (
          // a PC where local models cannot run (not Windows): the reason only; the rest of the view may be absent
          <SettingsGroup title="Engine" testId="local-runtime">
            <div mix={blockCss}>
              <p>{v.reason || 'Local models are not available here.'}</p>
            </div>
          </SettingsGroup>
        ) : (
          body(v, busy)
        )}
      </section>
    );
  };

  function body(v: LocalView, busy: boolean) {
    const label = (id: string) => v.backends.find((b) => b.id === id)?.label ?? id;
    const job = v.runtime.job;
    const installing = job?.state === 'running';
    const src = v.sources.find((x) => x.id === TURBOQUANT);
    const chosenBackend: LocalBackend | '' = v.settings.backend || v.suggestedBackend;
    const server = v.server;
    const serverOn = server.state === 'running' || server.state === 'starting';
    if (!serverOn) restartNeeded = false;
    const installedSrc = v.sources.find((x) => x.id === v.runtime.source);
    const cacheTypes = (v.runtime.installed ? installedSrc : src)?.cacheTypes ?? [];
    // a start of this window is waiting for the core, or the core says the server is starting (another window, the autostart)
    const serving = act.busy.startsWith('serve');
    const starting = server.state === 'starting' || serving;
    // Stop was pressed for that start: it only has to deliver its answer now (Serve waits for it; Stop is not offered twice)
    const ending = serving && cancelledStart;
    const repos = results ?? suggested;
    const hasGpu = gpus(v.runtime.devices.devices).length > 0;
    const servedName = server.modelId ? modelName(splitModelId(server.modelId).repo) : '';
    const bars = memoryBars(v);
    const kv = v.served?.kv;
    const cacheNow = serverOn ? kv?.cacheType || server.cacheType || v.settings.cacheType : v.settings.cacheType;
    const ctxNow = serverOn ? kv?.contextSize || server.contextSize || 0 : v.settings.contextSize;
    const started = server.state === 'running' ? startedWords(server.startedAt, server.autostarted, handle.props.now) : '';
    // the models the server keeps loaded (decision P111): several run the router, each its own model of the provider
    const loaded = loadedIds(server);
    const several = loaded.length > 1;
    const title =
      server.state === 'running'
        ? several
          ? `Serving ${loaded.length} models`
          : `Serving ${servedName}`
        : server.state === 'starting' || serving
          ? several
            ? `Starting ${loaded.length} models`
            : `Starting ${servedName || 'the server'}`
          : server.state === 'failed'
            ? 'The server failed'
            : 'Nothing served';
    const choices = switchChoices(
      v.models.filter((m) => !m.embedding),
      serverOn ? server.modelId : undefined,
    );
    const downloading = new Set(v.downloads.filter((d) => d.state === 'running').map((d) => d.target));
    const have = new Set(v.models.map((m) => m.id));
    const turbo = installedSrc ? installedSrc.cacheTypes.filter((c) => c.startsWith('turbo')) : [];
    const embed = v.embed ?? { state: 'stopped' as const };
    const embedOn = embed.state === 'running' || embed.state === 'starting';
    const embedModels = v.models.filter((m) => m.embedding);
    const token = v.hfToken ?? { set: false };

    const modelRow = (m: LocalModel) => {
      const isServed = loaded.includes(m.id);
      const forEmbeddings = embedOn && embed.modelId === m.id;
      const words = fitWords(m.fit, m.sizeBytes, { gpu: hasGpu, serving: isServed || forEmbeddings });
      // the model the server is loading: "Starting · GPU" until it is ready
      const fit = words && isServed && server.state === 'starting' ? { ...words, text: words.text.replace(/^Serving/, 'Starting') } : words;
      const only = draftOnly(m);
      const too = serverOn && !isServed && !m.embedding && !only ? loadToo(v, m) : undefined;
      const drafts = draftChoices(m, v.models);
      const drafted = draftWords(m, v.models);
      // loading beside the models the router keeps (decision P120)
      const loadingHere = (server.loading ?? []).includes(m.id);
      return (
        <li key={m.id} class="m" data-testid="local-model" data-id={m.id} data-served={isServed || forEmbeddings ? 'true' : undefined}>
          <div class="who">
            <span class="name">
              {modelName(m.repo)}
              <span class="quant"> · {m.variant}</span>
            </span>
            <span class="repo">{m.repo}</span>
            <span class="tags">
              {m.projector ? (
                <Badge testId="local-vision" title="Downloaded with its projector (mmproj): the model reads images too.">
                  vision
                </Badge>
              ) : null}
              {m.embedding ? (
                <Badge testId="local-embedding" title="Makes embeddings: served on its own, under Embeddings, never for chat.">
                  embeddings
                </Badge>
              ) : null}
              {m.mtp ? (
                <Badge testId="local-mtp" title="Carries its own multi-token prediction (MTP) heads: choose them as its draft to make it faster.">
                  MTP
                </Badge>
              ) : null}
              {only ? (
                <Badge testId={only === 'eagle3' ? 'local-eagle3' : 'local-draft-only'} title={DRAFT_KINDS[only].title}>
                  {DRAFT_KINDS[only].badge}
                </Badge>
              ) : null}
              {drafted ? <Badge testId="local-drafted">{drafted}</Badge> : null}
              {loadingHere ? <Badge testId="local-loading">loading</Badge> : null}
            </span>
            {!m.embedding && !only && drafts.length > 1 ? (
              <span class="what">
                <Select
                  size="sm"
                  testId="local-draft"
                  label={`Draft model for ${m.id}`}
                  title="Speculative decoding: a smaller model of the same family, a draft made for the model (EAGLE-3, DFlash, DSpark, MTP heads) or its own MTP heads guess ahead and the model checks, which is faster. Takes effect at the next start."
                  value={m.draft ?? ''}
                  disabled={busy}
                  options={drafts}
                  onChange={(d) => void change(`draft:${m.id}`, () => s.client.localModelSet(m.id, { draft: d }))}
                />
              </span>
            ) : null}
            {!only ? (
              <span class="what">
                <Checkbox
                  label="Embedding model"
                  checked={!!m.embedding}
                  disabled={busy || isServed || forEmbeddings}
                  onChange={(c) => void change(`kind:${m.id}`, () => s.client.localModelSet(m.id, { embedding: c }))}
                />
              </span>
            ) : null}
          </div>
          <span class="size">{gb(m.sizeBytes)}</span>
          <FitBadge fit={fit} />
          <span class="act">
            {m.embedding ? (
              forEmbeddings ? (
                <span class="word">serving embeddings</span>
              ) : (
                <Button
                  size="sm"
                  data-testid="local-embed-serve"
                  disabled={busy || !v.runtime.installed}
                  title={v.runtime.installed ? undefined : 'The engine is not set up yet'}
                  mix={on('click', () => void change(`embed:${m.id}`, () => s.client.localEmbeddingsStart(m.id)))}
                >
                  {act.busy === `embed:${m.id}` ? 'Starting…' : 'Serve embeddings'}
                </Button>
              )
            ) : isServed ? (
              several ? (
                <Button
                  size="sm"
                  variant="ghost"
                  data-testid="local-unload"
                  disabled={busy || starting}
                  title={unloadTitle(server)}
                  mix={on('click', () => void serveSet(loaded.filter((id) => id !== m.id), `serve-unload:${m.id}`))}
                >
                  {act.busy === `serve-unload:${m.id}` ? 'Unloading…' : 'Unload'}
                </Button>
              ) : (
                <span class="word">downloaded</span>
              )
            ) : null}
            {!isServed && !forEmbeddings ? (
              <>
                {only ? <span class="word">drafts for its model</span> : null}
                {!m.embedding && !only ? (
                  <Button
                    size="sm"
                    data-testid="local-serve"
                    disabled={busy || starting || !v.runtime.installed}
                    title={v.runtime.installed ? undefined : 'The engine is not set up yet'}
                    mix={on('click', () => void serve(m.id))}
                  >
                    {act.busy === `serve:${m.id}` ? 'Starting…' : 'Serve'}
                  </Button>
                ) : null}
                {too ? (
                  <Button
                    size="sm"
                    variant="ghost"
                    data-testid="local-load-too"
                    disabled={busy || starting || !too.ok}
                    title={too.ok ? loadTooTitle(server) : too.why}
                    mix={on('click', () => void serveSet([...loaded, m.id], `serve-too:${m.id}`))}
                  >
                    {act.busy === `serve-too:${m.id}` ? 'Loading…' : 'Load too'}
                  </Button>
                ) : null}
                <ConfirmAction
                  label="Remove"
                  yes="Yes, remove"
                  yesName={`Yes, remove the model ${m.id}`}
                  keepName={`Keep the model ${m.id}`}
                  question={`Remove the model ${m.id}? ${gb(m.sizeBytes)} are deleted from this PC, and getting it back means downloading it all again.`}
                  disabled={busy}
                  testId="local-model-remove"
                  onConfirm={() => change(`remove:${m.id}`, () => s.client.localModelRemove(m.id))}
                />
              </>
            ) : null}
          </span>
        </li>
      );
    };

    // a variant of a repository; a gated one (decision P111) downloads only with a Hugging Face token of an account that accepted
    // the licence, which the row says plainly with the page to accept it on
    const variantRow = (r: Repo, va: Variant) => {
      const repo = r.id;
      const id = `${repo}:${va.name}`;
      const fit = fitWords(va.fit, va.sizeBytes, { gpu: hasGpu });
      const tooBig = va.fit?.target === 'none';
      const notes = [...(va.notes ?? []), ...(va.projector ? ['Reads images: its projector is downloaded with it.'] : [])].join(' ');
      const gated = !!r.gated;
      return (
        <li key={va.name} class="m" data-testid="local-variant" data-name={va.name} data-offered={tooBig ? 'false' : undefined} data-gated={gated ? 'true' : undefined}>
          <div class="who">
            <span class="name">
              {modelName(repo)}
              <span class="quant"> · {va.quant || va.name}</span>
            </span>
            <span class="repo">{repo}</span>
            {notes ? <span class="what">{notes}</span> : null}
            {gated ? (
              <span class="what" data-testid="local-gated-note">
                {token.set ? 'Gated: Hugging Face shares it once your account has accepted its licence. ' : 'Gated: accept its licence on Hugging Face, then add your Hugging Face token below. '}
                <a href={licenceUrl(repo)} target="_blank" rel="noopener noreferrer" data-rmx-document data-testid="local-licence">
                  Accept the licence
                </a>
              </span>
            ) : null}
          </div>
          <span class="size">{gb(va.sizeBytes)}</span>
          <FitBadge fit={fit} />
          <span class="act">
            {have.has(id) ? (
              <span class="word">downloaded</span>
            ) : downloading.has(id) ? (
              <span class="word">downloading</span>
            ) : tooBig ? (
              // decided by the fit the core sent, not by the badge's words: a model this PC could not load is not offered
              <span class="word" data-testid="local-not-offered" title="Too big for this PC: Drydock does not download a model it could not load.">
                not offered
              </span>
            ) : gated && !token.set ? (
              <span class="word" data-testid="local-needs-token" title="A gated model downloads only with a Hugging Face token (Server settings).">
                needs a token
              </span>
            ) : (
              <Button size="sm" data-testid="local-download" disabled={busy} mix={on('click', () => void change(`download:${va.name}`, () => s.client.localModelDownload(repo, va.name)))}>
                Download
              </Button>
            )}
          </span>
        </li>
      );
    };

    const hub = (
      <form
        mix={[
          searchCss,
          on('submit', (e) => {
            e.preventDefault();
            void search();
          }),
        ]}
        role="search"
        aria-label="Hugging Face"
        data-testid="local-search"
      >
        <Icon icon={Search} size={14} />
        <input
          type="search"
          aria-label="Find a model"
          data-testid="local-search-input"
          placeholder="Find a model on Hugging Face"
          value={searchText}
          spellcheck={false}
          mix={[
            ref((node) => (hubInput = node as HTMLInputElement)),
            on('input', (e) => {
              searchText = e.currentTarget.value;
              redraw();
            }),
          ]}
        />
        {act.busy === 'search' ? (
          <span class="busy" role="status">
            Searching…
          </span>
        ) : (
          <kbd title="Press / to search">/</kbd>
        )}
      </form>
    );

    const stopShown = serverOn || (serving && !ending) || stopAct.busy !== '';
    return (
      <>
        {/* Server: the server, the model it serves, who uses it */}
        <div data-testid="local-server" data-state={server.state}>
          <SettingsGroup
            title="Server"
            actions={
              <>
                {stopShown ? (
                  <Button
                    size="sm"
                    data-testid="local-server-stop"
                    // while the model loads, the start holds the page for up to 15 minutes: Stop is the one button that works
                    // meanwhile, and says it ends the start
                    disabled={stopAct.busy !== '' || (busy && !serving)}
                    mix={on('click', () => void stop())}
                  >
                    {stopAct.busy !== '' ? 'Stopping…' : starting && !ending ? 'Cancel start' : 'Stop'}
                  </Button>
                ) : null}{' '}
                <Menu
                  label={serverOn ? 'Switch model' : 'Serve a model'}
                  placement="bottom-start"
                  items={choices.map((c) => ({ name: c.id, label: c.label, type: 'radio' as const, checked: c.current, value: c.id }))}
                  onSelect={(id) => {
                    if (!(serverOn && id === server.modelId)) void serve(id);
                  }}
                  trigger={(m) => (
                    <Button
                      size="sm"
                      variant="ghost"
                      data-testid="local-switch"
                      disabled={busy || starting || !v.runtime.installed || choices.length === 0 || (serverOn && choices.length < 2)}
                      title={choices.length === 0 ? 'Download a model first' : serverOn && choices.length < 2 ? 'Download another model to switch to' : undefined}
                      mix={[m]}
                    >
                      {serverOn ? 'Switch model' : 'Serve a model'}
                    </Button>
                  )}
                />{' '}
                <Button size="sm" variant="ghost" data-testid="local-log" aria-expanded={showLog ? 'true' : 'false'} mix={on('click', () => ((showLog = !showLog), redraw()))}>
                  {showLog ? 'Hide log' : 'Log'}
                </Button>
              </>
            }
          >
            <SettingRow
              label="Model"
              control={
                serverOn && (server.baseUrl || server.port) ? (
                  <span mix={[mono, addrCss]} data-testid="local-server-url" title={server.baseUrl}>
                    {hostPort(server.baseUrl, server.port)}
                  </span>
                ) : null
              }
            >
              <span mix={titleCss}>
                <span
                  mix={dotCss}
                  role="img"
                  aria-label={STATE_WORDS[server.state] ?? server.state}
                  data-testid="local-server-state"
                  data-tone={serverTone(server.state)}
                  style={{ background: dotColor(serverTone(server.state)) }}
                />
                <span title={several ? loaded.join(', ') : server.modelId || undefined} data-testid="local-server-title">
                  {title}
                </span>
              </span>
            </SettingRow>
            {server.error ? (
              <div mix={blockCss}>
                <ErrorNote error={server.error} what="Server" />
              </div>
            ) : null}
            {serverOn && server.note ? (
              <div mix={blockCss}>
                <p class="note" data-testid="local-server-note">
                  {server.note}
                </p>
              </div>
            ) : null}
            {serverOn && several ? (
              <SettingRow label="Loaded">
                <span mix={lineCss}>
                  <span data-testid="local-load">{loadLine(v)}</span>
                  <span class="note" data-testid="local-loaded">
                    {loaded.map((id) => servedLabel(id)).join(', ')}. Each is a model of the provider; a request names the one it wants.
                  </span>
                </span>
              </SettingRow>
            ) : null}
            <SettingRow label="Used by">
              <span data-testid="local-used-by">{usedByWords(v.usedBy)}</span>
            </SettingRow>
            <SettingRow label="KV cache">
              <span data-testid="local-kv">
                {cacheNow} · {contextWords(ctxNow)}
              </span>
            </SettingRow>
            {started ? (
              <SettingRow label="Started">
                <span data-testid="local-started">{started}</span>
              </SettingRow>
            ) : null}
            {v.settings.providerId ? (
              <SettingRow label="Provider">
                <span data-testid="local-provider-note">
                  Agents reach it as a provider: see <a href={routes.routing.href()}>Routing</a>.
                </span>
              </SettingRow>
            ) : null}
            {restartNeeded ? (
              <div mix={blockCss}>
                <Notice tone="info" testId="local-restart">
                  Restart the server to apply.
                </Notice>
              </div>
            ) : null}
            {showLog ? (
              <div mix={blockCss}>
                <pre data-testid="local-log-lines">{v.log.length > 0 ? v.log.slice(-200).join('\n') : 'No output yet.'}</pre>
              </div>
            ) : null}
          </SettingsGroup>
        </div>

        <div mix={topCss}>
          {/* Memory: each GPU split into the served model, its KV cache and the rest; the RAM; the disk */}
          <SettingsGroup title="Memory" testId="local-memory-card">
            {v.runtime.devices.probed ? (
              bars.gpus.length > 0 ? (
                bars.gpus.map((g) => (
                  <div key={g.device.id} mix={blockCss}>
                    <MemoryBar
                      name={`GPU · ${g.device.kind}`}
                      title={deviceLine(g.device)}
                      bar={g.bar}
                      legend={g.serving}
                      kvNote={g.serving && kv && !kv.known ? kv.note || 'not estimated' : undefined}
                      testId="local-device"
                      id={g.device.id}
                    />
                  </div>
                ))
              ) : (
                <div mix={blockCss}>
                  <p class="note" data-testid="local-devices">
                    {noGpuWords(v.runtime.devices.adapters)}
                  </p>
                  {v.runtime.devices.note ? (
                    <Disclosure summary="What the engine said" testId="local-devices-note">
                      <pre mix={[mono, noteCss]}>{v.runtime.devices.note}</pre>
                    </Disclosure>
                  ) : null}
                </div>
              )
            ) : v.runtime.devices.error ? (
              <div mix={blockCss}>
                <p class="note" data-testid="local-devices">
                  Devices: {v.runtime.devices.error}
                </p>
              </div>
            ) : null}
            {bars.ram ? (
              <div mix={blockCss}>
                <MemoryBar
                  name="RAM"
                  bar={bars.ram}
                  legend={v.served?.device === 'ram'}
                  kvNote={v.served?.device === 'ram' && kv && !kv.known ? kv.note || 'not estimated' : undefined}
                  testId="local-ram"
                />
              </div>
            ) : null}
            <SettingRow label="Disk">
              <span data-testid="local-memory">{diskLine(v)}</span>
            </SettingRow>
          </SettingsGroup>

          {/* Engine: the build on this PC (a llama.cpp build, never named), its updates */}
          <div data-testid="local-runtime" data-installed={String(v.runtime.installed)}>
            <SettingsGroup title="Engine" actions={v.runtime.bundled ? <Badge testId="local-bundled">came with Drydock</Badge> : undefined}>
              {v.runtime.installed ? (
                <>
                  <SettingRow label="Build">
                    <span data-testid="local-build" title={v.runtime.installedAt ? `${runtimeLine(v.runtime, label)}, installed ${formatDate(v.runtime.installedAt)}` : runtimeLine(v.runtime, label)}>
                      {v.runtime.sourceName || 'Model engine'} <span mix={mono}>{v.runtime.tag}</span>
                      {v.runtime.backend ? ` · ${label(v.runtime.backend)}` : ''}
                    </span>
                  </SettingRow>
                  <SettingRow
                    label="Updates"
                    control={
                      <Button
                        size="sm"
                        variant={v.runtime.latest ? 'primary' : 'secondary'}
                        data-testid="local-check-updates"
                        disabled={busy || installing}
                        mix={on('click', () => void checkUpdates())}
                      >
                        {act.busy === 'check-updates' ? 'Checking…' : v.runtime.latest ? `Update to ${v.runtime.latest}` : 'Check for updates'}
                      </Button>
                    }
                  >
                    <span mix={lineCss}>
                      <span class="note" data-testid="local-runtime-note">
                        Drydock set it up and keeps it current. An update restarts the server on the same model.
                      </span>
                      {checked && !v.runtime.latest && !installing ? (
                        <span role="status" data-testid="local-up-to-date">
                          This is the newest build.
                        </span>
                      ) : null}
                    </span>
                  </SettingRow>
                </>
              ) : (
                <SettingRow
                  label="Build"
                  control={
                    !installing && job?.state !== 'failed' ? (
                      <Button variant="primary" size="sm" data-testid="local-install" disabled={busy} mix={on('click', () => void install(TURBOQUANT, ''))}>
                        Set up
                      </Button>
                    ) : null
                  }
                >
                  <span mix={lineCss}>
                    <span data-testid="local-build">{installing ? 'Setting up local models' : 'Local models are not set up'}</span>
                    <span class="note">
                      {installing
                        ? "Drydock is putting its model engine in place, with Google's TurboQuant, checked against GitHub's digest. Nothing has to be installed by hand."
                        : 'Drydock puts its model engine in place by itself when it starts. Set up does it now.'}
                    </span>
                  </span>
                </SettingRow>
              )}
              {job ? (
                <div mix={blockCss} data-testid="local-job" data-state={job.state}>
                  {job.state === 'running' ? (
                    <>
                      <Meter value={jobFraction(job)} label={`${job.target}: ${progressText(job)}`} />
                      <span class="note">
                        {job.target}: {progressText(job)}
                        {job.file ? ` · ${job.file}` : ''}
                      </span>
                    </>
                  ) : job.state === 'failed' ? (
                    <>
                      <ErrorNote error={job.error} what={`${job.target} failed`} />
                      <span>
                        <Button
                          size="sm"
                          data-testid="local-retry"
                          disabled={busy}
                          mix={on('click', () => void install(v.runtime.installed ? v.settings.source : TURBOQUANT, v.runtime.installed ? v.settings.backend || chosenBackend : ''))}
                        >
                          Retry
                        </Button>
                      </span>
                    </>
                  ) : (
                    <span class="note">
                      {job.target}: {job.phase || job.state}
                    </span>
                  )}
                </div>
              ) : null}
            </SettingsGroup>
          </div>
        </div>

        {/* Embeddings: the embedding model's own server (decision P111), shown once there is an embedding model */}
        {embedModels.length > 0 || embedOn ? (
          <div data-testid="local-embeddings" data-state={embed.state}>
            <SettingsGroup
              title="Embeddings"
              actions={
                embedOn ? (
                  <Button
                    size="sm"
                    data-testid="local-embed-stop"
                    disabled={busy}
                    mix={on('click', () => void change('embed-stop', () => s.client.localEmbeddingsStop()))}
                  >
                    {act.busy === 'embed-stop' ? 'Stopping…' : 'Stop'}
                  </Button>
                ) : undefined
              }
            >
              <SettingRow
                label="Model"
                control={
                  embedOn && embed.port ? (
                    <span mix={[mono, addrCss]} data-testid="local-embed-url" title={embed.baseUrl}>
                      {hostPort(embed.baseUrl, embed.port)}
                    </span>
                  ) : null
                }
              >
                <span mix={titleCss}>
                  <span
                    mix={dotCss}
                    role="img"
                    aria-label={STATE_WORDS[embed.state] ?? embed.state}
                    data-tone={serverTone(embed.state)}
                    style={{ background: dotColor(serverTone(embed.state)) }}
                  />
                  <span data-testid="local-embed-title">
                    {embed.state === 'running' && embed.modelId
                      ? `Serving ${modelName(splitModelId(embed.modelId).repo)} for embeddings`
                      : embed.state === 'starting'
                        ? 'Starting the embedding model'
                        : embed.state === 'failed'
                          ? 'The embedding server failed'
                          : 'No embedding model served'}
                  </span>
                </span>
              </SettingRow>
              {embed.error ? (
                <div mix={blockCss}>
                  <ErrorNote error={embed.error} what="Embeddings" />
                </div>
              ) : null}
              <div mix={blockCss}>
                <p class="note">
                  An embedding model turns text into vectors (OpenAI&apos;s /v1/embeddings) and cannot chat, so it runs on a server of its own, on 127.0.0.1 only, with a key
                  of its own. It is not one of the provider&apos;s models.
                </p>
              </div>
              {/* its one use (decision P120): ranking pack searches by meaning, the person's choice */}
              <SettingRow label="Pack search" testId="local-rank-packs">
                <Checkbox
                  label="Rank pack searches by meaning"
                  description={
                    embedOn
                      ? 'The passages a pack search finds are put in order by how close they are to the question, with this model. The text stays on this PC.'
                      : 'While an embedding model is served, the passages a pack search finds are put in order by how close they are to the question. Until then, by the words they share.'
                  }
                  checked={!!v.settings.rankPacks}
                  disabled={busy}
                  onChange={(c) => void setSetting({ rankPacks: c })}
                />
              </SettingRow>
            </SettingsGroup>
          </div>
        ) : null}

        {/* the model table: the models on this PC, the downloads, and what the Hub offers */}
        <SettingsGroup title="Models" label="Local models" testId="local-models-card" actions={hub} bare>
          <div mix={tableCss}>
            <div class="m th" aria-hidden="true">
              <span>Model</span>
              <span>Size</span>
              <span>On this PC</span>
              <span />
            </div>
            {v.models.length === 0 && v.downloads.length === 0 && !repos ? <p class="empty">No model yet: find one on Hugging Face.</p> : null}
            {v.models.length > 0 ? <ul aria-label="Downloaded models">{v.models.map(modelRow)}</ul> : null}
            {v.downloads.length > 0 ? <ul aria-label="Downloads">{v.downloads.map((d) => jobRow(d, busy))}</ul> : null}
            {repos ? (
              <>
                <div class="group">
                  <span class="grow" data-testid="local-repos-title">
                    {results ? `Hugging Face, "${searched}": ${repos.length === 1 ? '1 repository' : `${repos.length} repositories`}` : 'Suggested on Hugging Face'}
                  </span>
                  {results ? (
                    <Button size="sm" variant="ghost" data-testid="local-suggested" disabled={busy} mix={on('click', () => void suggest())}>
                      {act.busy === 'suggested' ? 'Loading…' : 'Show suggestions'}
                    </Button>
                  ) : null}
                  <Button size="sm" variant="ghost" data-testid="local-repos-close" disabled={busy} mix={on('click', closeRepos)}>
                    {results ? 'Close results' : 'Close suggestions'}
                  </Button>
                </div>
                <ul aria-label={results ? 'Search results' : 'Suggested models'} data-testid="local-repos">
                  {repos.length === 0 ? <li class="empty">Nothing found.</li> : null}
                  {repos.map((r) => [
                    <li key={r.id} class="m">
                      <button
                        type="button"
                        mix={[repoBtn, on('click', () => void open(r))]}
                        aria-pressed={openRepo === r.id ? 'true' : 'false'}
                        aria-label={`${r.id}, ${compactCount(r.downloads)} downloads`}
                        data-testid="local-repo"
                        data-id={r.id}
                      >
                        <Icon icon={openRepo === r.id ? ChevronDown : ChevronRight} size={14} />
                        <span class="name">{modelName(r.id)}</span>
                        <span class="repo">{r.id}</span>
                        <span class="count">{compactCount(r.downloads)} downloads</span>
                        {r.gated ? (
                          <Badge tone="warn" testId="local-gated" title="Hugging Face shares its files only with an account that accepted its licence">
                            gated
                          </Badge>
                        ) : null}
                        {(r.tags ?? []).map((tag) => (
                          <Badge key={tag}>{tag}</Badge>
                        ))}
                      </button>
                    </li>,
                    openRepo === r.id ? (
                      variants ? (
                        variants.length === 0 ? (
                          <li key={`${r.id}:none`} class="empty">
                            No GGUF file the Hub has a checksum for.
                          </li>
                        ) : (
                          variants.map((va) => variantRow(r, va))
                        )
                      ) : (
                        <li key={`${r.id}:loading`} class="empty">
                          <Loading label="Reading the repository…" />
                        </li>
                      )
                    ) : null,
                  ])}
                </ul>
              </>
            ) : (
              <div class="foot">
                <span class="grow">Search Hugging Face above, or start from the models most downloaded.</span>
                <Button size="sm" variant="ghost" data-testid="local-suggested" disabled={busy} mix={on('click', () => void suggest())}>
                  {act.busy === 'suggested' ? 'Loading…' : 'Show suggestions'}
                </Button>
              </div>
            )}
          </div>
        </SettingsGroup>

        {/* the settings the next start uses */}
        <SettingsGroup title="Server settings" testId="local-settings">
          {turbo.length > 0 ? (
            <div mix={blockCss}>
              <span mix={muted} data-testid="local-turboquant">
                Google&apos;s TurboQuant is built in: {turbo.join(' and ')} keep more context in less memory.
              </span>
            </div>
          ) : null}
          <SettingRow
            label={<label for={ids.cache}>KV cache type</label>}
            control={
              <Select
                id={ids.cache}
                testId="local-cache"
                value={v.settings.cacheType}
                disabled={busy}
                options={selectValues(cacheTypes.includes(v.settings.cacheType) ? cacheTypes : [v.settings.cacheType, ...cacheTypes])}
                onChange={(val) => void setSetting({ cacheType: val })}
              />
            }
          >
            <span mix={muted}>q8_0: near-lossless, half the KV memory of f16. q4_0: a quarter.</span>
          </SettingRow>
          <SettingRow
            label={<label for={ids.context}>Context size</label>}
            control={
              <input
                id={ids.context}
                type="number"
                min="0"
                step="1024"
                data-testid="local-context"
                value={String(v.settings.contextSize)}
                disabled={busy}
                mix={[
                  numberCss,
                  on('change', (e) => {
                    const n = wholeNumber(e.currentTarget.value);
                    if (n !== undefined && n !== v.settings.contextSize) void setSetting({ contextSize: n });
                  }),
                ]}
              />
            }
          >
            <span mix={muted}>Tokens; 0 is the model&apos;s own. Coding agents need 64k or more.</span>
          </SettingRow>
          <SettingRow
            label={<label for={ids.port}>Port</label>}
            control={
              <input
                id={ids.port}
                type="number"
                min="1024"
                max="65535"
                data-testid="local-port"
                value={String(v.settings.port)}
                disabled={busy}
                mix={[
                  numberCss,
                  on('change', (e) => {
                    const n = wholeNumber(e.currentTarget.value);
                    if (n !== undefined && n !== v.settings.port) void setSetting({ port: n });
                  }),
                ]}
              />
            }
          >
            <span mix={muted}>The server listens on 127.0.0.1 only.</span>
          </SettingRow>
          <SettingRow
            label={<label for={ids.embedPort}>Embeddings port</label>}
            control={
              <input
                id={ids.embedPort}
                type="number"
                min="1024"
                max="65535"
                data-testid="local-embed-port"
                value={String(v.settings.embeddingPort || 9934)}
                disabled={busy}
                mix={[
                  numberCss,
                  on('change', (e) => {
                    const n = wholeNumber(e.currentTarget.value);
                    if (n !== undefined && n !== v.settings.embeddingPort) void setSetting({ embeddingPort: n });
                  }),
                ]}
              />
            }
          >
            <span mix={muted}>The embedding model&apos;s own server, also on 127.0.0.1 only.</span>
          </SettingRow>
          <SettingRow label="At startup" testId="local-autostart">
            <Checkbox
              label="Start the server with Drydock"
              description="Serves the models served last time, and the embedding model."
              checked={v.settings.autostart}
              disabled={busy}
              onChange={(c) => void setSetting({ autostart: c })}
            />
          </SettingRow>
          {/* the Hugging Face token (decision P111): for gated models; kept by the core in the credential store, shown masked */}
          <SettingRow
            label={<label for={ids.token}>Hugging Face token</label>}
            testId="local-hf"
            control={
              token.set ? (
                <Button size="sm" variant="ghost" data-testid="local-hf-remove" disabled={busy} mix={on('click', () => void change('hf-remove', () => s.client.localHfTokenRemove()))}>
                  {act.busy === 'hf-remove' ? 'Removing…' : 'Remove'}
                </Button>
              ) : (
                <form
                  mix={[
                    tokenFormCss,
                    on('submit', (e) => {
                      e.preventDefault();
                      const text = tokenText.trim();
                      if (!text) return;
                      void change('hf-set', () => s.client.localHfTokenSet(text)).then((res) => {
                        if (res) tokenText = '';
                        redraw();
                      });
                    }),
                  ]}
                  data-testid="local-hf-form"
                >
                  <input
                    id={ids.token}
                    type="password"
                    autocomplete="off"
                    spellcheck={false}
                    placeholder="hf_…"
                    data-testid="local-hf-input"
                    value={tokenText}
                    disabled={busy}
                    mix={on('input', (e) => {
                      tokenText = e.currentTarget.value;
                      redraw();
                    })}
                  />
                  <Button size="sm" type="submit" data-testid="local-hf-save" disabled={busy || tokenText.trim() === ''}>
                    {act.busy === 'hf-set' ? 'Saving…' : 'Save'}
                  </Button>
                </form>
              )
            }
          >
            <span mix={lineCss}>
              {token.set ? (
                <span mix={mono} data-testid="local-hf-masked">
                  {tokenWords(token)}
                </span>
              ) : null}
              <span class="note">
                For gated models. Kept in the Windows Credential Manager and sent only with downloads from huggingface.co. A token with read access is
                enough:{' '}
                <a href="https://huggingface.co/settings/tokens" target="_blank" rel="noopener noreferrer" data-rmx-document>
                  make one on Hugging Face
                </a>
                .
              </span>
            </span>
          </SettingRow>
        </SettingsGroup>
      </>
    );
  }
}

/** The state dot's colour: the tone's foreground. */
function dotColor(tn: Tone): string {
  return tn === 'muted' ? t.textFaint : tone[tn].fg;
}
