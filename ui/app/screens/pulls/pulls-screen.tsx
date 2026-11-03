// Pull Requests: the GitHub pull request of each task that has one (CI and reviews, read from GitHub through the core), and
// the open GitHub issues, read when the page opens: Import and start (the crew's pair, started in one click) or Import as task
// (a backlog task), and every listed issue as backlog tasks at once (C11). Nothing here merges: a pull request is merged through GitHub when the human
// approves the task's "Review and merge" request (only the reviewed commit).

import { css, on, type Handle } from 'remix/component';
import { GitPullRequest, Import, RefreshCw } from 'lucide';
import { projectHasRepo, type GitHubIssue, type TaskCreateResult } from '../../api/types.ts';
import { routes } from '../../routes.ts';
import { query, services, watch } from '../../state/services.tsx';
import { Button } from '../../ui/button.tsx';
import { StatusChip, muted } from '../../ui/data.tsx';
import { Empty, ErrorNote, Loading } from '../../ui/feedback.tsx';
import { Field } from '../../ui/form.tsx';
import { Select } from '../../ui/select.tsx';
import { Page, Section } from '../../ui/layout.tsx';
import { t } from '../../ui/tokens.ts';
import { TaskTotalNotice } from '../shared/task-total.tsx';
import { pickPair } from '../workspace/start.ts';
import { IssueItem, PullRequestPanel } from './pr-parts.tsx';
import { bulkImportLine, hasLabels, importedTask, importLine, ISSUES_FRESH_MS, labelList } from './pulls.ts';

const pickerCss = css({
  display: 'flex',
  alignItems: 'flex-end',
  flexWrap: 'wrap',
  gap: t.s3,
  '& > div:first-child': { flex: '1 1 320px', maxWidth: '560px' },
  '& select': { width: '100%' },
  '& > a': { marginBottom: '6px', fontSize: t.textSm },
  '& > span': { marginBottom: '5px' },
});

const loadCss = css({
  display: 'flex',
  alignItems: 'flex-end',
  flexWrap: 'wrap',
  gap: t.s3,
  '& > div:first-child': { flex: '0 1 320px' },
  '& input': { width: '100%' },
});

const statusCss = css({ margin: '0', fontSize: t.textSm });

/** The issues read per client and project, with when (a test's services are a new client, so nothing is shared between tests). */
const issueCaches = new WeakMap<object, Map<string, { at: number; list: GitHubIssue[] }>>();
function issueCacheOf(client: object): Map<string, { at: number; list: GitHubIssue[] }> {
  let m = issueCaches.get(client);
  if (!m) issueCaches.set(client, (m = new Map()));
  return m;
}

/** The section's line: what Import and start does with whom, or what it needs. */
function importDescription(pair: { workerName: string; reviewerName: string } | undefined): string {
  const start = pair
    ? `Import and start gives the issue to ${pair.workerName}, who builds it, and ${pair.reviewerName}, who reviews it, and starts it.`
    : 'Import and start needs a worker and a different reviewer in the crew.';
  return `The open issues of this repository's GitHub origin. ${start} Import as task makes a backlog task. The issue text is quoted as data.`;
}

const issuesCss = css({ margin: '0', padding: '0', listStyle: 'none', border: `1px solid ${t.border}`, borderRadius: t.rLg, background: t.bgSurface, overflow: 'hidden' });

