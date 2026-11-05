// The Repo map page (decision P62): the project's most important code as the agents' repo_map tool gives it. Aider's
// technique: tree-sitter definitions and references for every source file, a graph of which file uses which, PageRank
// (personalised toward the focus paths), and the definition lines of the top files within a token budget. The map is
// made on this computer from the repository's files; nothing leaves the machine. A build without CGO has no parser and
// says so.

import { css, on, type Handle } from 'remix/component';
import { FolderTree } from 'lucide';
import { REPO_MAP_DEFAULT_BUDGET } from '../../api/repomapClient.ts';
import { projectHasRepo } from '../../api/types.ts';
import { routes } from '../../routes.ts';
import { query, services, watch } from '../../state/services.tsx';
import { Button } from '../../ui/button.tsx';
import { Badge } from '../../ui/data.tsx';
import { ErrorNote, Loading, Notice } from '../../ui/feedback.tsx';
import { Field, formActions } from '../../ui/form.tsx';
import { Page, Section, card, tableCard } from '../../ui/layout.tsx';
import { t } from '../../ui/tokens.ts';
import { Segmented } from '../../ui/segmented.tsx';
import { BUDGET_CHOICES, doubled, languagesText, parseFocus, rankText, repoMapPrefsOf, skippedText, statsText, taskFocusChips, toggleFocus, withRepoMapPrefs, type RepoMapPrefs } from './repomap.ts';

const introCss = css({ color: t.textMuted, fontSize: t.textMd, maxWidth: '820px' });
const formCss = css({
  display: 'grid',
  gridTemplateColumns: 'minmax(220px, 1fr) auto',
  gap: t.s4,
  alignItems: 'start',
  '& input': { width: '100%', minWidth: '0' },
  '& input.paths': { fontFamily: t.mono, fontSize: t.textSm },
});
const budgetCss = css({ display: 'grid', gap: '6px', '& .lbl': { fontSize: t.textSm, fontWeight: '500' } });
const chipsCss = css({ display: 'flex', flexWrap: 'wrap', alignItems: 'center', gap: t.s2, marginTop: t.s2, fontSize: t.textSm, color: t.textMuted });
const statsCss = css({ color: t.textMuted, fontSize: t.textSm, margin: '0', display: 'grid', gap: '2px' });
const mapCss = css({
  margin: '0',
  padding: `${t.s3} ${t.s4}`,
  border: `1px solid ${t.border}`,
  borderRadius: t.rLg,
  background: t.bgSurface,
  fontFamily: t.mono,
  fontSize: t.textSm,
  lineHeight: '1.5',
  overflow: 'auto',
  maxHeight: '70vh',
  whiteSpace: 'pre',
});
const pathCss = css({ fontFamily: t.mono, fontSize: t.textSm, wordBreak: 'break-all' });
const numCss = css({ textAlign: 'right', fontVariantNumeric: 'tabular-nums' });

/** How many ranked files the table lists. */
const TABLE_ROWS = 30;

// each project's focus and budget as the person last made the map (a convenience: unreadable storage is the plain map)
const PREFS_KEY = 'drydock.repomap';

function readPrefsStore(): unknown {
  try {
    const text = localStorage.getItem(PREFS_KEY);
    return text ? JSON.parse(text) : undefined;
  } catch {
    return undefined;
  }
}

function writePrefs(projectId: string, prefs: RepoMapPrefs): void {
  try {
    localStorage.setItem(PREFS_KEY, JSON.stringify(withRepoMapPrefs(readPrefsStore(), projectId, prefs)));
  } catch {
    /* a convenience only */
  }
}

