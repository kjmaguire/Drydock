// Models on this PC (decision P65): llama.cpp on this computer, at the top of the Providers page. A person installs an official
// llama.cpp build (checked against the digest GitHub recorded), downloads GGUF models from the Hugging Face Hub (checked
// against the Hub's SHA-256) and serves one; the server is then registered as an ordinary provider, which appears as a tile
// below. Installs and downloads run in the background: their progress lives in the view (local.get), which the event
// local.changed re-reads; methods that change things answer with the fresh view, shown at once (q.set).

import { css, on, type Handle } from "remix/component";
import type {
  LocalBackend,
  LocalJob,
  LocalSettingsEdit,
  LocalView,
  Repo,
  Variant,
} from "../../api/localClient.ts";
import { formatDate } from "../../lib/format.ts";
import { action, query, services } from "../../state/services.tsx";
import { Button } from "../../ui/button.tsx";
import { Badge, mono, muted } from "../../ui/data.tsx";
import { ErrorNote, Loading, Notice } from "../../ui/feedback.tsx";
import { Checkbox, Field } from "../../ui/form.tsx";
import { Select, selectValues } from "../../ui/select.tsx";
import { Section } from "../../ui/layout.tsx";
import { LearnMore } from '../../ui/learn-more.tsx';
import { t } from "../../ui/tokens.ts";
import { Meter } from "../budgets/meter.tsx";
import { ConfirmAction } from "../shared/confirm-action.tsx";
import {
  compactCount,
  deviceLine,
  type FitWords,
  fitText,
  gb,
  gpus,
  jobFraction,
  memoryLine,
  progressText,
  runtimeLine,
  serverTone,
  wholeNumber,
} from "./local-models.ts";

const cardCss = css({
  display: "grid",
  gap: t.s3,
  padding: "16px 18px",
  border: `1px solid ${t.border}`,
  borderRadius: t.rLg,
  background: t.bgSurface,
  minWidth: "0",
  fontSize: t.textSm,
  "& h3": { fontSize: t.textMd, fontWeight: "600", margin: "0" },
  "& p": { margin: "0" },
  "& .line": {
    display: "flex",
    alignItems: "center",
    gap: t.s2,
    flexWrap: "wrap",
    minWidth: "0",
  },
  "& .acts": {
    marginLeft: "auto",
    display: "flex",
    alignItems: "center",
    gap: t.s2,
    flexWrap: "wrap",
  },
  '& select, & input[type="number"], & input[type="search"]': {
    fontSize: t.textSm,
  },
  '& input[type="number"]': { width: "120px" },
  "& ul": {
    margin: "0",
    padding: "0",
    listStyle: "none",
    display: "grid",
    gap: t.s2,
  },
  "& pre": {
    margin: "0",
    maxHeight: "220px",
    overflow: "auto",
    padding: t.s2,
    borderRadius: t.rMd,
    background: t.bgApp,
    fontSize: t.textXs,
  },
});
const rowCss = css({
  display: "flex",
  alignItems: "center",
  gap: t.s2,
  flexWrap: "wrap",
  padding: `${t.s2} ${t.s3}`,
  borderRadius: t.rMd,
  border: `1px solid ${t.border}`,
  background: t.bgApp,
  minWidth: "0",
  "& .name": {
    fontWeight: "600",
    fontFamily: t.mono,
    fontSize: t.textSm,
    overflowWrap: "anywhere",
  },
  "& .what": {
    flexBasis: "100%",
    color: t.textMuted,
    fontSize: t.textSm,
    overflowWrap: "anywhere",
  },
  "& .bar": { flexBasis: "100%", display: "grid", gap: "4px" },
});
const pickCss = css({
  display: "grid",
  gridTemplateColumns: "repeat(auto-fill, minmax(220px, 1fr))",
  gap: t.s3,
  alignItems: "start",
});
const repoBtn = css({
  display: "flex",
  alignItems: "center",
  gap: t.s2,
  flexWrap: "wrap",
  width: "100%",
  justifyContent: "flex-start",
  whiteSpace: "normal",
  textAlign: "left",
  padding: `${t.s2} ${t.s3}`,
  border: `1px solid ${t.border}`,
  borderRadius: t.rMd,
  background: t.bgSurface,
  color: t.text,
  fontSize: t.textSm,
  cursor: "pointer",
  "&:hover": { borderColor: t.borderStrong },
  '&[aria-pressed="true"]': { borderColor: t.accent, background: t.accentSoft },
  "& .id": { fontFamily: t.mono, fontWeight: "600" },
});
const searchCss = css({
  display: "flex",
  alignItems: "center",
  gap: t.s2,
  flexWrap: "wrap",
  "& input": { flex: "1", minWidth: "200px" },
});

