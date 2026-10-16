// Pull Requests: the GitHub pull request of each task that has one (CI and reviews, read from GitHub through the core), and
// importing open GitHub issues as backlog tasks. Nothing here merges: a pull request is merged through GitHub when the human
// approves the task's "Review and merge" request (only the reviewed commit).

import { css, navigate, on, type Handle } from 'remix/component';
import { CircleDot, GitPullRequest, Import } from 'lucide';
import { projectHasRepo, type GitHubIssue, type Task } from '../../api/types.ts';
import { routes } from '../../routes.ts';
import { query, services, watch } from '../../state/services.tsx';
import { Button } from '../../ui/button.tsx';
import { StatusChip, muted } from '../../ui/data.tsx';
import { Empty, ErrorNote, Loading } from '../../ui/feedback.tsx';
import { Field } from '../../ui/form.tsx';
import { Select } from '../../ui/select.tsx';
import { Page, Section } from '../../ui/layout.tsx';
import { t } from '../../ui/tokens.ts';
import { IssueItem, PullRequestPanel } from './pr-parts.tsx';

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

const issuesCss = css({ margin: '0', padding: '0', listStyle: 'none', border: `1px solid ${t.border}`, borderRadius: t.rLg, background: t.bgSurface, overflow: 'hidden' });

/** `onImported` replaces the navigation to the imported task (tests). */
export function PullsScreen(handle: Handle<{ onImported?: (task: Task) => void }>) {
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

  let issues: GitHubIssue[] | undefined;
  let labels = '';
  let issueError: Error | undefined;
  let loadingIssues = false;
  let importing: number | undefined;
  let importError: Error | undefined;

  const loadIssues = async () => {
    const projectId = s.project.id;
    if (!projectId) return;
    loadingIssues = true;
    issueError = undefined;
    void handle.update();
    try {
      issues = await s.client.githubIssues(projectId, labels.trim());
    } catch (e) {
      issueError = e instanceof Error ? e : new Error(String(e));
    } finally {
      loadingIssues = false;
      void handle.update();
    }
  };

  const doImport = async (n: number) => {
    const projectId = s.project.id;
    if (!projectId) return;
    importing = n;
    importError = undefined;
    void handle.update();
    try {
      const created = await s.client.githubImportIssue(projectId, n);
      if (handle.props.onImported) handle.props.onImported(created);
      else void navigate(routes.task.href({ id: created.id }));
    } catch (e) {
      importError = e instanceof Error ? e : new Error(String(e));
    } finally {
      importing = undefined;
      void handle.update();
    }
  };

  return () => {
    const project = s.workspace.project;
    if (project && !projectHasRepo(project)) {
      return (
        <Page title="Pull Requests" icon={GitPullRequest}>
          <Empty title="No repository yet" icon={GitPullRequest}>
            <p data-testid="pulls-no-repo">
              Attach a GitHub repository to see pull requests. <a href={routes.project.href()}>Open Project settings</a>
            </p>
          </Empty>
        </Page>
      );
    }
    const list = withPR();
    const taskId = currentId();
    const current = list.find((x) => x.id === taskId);
    return (
      <Page title="Pull Requests" icon={GitPullRequest} subtitle={list.length > 0 ? `${list.length} open from tasks` : undefined}>
        <Section title="Pull requests of tasks" description="In pull request mode a finished task is pushed and a PR is opened; its CI and reviews are read from GitHub. It is merged only after its CI passed (unless you decided otherwise: Project settings, CI before merge) and you approved.">
          <ErrorNote error={s.project.tasks.error} what="Could not load tasks" />
          {list.length === 0 ? (
            <Empty title="No task has a pull request" icon={GitPullRequest}>
              <p>In pull request mode (Project settings), a finished task is pushed and a PR is opened. It appears here with its CI and reviews.</p>
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

        <Section title="Import from GitHub issues" description="Lists the open issues of this repository's GitHub origin. Importing creates a backlog task with the issue text quoted as data.">
          <form
            mix={[
              loadCss,
              on('submit', (e) => {
                e.preventDefault();
                void loadIssues();
              }),
            ]}
          >
            <Field label="Labels (comma separated, optional)">
              {(id) => <input id={id} placeholder="bug, good first issue" value={labels} mix={on('input', (e) => ((labels = e.currentTarget.value), void handle.update()))} />}
            </Field>
            <Button type="submit" icon={CircleDot} disabled={loadingIssues || !s.project.id}>
              Load open issues
            </Button>
          </form>
          <ErrorNote error={issueError} what="Could not list issues" />
          <ErrorNote error={importError} what="Import failed" />
          {loadingIssues && !issues ? <Loading label="Loading issues..." /> : null}
          {issues && issues.length === 0 ? (
            <Empty title="No open issues." icon={Import}>
              <p mix={muted}>Nothing to import{labels.trim() ? ' with these labels' : ''}.</p>
            </Empty>
          ) : null}
          {issues && issues.length > 0 ? (
            <ul mix={issuesCss} aria-label="Open issues">
              {issues.map((i) => (
                <IssueItem key={i.number} issue={i} busy={importing !== undefined} onImport={() => void doImport(i.number)} />
              ))}
            </ul>
          ) : null}
        </Section>
      </Page>
    );
  };
}