/** `onImported` hears of each task an import made (the page stays where it is, C11). */
export function PullsScreen(handle: Handle<{ onImported?: (task: TaskCreateResult) => void }>) {
  const s = services(handle);
  watch(handle, s.project);
  watch(handle, s.workspace);
  let selected: string | undefined;
  const withPR = () => s.project.taskList.filter((x) => x.prRef);
  const currentId = () => {
    const list = withPR();
    return list.some((x) => x.id === selected) ? selected : list[0]?.id;
  };
  const pr = query(handle, {
    load: () => s.client.githubPullRequest(currentId()!),
    key: () => currentId(),
    enabled: () => !!currentId(),
    when: (e) => e.type.startsWith('github.') && e.taskId === currentId(),
    debounceMs: 300,
  });

  // the open issues (C11): read when the page opens, kept a minute per project (ISSUES_FRESH_MS), filtered by label here
  let issues: GitHubIssue[] | undefined;
  let issuesFor: string | undefined;
  let labels = '';
  let issueError: Error | undefined;
  let loadingIssues = false;
  let importing: number | 'all' | undefined;
  let importError: Error | undefined;
  let status: { text: string; task?: { id: string; seq: number } } | undefined;
  // the tasks imported on this page, by project and issue number: an issue number is a project's own
  const madeHere = new Map<string, { id: string; seq: number }>();
  const madeKey = (projectId: string, n: number) => `${projectId}#${n}`;
  // the issues a bulk import was refused for: still listed, with their own buttons, but not in the next bulk import
  const refused = new Set<string>();

  const loadIssues = async (fresh: boolean) => {
    const projectId = s.project.id;
    if (!projectId || loadingIssues) return;
    if (projectId !== issuesFor) {
      // another project: nothing of the last one stays on screen (its issues' numbers would import this project's issues)
      issues = undefined;
      issueError = undefined;
      importError = undefined;
      status = undefined;
    }
    issuesFor = projectId;
    const cache = issueCacheOf(s.client);
    const kept = cache.get(projectId);
    if (!fresh && kept && Date.now() - kept.at < ISSUES_FRESH_MS) {
      issues = kept.list;
      void handle.update();
      return;
    }
    loadingIssues = true;
    issueError = undefined;
    void handle.update();
    try {
      const list = await s.client.githubIssues(projectId);
      cache.set(projectId, { at: Date.now(), list });
      if (s.project.id === projectId) issues = list;
    } catch (e) {
      if (s.project.id === projectId) issueError = e instanceof Error ? e : new Error(String(e));
    } finally {
      loadingIssues = false;
      void handle.update();
    }
  };

  const pairNames = () => {
    const agents = s.project.agentList;
    const pair = pickPair(agents);
    const name = (id: string) => agents.find((a) => a.id === id)?.name ?? id;
    return pair ? { ...pair, workerName: name(pair.worker), reviewerName: name(pair.reviewer) } : undefined;
  };
  const taskOf = (n: number) => (s.project.id ? madeHere.get(madeKey(s.project.id, n)) : undefined) ?? importedTask(s.project.taskList, n);

  // one issue: a backlog task, or (start) assigned to the crew's pair and started; the page stays where it is
  const importOne = async (n: number, start: boolean) => {
    const projectId = s.project.id;
    const pair = start ? pairNames() : undefined;
    // the list shown must be this project's: while another project's issues load, nothing is imported
    if (!projectId || projectId !== issuesFor || loadingIssues || (start && !pair)) return undefined;
    const created = await s.client.githubImportIssue(projectId, n, pair ? { assignee: pair.worker, reviewer: pair.reviewer, start: true } : undefined);
    madeHere.set(madeKey(projectId, n), { id: created.id, seq: created.seq });
    handle.props.onImported?.(created);
    return { created, pair };
  };

  const doImport = async (n: number, start: boolean) => {
    importing = n;
    importError = undefined;
    status = undefined;
    void handle.update();
    try {
      const r = await importOne(n, start);
      if (r) status = { text: importLine(n, r.created, r.pair && !r.created.startError ? { worker: r.pair.workerName, reviewer: r.pair.reviewerName } : undefined), task: { id: r.created.id, seq: r.created.seq } };
    } catch (e) {
      importError = e instanceof Error ? e : new Error(String(e));
    } finally {
      importing = undefined;
      void handle.update();
    }
  };

  // every listed issue not imported yet, as backlog tasks only (nothing starts); an issue that is refused (a high injection
  // risk, say) does not stop the ones after it, and is left out of the next bulk import
  const importAll = async (ns: number[]) => {
    importing = 'all';
    importError = undefined;
    status = undefined;
    void handle.update();
    let done = 0;
    const failed: { n: number; why: string }[] = [];
    try {
      for (const n of ns) {
        try {
          if (await importOne(n, false)) done++;
        } catch (e) {
          failed.push({ n, why: e instanceof Error ? e.message : String(e) });
          refused.add(madeKey(issuesFor ?? '', n));
        }
      }
    } finally {
      importing = undefined;
      status = { text: bulkImportLine(done, failed) };
      void handle.update();
    }
  };

  return () => {
    const project = s.workspace.project;
    if (project && !projectHasRepo(project)) {
      return (
        <Page title="Pull requests" icon={GitPullRequest}>
          <Empty title="No repository yet" icon={GitPullRequest}>
            <p data-testid="pulls-no-repo">
              Attach a GitHub repository to see pull requests. <a href={routes.project.href()}>Open Project settings</a>
            </p>
          </Empty>
        </Page>
      );
    }
    if (s.project.id && issuesFor !== s.project.id && !loadingIssues) handle.queueTask(() => void loadIssues(false));
    const pair = pairNames();
    const wanted = labelList(labels);
    const shown = (issues ?? []).filter((i) => hasLabels(i, wanted));
    const waiting = shown.filter((i) => !taskOf(i.number));
    const bulk = waiting.filter((i) => !refused.has(madeKey(s.project.id ?? '', i.number)));
    const list = withPR();
    const taskId = currentId();
    const current = list.find((x) => x.id === taskId);
    return (
      <Page title="Pull requests" icon={GitPullRequest} subtitle={list.length > 0 ? `${list.length} open from tasks` : undefined} guide="delivery">
        {/* said once (decision P144): the section's line while there are pull requests, the empty state's while there are none */}
        <Section title="Pull requests of tasks" description={list.length > 0 ? 'A finished task is pushed and a pull request opened. It merges after its CI passed and you approved.' : undefined}>
          <ErrorNote error={s.project.tasks.error} what="Could not load tasks" />
          <TaskTotalNotice>The pull requests of older tasks are not listed here.</TaskTotalNotice>
          {list.length === 0 ? (
            <Empty title="No task has a pull request" icon={GitPullRequest}>
              <p data-testid="pulls-empty-line">
                A finished task opens one when the project delivers by pull request (<a href={routes.project.href()}>Project settings</a>).
              </p>
            </Empty>
          ) : (
            <>
              <div mix={pickerCss}>
                <Field label="Task">
                  {(id) => (
                    <Select
                      id={id}
                      label="Task"
                      value={taskId ?? ''}
                      options={list.map((x) => ({ value: x.id, label: `#${x.seq} ${x.title} (${x.prRef})` }))}
                      onChange={(v) => {
                        selected = v;
                        void handle.update();
                      }}
                    />
                  )}
                </Field>
                {current ? <StatusChip status={current.status} /> : null}
                {current ? <a href={routes.task.href({ id: current.id })}>Open the task</a> : null}
              </div>
              <ErrorNote error={pr.error} what="Could not read the pull request from GitHub" />
              {pr.loading && !pr.data ? <Loading label="Loading from GitHub..." /> : null}
              {pr.data ? <PullRequestPanel state={pr.data} /> : null}
            </>
          )}
        </Section>

        <Section title="Import from GitHub issues" description={importDescription(pair)}>
          <form
            mix={[
              loadCss,
              on('submit', (e) => {
                e.preventDefault();
                void loadIssues(true);
              }),
            ]}
          >
            <Field label="Labels (comma separated, optional)">
              {(id) => <input id={id} placeholder="bug, good first issue" value={labels} mix={on('input', (e) => ((labels = e.currentTarget.value), void handle.update()))} />}
            </Field>
            <Button type="submit" icon={RefreshCw} disabled={loadingIssues || !s.project.id}>
              {loadingIssues ? 'Reading issues...' : 'Refresh issues'}
            </Button>
            {bulk.length > 1 ? (
              <Button icon={Import} disabled={importing !== undefined || loadingIssues} data-testid="gh-import-all" mix={on('click', () => void importAll(bulk.map((i) => i.number)))}>
                {importing === 'all' ? 'Importing...' : `Import ${bulk.length} as backlog tasks`}
              </Button>
            ) : null}
          </form>
          <ErrorNote error={issueError} what="Could not list issues" />
          <ErrorNote error={importError} what="Import failed" />
          {status ? (
            <p role="status" mix={statusCss} data-testid="gh-import-status">
              {status.text}
              {status.task ? (
                <>
                  {' '}
                  <a href={routes.task.href({ id: status.task.id })}>Open the task</a>
                </>
              ) : null}
            </p>
          ) : null}
          {loadingIssues && !issues ? <Loading label="Loading issues..." /> : null}
          {issues && shown.length === 0 ? (
            <Empty title={issues.length === 0 ? 'No open issues.' : 'No open issue has these labels.'} icon={Import}>
              <p mix={muted}>Nothing to import{wanted.length > 0 ? ' with these labels' : ''}.</p>
            </Empty>
          ) : null}
          {shown.length > 0 ? (
            <ul mix={issuesCss} aria-label="Open issues">
              {shown.map((i) => (
                <IssueItem
                  key={i.number}
                  issue={i}
                  imported={taskOf(i.number)}
                  busy={importing !== undefined || loadingIssues}
                  onImport={() => void doImport(i.number, false)}
                  onImportStart={pair ? () => void doImport(i.number, true) : undefined}
                />
              ))}
            </ul>
          ) : null}
        </Section>
      </Page>
    );
  };
}