const RESTART_FIELDS = new Set(["cacheType", "contextSize", "port"]);
const SERVER_WORDS: Record<string, string> = {
  stopped: "stopped",
  starting: "starting",
  running: "running",
  failed: "failed",
};

export function LocalModels(handle: Handle) {
  const s = services(handle);
  const q = query(handle, {
    load: () => s.client.localGet(),
    when: (e) => e.type === "local.changed",
    debounceMs: 150,
  });
  const act = action(handle);
  // Stop has an action of its own: a start waits for the core until the model is loaded (up to 15 minutes) and holds `act` all
  // that time, and Stop has to work meanwhile (audit 2026-10-16, L44)
  const stopAct = action(handle);
  // the person's picks before an install ('' until picked: the settings', else the suggested backend)
  let source = "";
  let backend: LocalBackend | "" = "";
  let confirmRemove = false;
  let showLog = false;
  let restartNeeded = false; // a cache, context or port change while the server ran
  // Stop was pressed while a start was waiting: the core answers that start with an error ("the server was stopped before
  // llama-server was ready"), which is what the person asked for, so the answer is dropped and a calm note is shown
  let cancelledStart = false;
  let stoppedNote = false;
  // the Hub: suggestions and search results, the one repository open and its variants
  let suggested: Repo[] | undefined;
  let results: Repo[] | undefined;
  let searchText = "";
  let openRepo = "";
  let variants: Variant[] | undefined;
  const redraw = () => void handle.update();

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
    const cancelling = act.busy.startsWith("serve:");
    if (cancelling) cancelledStart = true;
    stoppedNote = false;
    const v = await stopAct.run("stop", () => s.client.localServerStop());
    if (v) {
      q.set(v);
      stoppedNote = cancelling;
    } else if (cancelling) {
      cancelledStart = false; // the Stop did not go through: the start goes on, and its answer counts after all
    }
    redraw();
  };
  const install = (src: string, be: LocalBackend | "") =>
    change("install", () => s.client.localInstall(src, be));
  const setSetting = async (e: LocalSettingsEdit) => {
    const running =
      q.data?.server.state === "running" || q.data?.server.state === "starting";
    const v = await change("settings", () => s.client.localSettingsSet(e));
    if (v && running && Object.keys(e).some((k) => RESTART_FIELDS.has(k)))
      restartNeeded = true;
    redraw();
  };
  const open = async (repo: Repo) => {
    if (openRepo === repo.id) {
      openRepo = "";
      variants = undefined;
      redraw();
      return;
    }
    openRepo = repo.id;
    variants = undefined;
    redraw();
    const vs = await act.run(`variants:${repo.id}`, () =>
      s.client.localModelVariants(repo.id),
    );
    if (vs && openRepo === repo.id) variants = vs;
    redraw();
  };
  const search = async () => {
    const text = searchText.trim();
    if (!text) return;
    const r = await act.run("search", () => s.client.localModelsSearch(text));
    if (r) results = r;
    redraw();
  };
  const suggest = async () => {
    const r = await act.run("suggested", () => s.client.localModelsSuggested());
    if (r) {
      suggested = r;
      results = undefined; // the suggestions replace an earlier search, as a search replaces them
    }
    redraw();
  };
  // Close suggestions / Close results: the list and the repository open in it go away, the page shrinks back
  const closeRepos = () => {
    suggested = undefined;
    results = undefined;
    openRepo = "";
    variants = undefined;
    redraw();
  };
  // the fit badge (short, the full sentence as its tooltip) and, when the model will not run well, the sentence in the row
  const fitBadge = (fit: FitWords | undefined, testId?: string) =>
    fit ? (
      <Badge tone={fit.tone} title={fit.long} testId={testId}>
        {fit.text}
      </Badge>
    ) : null;
  const fitWhy = (fit: FitWords | undefined) =>
    fit && fit.tone !== "ok" ? (
      <span class="what" data-testid="local-fit-why">
        {fit.long}
      </span>
    ) : null;

  const jobRow = (job: LocalJob, testId: string, onCancel: () => void) => {
    const f = jobFraction(job);
    return (
      <li
        key={job.target}
        mix={rowCss}
        data-testid={testId}
        data-id={job.target}
        data-state={job.state}
      >
        <span class="name">{job.target}</span>
        <Badge
          tone={
            job.state === "running"
              ? "info"
              : job.state === "done"
                ? "ok"
                : job.state === "failed"
                  ? "err"
                  : "muted"
          }
        >
          {job.state}
        </Badge>
        <span class="acts">
          <Button
            size="sm"
            variant="ghost"
            disabled={act.busy !== ""}
            mix={on("click", onCancel)}
          >
            {job.state === "running" ? "Cancel" : "Clear"}
          </Button>
        </span>
        {job.state === "running" ? (
          <span class="bar">
            <Meter value={f} label={`${job.target}: ${progressText(job)}`} />
            <span mix={muted}>
              {progressText(job)}
              {job.file ? ` · ${job.file}` : ""}
            </span>
          </span>
        ) : job.error ? (
          <span class="what">{job.error}</span>
        ) : null}
      </li>
    );
  };

  return () => {
    const v = q.data;
    const busy = act.busy !== "" || stopAct.busy !== "";
    if (!v) {
      return (
        <Section
          title="Models on this PC"
          testId="local-models"
          description={<>Run open models with llama.cpp on this computer. Drydock downloads and runs llama.cpp itself; nothing leaves the PC, and the server joins the providers below. <LearnMore topic="local-models" /></>}
        >
          <ErrorNote error={q.error} what="Models on this PC" />
          {q.loading ? <Loading /> : null}
        </Section>
      );
    }
    if (!v.supported) {
      // a PC where local models cannot run (not Windows): the reason only; the rest of the view may be absent
      return (
        <Section
          title="Models on this PC"
          testId="local-models"
          description={<>Run open models with llama.cpp on this computer. Drydock downloads and runs llama.cpp itself; nothing leaves the PC, and the server joins the providers below. <LearnMore topic="local-models" /></>}
        >
          <ErrorNote error={q.error} what="Models on this PC" />
          <div mix={cardCss} data-testid="local-runtime">
            <p mix={muted}>
              {v.reason || "Local models are not available here."}
            </p>
          </div>
        </Section>
      );
    }
    const label = (id: string) =>
      v.backends.find((b) => b.id === id)?.label ?? id;
    const job = v.runtime.job;
    const installing = job?.state === "running";
    const srcId = source || v.settings.source || v.sources[0]?.id || "";
    const src = v.sources.find((x) => x.id === srcId);
    const chosenBackend: LocalBackend | "" =
      backend || v.settings.backend || v.suggestedBackend;
    const server = v.server;
    const serverOn = server.state === "running" || server.state === "starting";
    if (!serverOn) restartNeeded = false;
    const installedSrc = v.sources.find((x) => x.id === v.runtime.source);
    const cacheTypes =
      (v.runtime.installed ? installedSrc : src)?.cacheTypes ?? [];
    // a start of this window is waiting for the core, or the core says the server is starting (another window, the autostart)
    const serving = act.busy.startsWith("serve");
    const starting = server.state === "starting" || serving;
    // Stop was pressed for that start: it only has to deliver its answer now (Serve waits for it; Stop is not offered twice)
    const ending = serving && cancelledStart;
    const repos = results ?? suggested;

    return (
      <Section
        title="Models on this PC"
        testId="local-models"
        description={<>Run open models with llama.cpp on this computer. Drydock downloads and runs llama.cpp itself; nothing leaves the PC, and the server joins the providers below. <LearnMore topic="local-models" /></>}
      >
        <ErrorNote error={q.error ?? act.error ?? stopAct.error} what="Models on this PC" />
        {stoppedNote ? (
          <Notice tone="info" role="status" testId="local-stopped">
            The server was stopped.
          </Notice>
        ) : null}
        <>
          {/* the runtime: the llama.cpp build on this PC */}
          <div
            mix={cardCss}
            data-testid="local-runtime"
            data-installed={String(v.runtime.installed)}
          >
            {v.runtime.installed ? (
              <>
                <div class="line">
                  <h3>{runtimeLine(v.runtime, label)}</h3>
                  <Badge tone="ok">installed</Badge>
                  {v.runtime.installedAt ? (
                    <span mix={muted}>
                      installed {formatDate(v.runtime.installedAt)}
                    </span>
                  ) : null}
                  <span class="acts">
                    <Button
                      size="sm"
                      data-testid="local-update"
                      disabled={busy || installing}
                      mix={on(
                        "click",
                        () =>
                          void install(
                            v.settings.source,
                            v.settings.backend || v.runtime.backend || "",
                          ),
                      )}
                    >
                      Update
                    </Button>
                    {confirmRemove ? (
                      <>
                        <Button
                          size="sm"
                          variant="danger"
                          disabled={busy || installing}
                          data-testid="local-runtime-remove-confirm"
                          mix={on("click", async () => {
                            await change("remove-runtime", () =>
                              s.client.localRuntimeRemove(),
                            );
                            confirmRemove = false;
                            redraw();
                          })}
                        >
                          Yes, remove llama.cpp
                        </Button>
                        <Button
                          size="sm"
                          mix={on("click", () => {
                            confirmRemove = false;
                            redraw();
                          })}
                        >
                          Keep
                        </Button>
                      </>
                    ) : (
                      <Button
                        size="sm"
                        variant="ghost"
                        data-testid="local-runtime-remove"
                        disabled={busy || installing}
                        mix={on("click", () => {
                          confirmRemove = true;
                          redraw();
                        })}
                      >
                        Remove
                      </Button>
                    )}
                  </span>
                </div>
                <p mix={muted}>
                  Downloaded and run by Drydock from its own folder; Update
                  fetches the newest build the same way.
                </p>
                {v.runtime.devices.probed ? (
                  gpus(v.runtime.devices.devices).length > 0 ? (
                    <ul aria-label="Devices" data-testid="local-devices">
                      {v.runtime.devices.devices.map((d) => (
                        <li
                          key={d.id}
                          data-testid="local-device"
                          data-id={d.id}
                        >
                          {deviceLine(d)}
                        </li>
                      ))}
                    </ul>
                  ) : (
                    <p mix={muted} data-testid="local-devices">
                      No GPU found: runs on the CPU
                    </p>
                  )
                ) : v.runtime.devices.error ? (
                  <p mix={muted} data-testid="local-devices">
                    Devices: {v.runtime.devices.error}
                  </p>
                ) : null}
              </>
            ) : (
              <>
                <div class="line">
                  <h3>Install llama.cpp</h3>
                  <Badge>built in</Badge>
                </div>
                <p>
                  Drydock downloads and runs llama.cpp itself: the newest
                  Windows build of the source below, for the backend you pick,
                  checked against the digest GitHub recorded and unpacked into
                  Drydock&apos;s own folder. Nothing has to be installed by
                  hand, and nothing is added to the PC&apos;s PATH; the models
                  come from the Hugging Face Hub the same way.
                </p>
                <div mix={pickCss}>
                  <Field
                    label="Backend"
                    hint={v.backends.find((b) => b.id === chosenBackend)?.for}
                  >
                    {(id) => (
                      <Select
                        id={id}
                        testId="local-backend"
                        value={chosenBackend}
                        disabled={busy || installing}
                        options={v.backends.map((b) => ({
                          value: b.id,
                          label: b.label,
                          hint:
                            b.id === v.suggestedBackend
                              ? "suggested for this PC"
                              : undefined,
                        }))}
                        onChange={(val) => {
                          backend = val as LocalBackend;
                          redraw();
                        }}
                      />
                    )}
                  </Field>
                  <Field
                    label="Source"
                    hint="The official weekly build is the default. The TurboQuant fork is a second build you can pick; it adds the turbo4 and turbo3 cache types."
                  >
                    {(id) => (
                      <Select
                        id={id}
                        testId="local-source"
                        value={srcId}
                        disabled={busy || installing}
                        options={v.sources.map((x) => ({
                          value: x.id,
                          label: x.name,
                        }))}
                        onChange={(val) => {
                          source = val;
                          redraw();
                        }}
                      />
                    )}
                  </Field>
                </div>
                {src?.warning ? (
                  <Notice tone="warn" testId="local-source-warning">
                    {src.warning}
                  </Notice>
                ) : null}
                <div class="line">
                  <Button
                    variant="primary"
                    size="sm"
                    data-testid="local-install"
                    disabled={busy || installing}
                    mix={on("click", () => void install(srcId, chosenBackend))}
                  >
                    {installing ? "Installing…" : "Install llama.cpp"}
                  </Button>
                </div>
              </>
            )}
            {job ? (
              <div
                data-testid="local-job"
                data-state={job.state}
                style={{ display: "grid", gap: "4px" }}
              >
                {job.state === "running" ? (
                  <>
                    <Meter
                      value={jobFraction(job)}
                      label={`${job.target}: ${progressText(job)}`}
                    />
                    <span mix={muted}>
                      {job.target}: {progressText(job)}
                      {job.file ? ` · ${job.file}` : ""}
                    </span>
                  </>
                ) : job.state === "failed" ? (
                  <div class="line">
                    <ErrorNote
                      error={job.error}
                      what={`${job.target} failed`}
                    />
                    <Button
                      size="sm"
                      data-testid="local-retry"
                      disabled={busy}
                      mix={on(
                        "click",
                        () =>
                          void install(
                            v.settings.source,
                            v.settings.backend || chosenBackend,
                          ),
                      )}
                    >
                      Retry
                    </Button>
                  </div>
                ) : (
                  <span mix={muted}>
                    {job.target}: {job.phase || job.state}
                  </span>
                )}
              </div>
            ) : null}
          </div>

          {/* the models on this PC, the downloads running, and the Hub to add one */}
          <div mix={cardCss} data-testid="local-models-card">
            <h3>Models</h3>
            {v.models.length === 0 && v.downloads.length === 0 ? (
              <p mix={muted}>No model yet. Add one below.</p>
            ) : null}
            {v.models.length > 0 ? (
              <ul aria-label="Downloaded models">
                {v.models.map((m) => {
                  const fit = fitText(m.fit, m.sizeBytes);
                  const served = server.modelId === m.id && serverOn;
                  return (
                    <li
                      key={m.id}
                      mix={rowCss}
                      data-testid="local-model"
                      data-id={m.id}
                    >
                      <span class="name">{m.id}</span>
                      <span mix={muted}>{gb(m.sizeBytes)}</span>
                      {fitBadge(fit, "local-fit")}
                      {served ? <Badge tone="ok">serving</Badge> : null}
                      <span class="acts">
                        <Button
                          size="sm"
                          variant="primary"
                          data-testid="local-serve"
                          disabled={
                            busy || starting || served || !v.runtime.installed
                          }
                          mix={on("click", () => void serve(m.id))}
                        >
                          {act.busy === `serve:${m.id}` ? "Starting…" : "Serve"}
                        </Button>
                        <ConfirmAction
                          label="Remove"
                          yes="Yes, remove"
                          yesName={`Yes, remove the model ${m.id}`}
                          keepName={`Keep the model ${m.id}`}
                          question={`Remove the model ${m.id}? ${gb(m.sizeBytes)} are deleted from this PC, and getting it back means downloading it all again.`}
                          disabled={busy}
                          testId="local-model-remove"
                          onConfirm={() =>
                            change(`remove:${m.id}`, () =>
                              s.client.localModelRemove(m.id),
                            )
                          }
                        />
                      </span>
                      {fitWhy(fit)}
                    </li>
                  );
                })}
              </ul>
            ) : null}
            {v.downloads.length > 0 ? (
              <ul aria-label="Downloads">
                {v.downloads.map((d) =>
                  jobRow(
                    d,
                    "local-download-row",
                    () =>
                      void change(`cancel:${d.target}`, () =>
                        s.client.localModelDownloadCancel(d.target),
                      ),
                  ),
                )}
              </ul>
            ) : null}

            <h3>Add a model</h3>
            <div class="line">
              <form
                mix={[
                  searchCss,
                  on("submit", (e) => {
                    e.preventDefault();
                    void search();
                  }),
                ]}
                data-testid="local-search"
              >
                <input
                  type="search"
                  aria-label="Search the Hugging Face Hub"
                  data-testid="local-search-input"
                  placeholder="Search GGUF repositories on the Hugging Face Hub"
                  value={searchText}
                  spellcheck={false}
                  mix={on("input", (e) => {
                    searchText = e.currentTarget.value;
                    redraw();
                  })}
                />
                <Button
                  type="submit"
                  size="sm"
                  disabled={busy || !searchText.trim()}
                >
                  {act.busy === "search" ? "Searching…" : "Search"}
                </Button>
              </form>
              {suggested && !results ? null : (
                <Button
                  size="sm"
                  variant="ghost"
                  data-testid="local-suggested"
                  disabled={busy}
                  mix={on("click", () => void suggest())}
                >
                  {act.busy === "suggested" ? "Loading…" : "Show suggestions"}
                </Button>
              )}
              {repos ? (
                <Button
                  size="sm"
                  variant="ghost"
                  data-testid="local-repos-close"
                  disabled={busy}
                  mix={on("click", closeRepos)}
                >
                  {results ? "Close results" : "Close suggestions"}
                </Button>
              ) : null}
            </div>
            {repos ? (
              <ul
                aria-label={results ? "Search results" : "Suggested models"}
                data-testid="local-repos"
              >
                {repos.length === 0 ? (
                  <li mix={muted}>Nothing found.</li>
                ) : null}
                {repos.map((r) => (
                  <li key={r.id}>
                    <button
                      type="button"
                      mix={[repoBtn, on("click", () => void open(r))]}
                      aria-pressed={openRepo === r.id ? "true" : "false"}
                      data-testid="local-repo"
                      data-id={r.id}
                    >
                      <span class="id">{r.id}</span>
                      <span mix={muted}>
                        {compactCount(r.downloads)} downloads
                      </span>
                      {(r.tags ?? []).map((tag) => (
                        <Badge key={tag}>{tag}</Badge>
                      ))}
                    </button>
                    {openRepo === r.id ? (
                      variants ? (
                        <ul
                          aria-label={`Files of ${r.id}`}
                          style={{ marginTop: "8px" }}
                        >
                          {variants.length === 0 ? (
                            <li mix={muted}>
                              No GGUF file the Hub has a checksum for.
                            </li>
                          ) : null}
                          {variants.map((va) => {
                            const fit = fitText(va.fit, va.sizeBytes);
                            const tooBig = va.fit?.target === "none";
                            return (
                              <li
                                key={va.name}
                                mix={rowCss}
                                data-testid="local-variant"
                                data-name={va.name}
                              >
                                <span class="name">{va.name}</span>
                                <span mix={muted}>{gb(va.sizeBytes)}</span>
                                {fitBadge(fit, "local-fit")}
                                <span class="acts">
                                  <Button
                                    size="sm"
                                    variant="primary"
                                    data-testid="local-download"
                                    disabled={busy || tooBig}
                                    title={
                                      tooBig
                                        ? "Too big for this PC: Drydock does not download a model llama.cpp could not load."
                                        : undefined
                                    }
                                    mix={on(
                                      "click",
                                      () =>
                                        void change(`download:${va.name}`, () =>
                                          s.client.localModelDownload(
                                            r.id,
                                            va.name,
                                          ),
                                        ),
                                    )}
                                  >
                                    Download
                                  </Button>
                                </span>
                                {fitWhy(fit)}
                                {(va.notes ?? []).length > 0 ? (
                                  <span class="what">
                                    {(va.notes ?? []).join(" ")}
                                  </span>
                                ) : null}
                              </li>
                            );
                          })}
                        </ul>
                      ) : (
                        <Loading label="Reading the repository…" />
                      )
                    ) : null}
                  </li>
                ))}
              </ul>
            ) : null}
          </div>

          {/* the server */}
          <div
            mix={cardCss}
            data-testid="local-server"
            data-state={server.state}
          >
            <div class="line">
              <h3>Server</h3>
              <Badge
                tone={serverTone(server.state)}
                testId="local-server-state"
              >
                {SERVER_WORDS[server.state] ?? server.state}
              </Badge>
              {server.modelId ? <span mix={mono}>{server.modelId}</span> : null}
              {server.baseUrl ? (
                <code data-testid="local-server-url">{server.baseUrl}</code>
              ) : null}
              <span class="acts">
                <Button
                  size="sm"
                  variant="ghost"
                  data-testid="local-log"
                  mix={on("click", () => ((showLog = !showLog), redraw()))}
                >
                  {showLog ? "Hide log" : "Log"}
                </Button>
                {serverOn || (serving && !ending) || stopAct.busy !== "" ? (
                  <Button
                    size="sm"
                    variant="danger"
                    data-testid="local-server-stop"
                    // while the model loads, the start holds the section for up to 15 minutes: Stop is the one button that
                    // works meanwhile, and says it ends the start
                    disabled={stopAct.busy !== "" || (busy && !serving)}
                    mix={on("click", () => void stop())}
                  >
                    {stopAct.busy !== ""
                      ? "Stopping…"
                      : starting && !ending
                        ? "Cancel start"
                        : "Stop"}
                  </Button>
                ) : null}
              </span>
            </div>
            {server.error ? (
              <ErrorNote error={server.error} what="Server" />
            ) : null}
            {v.settings.providerId ? (
              <p mix={muted} data-testid="local-provider-note">
                Registered as the provider &apos;Models on this PC
                (llama.cpp)&apos; below.
              </p>
            ) : null}
            {restartNeeded ? (
              <Notice tone="info" testId="local-restart">
                Restart the server to apply.
              </Notice>
            ) : null}
            {showLog ? (
              <pre data-testid="local-log-lines">
                {v.log.length > 0
                  ? v.log.slice(-200).join("\n")
                  : "No output yet."}
              </pre>
            ) : null}
          </div>

          {/* settings */}
          <div mix={cardCss} data-testid="local-settings">
            <h3>Settings</h3>
            <div mix={pickCss}>
              <Field
                label="KV cache type"
                hint="q8_0: near-lossless, half the KV memory of f16. q4_0: a quarter, with llama.cpp's Hadamard rotation (most of TurboQuant's gain)"
              >
                {(id) => (
                  <Select
                    id={id}
                    testId="local-cache"
                    value={v.settings.cacheType}
                    disabled={busy}
                    options={selectValues(
                      cacheTypes.includes(v.settings.cacheType)
                        ? cacheTypes
                        : [v.settings.cacheType, ...cacheTypes],
                    )}
                    onChange={(val) => void setSetting({ cacheType: val })}
                  />
                )}
              </Field>
              <Field
                label="Context size"
                hint="Tokens; 0 is the model's own. Coding agents need 64k or more."
              >
                {(id) => (
                  <input
                    id={id}
                    type="number"
                    min="0"
                    step="1024"
                    data-testid="local-context"
                    value={String(v.settings.contextSize)}
                    disabled={busy}
                    mix={on("change", (e) => {
                      const n = wholeNumber(e.currentTarget.value);
                      if (n !== undefined && n !== v.settings.contextSize)
                        void setSetting({ contextSize: n });
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
                    mix={on("change", (e) => {
                      const n = wholeNumber(e.currentTarget.value);
                      if (n !== undefined && n !== v.settings.port)
                        void setSetting({ port: n });
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
            <p mix={muted} data-testid="local-memory">
              {memoryLine(v)}
            </p>
          </div>
        </>
      </Section>
    );
  };
}
