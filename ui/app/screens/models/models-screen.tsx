// Models on this PC (decisions P65, P97): a workspace page of its own (/models). The engine (a llama.cpp build) is part of
// Drydock and never named on the page (decision P95): Drydock sets the TurboQuant build up itself (from the download, else from
// GitHub, checked against the digest GitHub recorded); a person finds GGUF models on the Hugging Face Hub (the search in the
// head, "/" goes to it), downloads them (checked against the Hub's SHA-256) and serves one; the server is then an ordinary
// provider, listed on Providers. The page follows the mockup: the Serving card (the model, its address, the agents that use it,
// its KV cache and context, when it started, Stop and Switch model), Memory (each GPU split into the model, its KV cache and
// the rest, then the RAM), Engine (the build and backend, Update to <tag>), the model table (name and quantization, repository,
// size, where it fits, the action), then the server settings. Installs and downloads run in the background: their progress
// lives in the view (local.get), which the event local.changed re-reads; methods that change things answer with the fresh
// view, shown at once (q.set).

import { css, on, ref, type Handle } from 'remix/component';
import { ChevronDown, ChevronRight, Cpu, Search } from 'lucide';
import type { LocalBackend, LocalJob, LocalModel, LocalSettingsEdit, LocalView, Repo, Variant } from '../../api/localClient.ts';
import { formatDate } from '../../lib/format.ts';
import { ownsKeys } from '../../lib/keys.ts';
import { routes } from '../../routes.ts';
import { action, query, services } from '../../state/services.tsx';
import { Button } from '../../ui/button.tsx';
import { Badge, mono } from '../../ui/data.tsx';
import { ErrorNote, Loading, Notice } from '../../ui/feedback.tsx';
import { Checkbox, Field } from '../../ui/form.tsx';
import { Icon } from '../../ui/icon.tsx';
import { Page, Section } from '../../ui/layout.tsx';
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
  type FitWords,
  fitWords,
  gb,
  gpus,
  hostPort,
  jobFraction,
  memoryBars,
  modelName,
  progressText,
  runtimeLine,
  serverTone,
  splitModelId,
  startedWords,
  switchChoices,
  usedByWords,
  usedOf,
  wholeNumber,
} from './models.ts';

/** The build Drydock sets up (decision P95): the TurboQuant one, with Google's TurboQuant KV cache types. */
const TURBOQUANT = 'turboquant';
const RESTART_FIELDS = new Set(['cacheType', 'contextSize', 'port']);
const STATE_WORDS: Record<string, string> = { stopped: 'stopped', starting: 'starting', running: 'running', failed: 'failed' };

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
const cardsCss = css({ display: 'flex', flexWrap: 'wrap', gap: '14px', minWidth: '0' });
const cardCss = css({
  display: 'flex',
  flexDirection: 'column',
  gap: t.s3,
  padding: t.s4,
  border: `1px solid ${t.border}`,
  borderRadius: t.rLg,
  background: t.bgSurface,
  minWidth: '0',
  '& h2': { margin: '0', fontSize: t.textMd, fontWeight: '600', minWidth: '0', overflowWrap: 'anywhere' },
  '& p': { margin: '0' },
  '& .head': { display: 'flex', alignItems: 'center', gap: t.s2, minWidth: '0' },
  '& .addr': { marginLeft: 'auto', color: t.textFaint, fontSize: t.textXs },
  '& .acts': { display: 'flex', alignItems: 'center', gap: t.s2, flexWrap: 'wrap' },
  '& .note': { color: t.textFaint, fontSize: t.textXs, lineHeight: '1.5' },
  '& .build': { color: t.textMuted },
  '& pre': { margin: '0', maxHeight: '220px', overflow: 'auto', padding: t.s2, borderRadius: t.rMd, background: t.bgApp, fontSize: t.textXs },
});
const dotCss = css({ width: '8px', height: '8px', borderRadius: '50%', flex: 'none' });
const dlCss = css({
  margin: '0',
  display: 'grid',
  gridTemplateColumns: '110px minmax(0, 1fr)',
  rowGap: t.s2,
  columnGap: '10px',
  '& dt': { color: t.textFaint },
  '& dd': { margin: '0', minWidth: '0', overflowWrap: 'anywhere' },
});
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
const settingsCss = css({
  display: 'grid',
  gridTemplateColumns: 'repeat(auto-fill, minmax(220px, 1fr))',
  gap: t.s3,
  alignItems: 'start',
  '& input[type="number"]': { width: '120px' },
});

