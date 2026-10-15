// The Repo map page (decision P62): the project's most important code as the agents' repo_map tool gives it. Aider's
// technique: tree-sitter definitions and references for every source file, a graph of which file uses which, PageRank
// (personalised toward the focus paths), and the definition lines of the top files within a token budget. The map is
// made on this computer from the repository's files; nothing leaves the machine. A build without CGO has no parser and
// says so.

import { css, on, type Handle } from 'remix/component';
import { FolderTree } from 'lucide';
import { REPO_MAP_DEFAULT_BUDGET, REPO_MAP_MAX_BUDGET, REPO_MAP_MIN_BUDGET } from '../../api/repomapClient.ts';
import { projectHasRepo } from '../../api/types.ts';
import { routes } from '../../routes.ts';
import { query, services, watch } from '../../state/services.tsx';
import { Button } from '../../ui/button.tsx';
import { Badge } from '../../ui/data.tsx';
import { ErrorNote, Loading, Notice } from '../../ui/feedback.tsx';
import { Field, formActions } from '../../ui/form.tsx';
import { Page, Section, card, tableCard } from '../../ui/layout.tsx';
import { t } from '../../ui/tokens.ts';
import { budgetOf, languagesText, parseFocus, rankText, skippedText, statsText } from './repomap.ts';

const introCss = css({ color: t.textMuted, fontSize: t.textMd, maxWidth: '820px' });
const formCss = css({
  display: 'grid',
  gridTemplateColumns: 'minmax(220px, 1fr) 160px',
  gap: t.s4,
  alignItems: 'start',
  '& input': { width: '100%', minWidth: '0' },
  '& input.paths': { fontFamily: t.mono, fontSize: t.textSm },
});
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

export function RepoMapScreen(handle: Handle) {
  const s = services(handle);
  watch(handle, s.workspace);
  // what the fields say, and what the shown map was made with (the query re-reads when the latter changes)
  let focusText = '';
  let budgetText = String(REPO_MAP_DEFAULT_BUDGET);
  let applied = { focus: [] as string[], budget: REPO_MAP_DEFAULT_BUDGET };
  const q = query(handle, {
    load: () => s.client.repoMapGet(s.workspace.projectId ?? '', applied.focus, applied.budget),
    key: () => [s.workspace.projectId, applied.focus, applied.budget],
    enabled: () => !!s.workspace.projectId && projectHasRepo(s.workspace.project),
  });
  const submit = () => {
    const next = { focus: parseFocus(focusText), budget: budgetOf(budgetText) };
    budgetText = String(next.budget);
    if (JSON.stringify(next) === JSON.stringify(applied)) q.reload();
    else applied = next;
    void handle.update();
  };

  return () => {
    const project = s.workspace.project;
    const hasRepo = projectHasRepo(project);
    const v = q.data;
    const r = v?.result;
    const skipped = r ? skippedText(r.skipped) : '';
    return (
      <Page title="Repo map" icon={FolderTree} testId="repo-map" subtitle={project?.name}>
        <p mix={introCss}>
          The most important code of the project's repository, as agents get it from the repo_map tool: files ranked by how much the rest of the code uses them, each with
          its most important definition lines, within a token budget. Focus paths rank what those files use higher. It is made on this computer from the repository's files
          (.gitignore respected); nothing leaves the machine.
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
            // the budget is kept inside its range when the map is made (budgetOf); the browser's own check would silently
            // refuse to submit a budget off the 256 steps
            noValidate
          >
            <div mix={formCss}>
              <Field label="Focus paths" hint="Files or folders relative to the repository, separated by commas. Empty: the whole repository evenly.">
                {(id) => (
                  <input
                    id={id}
                    class="paths"
                    type="text"
                    placeholder="internal/core, ui/app/screens"
                    value={focusText}
                    data-testid="repomap-focus"
                    mix={on('input', (e) => {
                      focusText = e.currentTarget.value;
                    })}
                  />
                )}
              </Field>
              <Field label="Token budget" hint={`${REPO_MAP_MIN_BUDGET} to ${REPO_MAP_MAX_BUDGET}`}>
                {(id) => (
                  <input
                    id={id}
                    type="number"
                    min={String(REPO_MAP_MIN_BUDGET)}
                    max={String(REPO_MAP_MAX_BUDGET)}
                    step="256"
                    value={budgetText}
                    data-testid="repomap-budget"
                    mix={on('input', (e) => {
                      budgetText = e.currentTarget.value;
                    })}
                  />
                )}
              </Field>
              <div mix={formActions}>
                <Button type="submit" variant="primary" icon={FolderTree} disabled={q.loading} data-testid="repomap-make">
                  {q.loading ? 'Mapping...' : 'Make the map'}
                </Button>
              </div>
            </div>
          </form>
        ) : null}
        <ErrorNote error={q.error} what="Repo map" />
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
                    : `${r.files} source files were read, but not even the most important definition fits in ${r.budget} tokens. Try a larger token budget.`}
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