export function RepoMapScreen(handle: Handle) {
  const s = services(handle);
  watch(handle, s.workspace);
  watch(handle, s.project);
  // per project: what the focus field says, and what the shown map was made with (the query re-reads when the latter changes);
  // both start from what the person last made the map with in that project (X412)
  const per = new Map<string, { focusText: string; applied: RepoMapPrefs }>();
  const cur = () => {
    const pid = s.workspace.projectId ?? '';
    let v = per.get(pid);
    if (!v) {
      const applied = pid ? repoMapPrefsOf(readPrefsStore(), pid) : { focus: [], budget: REPO_MAP_DEFAULT_BUDGET };
      v = { focusText: applied.focus.join(', '), applied };
      per.set(pid, v);
    }
    return v;
  };
  const q = query(handle, {
    load: () => s.client.repoMapGet(s.workspace.projectId ?? '', cur().applied.focus, cur().applied.budget),
    key: () => [s.workspace.projectId, cur().applied.focus, cur().applied.budget],
    enabled: () => !!s.workspace.projectId && projectHasRepo(s.workspace.project),
  });
  // make the map with this focus and budget, and keep them for the project
  const make = (next: RepoMapPrefs) => {
    const c = cur();
    c.focusText = next.focus.join(', ');
    if (JSON.stringify(next) === JSON.stringify(c.applied)) q.reload();
    else c.applied = next;
    if (s.workspace.projectId) writePrefs(s.workspace.projectId, next);
    void handle.update();
  };
  const submit = () => make({ focus: parseFocus(cur().focusText), budget: cur().applied.budget });

  return () => {
    const project = s.workspace.project;
    const hasRepo = projectHasRepo(project);
    const v = q.data;
    const r = v?.result;
    const skipped = r ? skippedText(r.skipped) : '';
    const c = cur();
    const chips = taskFocusChips(s.project.taskList);
    const more = r ? doubled(r.budget) : undefined;
    return (
      <Page title="Repo map" icon={FolderTree} testId="repo-map" subtitle={project?.name} guide="knowledge">
        <p mix={introCss}>
          Files ranked by how much the rest of the code uses them, as agents get them from the repo_map tool. Made on this computer; nothing leaves it.
        </p>
        {!project ? <Notice tone="info">Choose a project to see its repo map.</Notice> : null}
        {project && !hasRepo ? (
          <Notice tone="warn" testId="repomap-no-repo">
            The repo map needs a repository. <a href={routes.project.href()}>Open Project settings</a> to attach one.
          </Notice>
        ) : null}
        {project && hasRepo ? (
          <form
            mix={[card, on('submit', (e) => {
              e.preventDefault();
              submit();
            })]}
            aria-label="Repo map options"
          >
            <div mix={formCss}>
              <Field label="Focus paths" hint="Files or folders relative to the repository, separated by commas. Empty: the whole repository evenly.">
                {(id) => (
                  <input
                    id={id}
                    class="paths"
                    type="text"
                    placeholder="internal/core, ui/app/screens"
                    value={c.focusText}
                    data-testid="repomap-focus"
                    mix={on('input', (e) => {
                      c.focusText = e.currentTarget.value;
                      void handle.update();
                    })}
                  />
                )}
              </Field>
              <div mix={budgetCss}>
                <span class="lbl" aria-hidden="true">
                  Token budget
                </span>
                <Segmented
                  label="Token budget"
                  testId="repomap-budget"
                  value={String(c.applied.budget)}
                  options={BUDGET_CHOICES.map((b) => ({ value: String(b.value), label: b.label, hint: `${b.value} tokens` }))}
                  disabled={q.loading}
                  onChange={(v) => make({ focus: parseFocus(c.focusText), budget: Number(v) })}
                />
              </div>
              <div mix={formActions}>
                <Button type="submit" variant="primary" icon={FolderTree} disabled={q.loading} data-testid="repomap-make">
                  {q.loading ? 'Mapping...' : 'Make the map'}
                </Button>
              </div>
            </div>
            {chips.length > 0 ? (
              <div mix={chipsCss} data-testid="repomap-task-chips">
                <span>Focus on:</span>
                {chips.map((ch) => (
                  <Button key={ch.id} size="sm" variant="ghost" title={ch.files.join(', ')} disabled={q.loading} mix={on('click', () => make({ focus: ch.files, budget: c.applied.budget }))}>
                    Files of task #{ch.seq}
                  </Button>
                ))}
              </div>
            ) : null}
          </form>
        ) : null}
        <ErrorNote error={q.error} retry={q.reload} what="Repo map" />
        {q.loading && !v ? <Loading label="Reading the repository..." /> : null}
        {v && !v.available ? (
          <Notice tone="warn" title="No repo map in this build" testId="repomap-unavailable">
            {v.message}
          </Notice>
        ) : null}
        {r ? (
          <>
            <div mix={statsCss} data-testid="repomap-stats">
              <span>{statsText(r)}</span>
              {Object.keys(r.languages).length ? <span>Languages: {languagesText(r.languages)}.</span> : null}
              {skipped ? <span>Not parsed: {skipped}.</span> : null}
              {r.focus.length ? <span>Focus: {r.focus.join(', ')}.</span> : null}
            </div>
            <Section title="Map" label="Map">
              {r.map ? (
                <pre mix={mapCss} data-testid="repomap-text" tabIndex={0} aria-label="Repo map text">
                  {r.map}
                </pre>
              ) : (
                <Notice tone="info" testId="repomap-empty">
                  {r.files === 0
                    ? `The repository has no source files in the languages the map reads (${v!.languages.join(', ')}).`
                    : `${r.files} source files were read, but not even the most important definition fits in ${r.budget} tokens.`}
                  {r.files > 0 && more ? (
                    <>
                      {' '}
                      <Button size="sm" disabled={q.loading} data-testid="repomap-double" mix={on('click', () => make({ focus: c.applied.focus, budget: more }))}>
                        Double the budget
                      </Button>
                    </>
                  ) : null}
                </Notice>
              )}
            </Section>
            {r.ranked.length ? (
              <Section title="Ranking" label="Ranking" description="PageRank over the files: how much the rest of the code depends on each.">
                <div mix={tableCard}>
                  <table aria-label="Ranked files" data-testid="repomap-ranking">
                    <thead>
                      <tr>
                        <th scope="col">File</th>
                        <th scope="col">Language</th>
                        <th scope="col" mix={numCss}>
                          Rank
                        </th>
                        <th scope="col" mix={numCss}>
                          Definitions
                        </th>
                        <th scope="col" mix={numCss}>
                          References
                        </th>
                        <th scope="col">In the map</th>
                        <th scope="col">
                          <span class="sr-only">Focus</span>
                        </th>
                      </tr>
                    </thead>
                    <tbody>
                      {r.ranked.slice(0, TABLE_ROWS).map((f) => (
                        <tr key={f.path}>
                          <td mix={pathCss}>
                            {f.path} {f.focus ? <Badge tone="accent">focus</Badge> : null}
                          </td>
                          <td>{f.lang}</td>
                          <td mix={numCss}>{rankText(f.rank)}</td>
                          <td mix={numCss}>{f.defs}</td>
                          <td mix={numCss}>{f.refs}</td>
                          <td>{f.shown ? 'yes' : 'no'}</td>
                          <td>
                            <Button
                              size="sm"
                              variant="ghost"
                              aria-pressed={c.applied.focus.includes(f.path) ? 'true' : 'false'}
                              aria-label={`Focus on ${f.path}`}
                              disabled={q.loading}
                              mix={on('click', () => make({ focus: toggleFocus(c.applied.focus, f.path), budget: c.applied.budget }))}
                            >
                              Focus
                            </Button>
                          </td>
                        </tr>
                      ))}
                    </tbody>
                  </table>
                </div>
              </Section>
            ) : null}
          </>
        ) : null}
      </Page>
    );
  };
}