/** Whether a key press is typing (a field, a box, an editor, a menu), so "/" must leave it alone. */
function typing(e: KeyboardEvent): boolean {
  if (e.ctrlKey || e.metaKey || e.altKey || ownsKeys(e.target)) return true;
  const el = e.target instanceof HTMLElement ? e.target : null;
  return !!el && (el.isContentEditable || ['INPUT', 'TEXTAREA', 'SELECT'].includes(el.tagName) || !!el.closest?.('dialog[open], [role="combobox"], [role="listbox"], [role="menu"]'));
}

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

export function ModelsScreen(handle: Handle<{ /** "now" for the Started line (tests). */ now?: number; /** The item to open (/models/<key>). */ initial?: string }>) {
  const s = services(handle);
  const q = query(handle, {
    load: () => s.client.localGet(),
    when: (e) => e.type === 'local.changed',
    debounceMs: 150,
  });
  const act = action(handle);
  // Stop has an action of its own: a start waits for the core until the model is loaded (up to 15 minutes) and holds `act` all
  // that time, and Stop has to work meanwhile (audit 2026-10-16, L44)
  const stopAct = action(handle);
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
    if (v) q.set(v);
    return v;
  };
  // Serve: the core answers when the model is loaded. A Stop pressed meanwhile ends that start, and its answer is dropped.
  const serve = (modelId: string) => {
    cancelledStart = false;
    return change(`serve:${modelId}`, async () => {
      let view: LocalView;
      try {
        view = await s.client.localServerStart(modelId);
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
      q.set(v);
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
    const running = q.data?.server.state === 'running' || q.data?.server.state === 'starting';
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
              <span class="what">{job.error || 'failed'}</span>
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
    const v = q.data;
    const busy = act.busy !== '' || stopAct.busy !== '';
    const supported = !!v?.supported;

    const hub = supported ? (
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
    ) : undefined;

    return (
      <Page title="Models on this PC" icon={Cpu} subtitle="Private: prompts and code never leave this computer" guide="local-models" actions={hub} testId="local-models">
        <ErrorNote error={q.error ?? act.error ?? stopAct.error} what="Models on this PC" />
        {stoppedNote ? (
          <Notice tone="info" role="status" testId="local-stopped">
            The server was stopped.
          </Notice>
        ) : null}
        {!v ? (
          q.loading ? <Loading /> : null
        ) : !v.supported ? (
          // a PC where local models cannot run (not Windows): the reason only; the rest of the view may be absent
          <div mix={cardCss} data-testid="local-runtime">
            <p>{v.reason || 'Local models are not available here.'}</p>
          </div>
        ) : (
          body(v, busy)
        )}
      </Page>
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
    const title =
      server.state === 'running' ? `Serving ${servedName}` : server.state === 'starting' || serving ? `Starting ${servedName || 'the server'}` : server.state === 'failed' ? 'The server failed' : 'Nothing served';
    const choices = switchChoices(v.models, serverOn ? server.modelId : undefined);
    const downloading = new Set(v.downloads.filter((d) => d.state === 'running').map((d) => d.target));
    const have = new Set(v.models.map((m) => m.id));

    const modelRow = (m: LocalModel) => {
      const isServed = serverOn && server.modelId === m.id;
      const words = fitWords(m.fit, m.sizeBytes, { gpu: hasGpu, serving: isServed });
      // the model the server is loading: "Starting · GPU" until it is ready
      const fit = words && isServed && server.state === 'starting' ? { ...words, text: words.text.replace(/^Serving/, 'Starting') } : words;
      return (
        <li key={m.id} class="m" data-testid="local-model" data-id={m.id} data-served={isServed ? 'true' : undefined}>
          {nameCell(m.repo, m.variant)}
          <span class="size">{gb(m.sizeBytes)}</span>
          <FitBadge fit={fit} />
          <span class="act">
            {isServed ? (
              <span class="word">downloaded</span>
            ) : (
              <>
                <Button
                  size="sm"
                  data-testid="local-serve"
                  disabled={busy || starting || !v.runtime.installed}
                  title={v.runtime.installed ? undefined : 'The engine is not set up yet'}
                  mix={on('click', () => void serve(m.id))}
                >
                  {act.busy === `serve:${m.id}` ? 'Starting…' : 'Serve'}
                </Button>
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
            )}
          </span>
        </li>
      );
    };

    const variantRow = (repo: string, va: Variant) => {
      const id = `${repo}:${va.name}`;
      const fit = fitWords(va.fit, va.sizeBytes, { gpu: hasGpu });
      const tooBig = va.fit?.target === 'none';
      return (
        <li key={va.name} class="m" data-testid="local-variant" data-name={va.name} data-offered={tooBig ? 'false' : undefined}>
          {nameCell(repo, va.quant || va.name, (va.notes ?? []).join(' ') || undefined)}
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
            ) : (
              <Button size="sm" data-testid="local-download" disabled={busy} mix={on('click', () => void change(`download:${va.name}`, () => s.client.localModelDownload(repo, va.name)))}>
                Download
              </Button>
            )}
          </span>
        </li>
      );
    };

    const stopShown = serverOn || (serving && !ending) || stopAct.busy !== '';
    return (
      <>
        <div mix={cardsCss}>
          {/* Serving: the server, the model it serves, who uses it */}
          <div mix={cardCss} style={{ flex: '1 1 300px' }} data-testid="local-server" data-state={server.state}>
            <div class="head">
              <span
                mix={dotCss}
                role="img"
                aria-label={STATE_WORDS[server.state] ?? server.state}
                data-testid="local-server-state"
                data-tone={serverTone(server.state)}
                style={{ background: dotColor(serverTone(server.state)) }}
              />
              <h2 title={server.modelId || undefined} data-testid="local-server-title">
                {title}
              </h2>
              {serverOn && (server.baseUrl || server.port) ? (
                <span class="addr" mix={mono} data-testid="local-server-url" title={server.baseUrl}>
                  {hostPort(server.baseUrl, server.port)}
                </span>
              ) : null}
            </div>
            {server.error ? <ErrorNote error={server.error} what="Server" /> : null}
            <dl mix={dlCss}>
              <dt>Used by</dt>
              <dd data-testid="local-used-by">{usedByWords(v.usedBy)}</dd>
              <dt>KV cache</dt>
              <dd data-testid="local-kv">
                {cacheNow} · {contextWords(ctxNow)}
              </dd>
              {started ? (
                <>
                  <dt>Started</dt>
                  <dd data-testid="local-started">{started}</dd>
                </>
              ) : null}
            </dl>
            {restartNeeded ? (
              <Notice tone="info" testId="local-restart">
                Restart the server to apply.
              </Notice>
            ) : null}
            <div class="acts">
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
              ) : null}
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
              />
              <Button size="sm" variant="ghost" data-testid="local-log" aria-expanded={showLog ? 'true' : 'false'} mix={on('click', () => ((showLog = !showLog), redraw()))}>
                {showLog ? 'Hide log' : 'Log'}
              </Button>
            </div>
            {v.settings.providerId ? (
              <p class="note" data-testid="local-provider-note">
                Agents reach it as a provider: see <a href={routes.providers.href()}>Providers</a>.
              </p>
            ) : null}
            {showLog ? <pre data-testid="local-log-lines">{v.log.length > 0 ? v.log.slice(-200).join('\n') : 'No output yet.'}</pre> : null}
          </div>

          {/* Memory: each GPU split into the served model, its KV cache and the rest; the RAM */}
          <div mix={cardCss} style={{ flex: '1 1 300px' }} data-testid="local-memory-card">
            <h2>Memory</h2>
            {v.runtime.devices.probed ? (
              bars.gpus.length > 0 ? (
                bars.gpus.map((g) => (
                  <MemoryBar
                    key={g.device.id}
                    name={`GPU · ${g.device.kind}`}
                    title={deviceLine(g.device)}
                    bar={g.bar}
                    legend={g.serving}
                    kvNote={g.serving && kv && !kv.known ? kv.note || 'not estimated' : undefined}
                    testId="local-device"
                    id={g.device.id}
                  />
                ))
              ) : (
                <p class="note" data-testid="local-devices">
                  No GPU found: runs on the CPU
                </p>
              )
            ) : v.runtime.devices.error ? (
              <p class="note" data-testid="local-devices">
                Devices: {v.runtime.devices.error}
              </p>
            ) : null}
            {bars.ram ? (
              <MemoryBar
                name="RAM"
                bar={bars.ram}
                legend={v.served?.device === 'ram'}
                kvNote={v.served?.device === 'ram' && kv && !kv.known ? kv.note || 'not estimated' : undefined}
                testId="local-ram"
              />
            ) : null}
            <p class="note" data-testid="local-memory">
              {diskLine(v)}
            </p>
          </div>

          {/* Engine: the build on this PC (a llama.cpp build, never named), its updates */}
          <div mix={cardCss} style={{ flex: '1 1 260px' }} data-testid="local-runtime" data-installed={String(v.runtime.installed)}>
            <div class="head">
              <h2>Engine</h2>
              {v.runtime.bundled ? <Badge testId="local-bundled">came with Drydock</Badge> : null}
            </div>
            {v.runtime.installed ? (
              <>
                <span class="build" data-testid="local-build" title={v.runtime.installedAt ? `${runtimeLine(v.runtime, label)}, installed ${formatDate(v.runtime.installedAt)}` : runtimeLine(v.runtime, label)}>
                  {v.runtime.sourceName || 'Model engine'} <span mix={mono}>{v.runtime.tag}</span>
                  {v.runtime.backend ? ` · ${label(v.runtime.backend)}` : ''}
                </span>
                <p class="note" data-testid="local-runtime-note">
                  Drydock set it up and keeps it current. An update restarts the server on the same model.
                </p>
                {checked && !v.runtime.latest && !installing ? (
                  <p class="note" role="status" data-testid="local-up-to-date">
                    This is the newest build.
                  </p>
                ) : null}
                <span>
                  <Button
                    size="sm"
                    variant={v.runtime.latest ? 'primary' : 'secondary'}
                    data-testid="local-check-updates"
                    disabled={busy || installing}
                    mix={on('click', () => void checkUpdates())}
                  >
                    {act.busy === 'check-updates' ? 'Checking…' : v.runtime.latest ? `Update to ${v.runtime.latest}` : 'Check for updates'}
                  </Button>
                </span>
              </>
            ) : (
              <>
                <span class="build" data-testid="local-build">
                  {installing ? 'Setting up local models' : 'Local models are not set up'}
                </span>
                <p class="note">
                  {installing
                    ? "Drydock is putting its model engine in place, with Google's TurboQuant, checked against GitHub's digest. Nothing has to be installed by hand."
                    : 'Drydock puts its model engine in place by itself when it starts. Set up does it now.'}
                </p>
                {!installing && job?.state !== 'failed' ? (
                  <span>
                    <Button variant="primary" size="sm" data-testid="local-install" disabled={busy} mix={on('click', () => void install(TURBOQUANT, ''))}>
                      Set up
                    </Button>
                  </span>
                ) : null}
              </>
            )}
            {job ? (
              <div data-testid="local-job" data-state={job.state} style={{ display: 'grid', gap: '4px' }}>
                {job.state === 'running' ? (
                  <>
                    <Meter value={jobFraction(job)} label={`${job.target}: ${progressText(job)}`} />
                    <span class="note">
                      {job.target}: {progressText(job)}
                      {job.file ? ` · ${job.file}` : ''}
                    </span>
                  </>
                ) : job.state === 'failed' ? (
                  <div class="acts">
                    <ErrorNote error={job.error} what={`${job.target} failed`} />
                    <Button
                      size="sm"
                      data-testid="local-retry"
                      disabled={busy}
                      mix={on('click', () => void install(v.runtime.installed ? v.settings.source : TURBOQUANT, v.runtime.installed ? v.settings.backend || chosenBackend : ''))}
                    >
                      Retry
                    </Button>
                  </div>
                ) : (
                  <span class="note">
                    {job.target}: {job.phase || job.state}
                  </span>
                )}
              </div>
            ) : null}
          </div>
        </div>

        {/* the model table: the models on this PC, the downloads, and what the Hub offers */}
        <section aria-label="Models" mix={tableCss} data-testid="local-models-card">
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
                        variants.map((va) => variantRow(r.id, va))
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
        </section>

        {/* the settings the next start uses */}
        <Section
          title="Server settings"
          testId="local-settings"
          description={
            installedSrc && installedSrc.cacheTypes.some((c) => c.startsWith('turbo')) ? (
              <span data-testid="local-turboquant">
                Google&apos;s TurboQuant is built in: {installedSrc.cacheTypes.filter((c) => c.startsWith('turbo')).join(' and ')} keep more context in less memory.
              </span>
            ) : undefined
          }
        >
          <div mix={settingsCss}>
            <Field label="KV cache type" hint="q8_0: near-lossless, half the KV memory of f16. q4_0: a quarter.">
              {(id) => (
                <Select
                  id={id}
                  testId="local-cache"
                  value={v.settings.cacheType}
                  disabled={busy}
                  options={selectValues(cacheTypes.includes(v.settings.cacheType) ? cacheTypes : [v.settings.cacheType, ...cacheTypes])}
                  onChange={(val) => void setSetting({ cacheType: val })}
                />
              )}
            </Field>
            <Field label="Context size" hint="Tokens; 0 is the model's own. Coding agents need 64k or more.">
              {(id) => (
                <input
                  id={id}
                  type="number"
                  min="0"
                  step="1024"
                  data-testid="local-context"
                  value={String(v.settings.contextSize)}
                  disabled={busy}
                  mix={on('change', (e) => {
                    const n = wholeNumber(e.currentTarget.value);
                    if (n !== undefined && n !== v.settings.contextSize) void setSetting({ contextSize: n });
                  })}
                />
              )}
            </Field>
            <Field label="Port" hint="The server listens on 127.0.0.1 only.">
              {(id) => (
                <input
                  id={id}
                  type="number"
                  min="1024"
                  max="65535"
                  data-testid="local-port"
                  value={String(v.settings.port)}
                  disabled={busy}
                  mix={on('change', (e) => {
                    const n = wholeNumber(e.currentTarget.value);
                    if (n !== undefined && n !== v.settings.port) void setSetting({ port: n });
                  })}
                />
              )}
            </Field>
            <div data-testid="local-autostart">
              <Checkbox
                label="Start the server with Drydock"
                description="Serves the model served last time."
                checked={v.settings.autostart}
                disabled={busy}
                onChange={(c) => void setSetting({ autostart: c })}
              />
            </div>
          </div>
        </Section>
      </>
    );
  }
}

/** The state dot's colour: the tone's foreground. */
function dotColor(tn: Tone): string {
  return tn === 'muted' ? t.textFaint : tone[tn].fg;
}
