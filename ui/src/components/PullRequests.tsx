// Pull Requests (docs/ui.md S18) and GitHub issue import. Everything that comes from GitHub (PR body, review and comment
// text, issue bodies) is outside text: it is rendered through the inert Markdown renderer, shown with a trusted or
// untrusted badge, and never turns into an action. The only actions here are buttons the human presses.

import { useState } from 'react';
import type { GitHubCIState, GitHubIssue, GitHubReview, PRState, Task } from '../api/types';
import { Markdown, safeHref } from '../lib/Markdown';
import { formatDateTime } from '../lib/format';
import { useClient } from '../state/context';
import { useLive } from '../state/hooks';
import { Badge, Empty, ErrorNote, Page, StatusChip } from './common';

export function ReviewItem({ r }: { r: GitHubReview }) {
  return (
    <li className={`gh-review ${r.trusted ? 'gh-trusted' : 'gh-untrusted'}`} data-testid="gh-review" data-trusted={r.trusted}>
      <header className="msg-head">
        <strong>{r.author}</strong>
        {r.trusted ? (
          <Badge tone="ok" title="Repository owner, member or collaborator. A change request from this author can send the task back for rework.">
            trusted
          </Badge>
        ) : (
          <Badge tone="warn" title="Not a collaborator. This text is shown for you to read; it never triggers any action on its own.">
            untrusted
          </Badge>
        )}
        <span className="muted">{r.association}</span>
        <Badge>{r.kind}</Badge>
        {r.state ? <Badge tone={r.state === 'APPROVED' ? 'ok' : r.state === 'CHANGES_REQUESTED' ? 'err' : 'muted'}>{r.state}</Badge> : null}
        {r.path ? (
          <code>
            {r.path}
            {r.line ? `:${r.line}` : ''}
          </code>
        ) : null}
        <time className="muted">{formatDateTime(r.created)}</time>
      </header>
      {r.body ? <Markdown source={r.body} /> : <p className="muted">(no text)</p>}
    </li>
  );
}

export function CiList({ ci }: { ci: GitHubCIState }) {
  const runs = ci.Runs ?? [];
  const summary = runs.length === 0 ? 'no checks reported' : ci.Pending ? 'pending' : ci.Passed ? 'passed' : 'failed';
  const tone = summary === 'passed' ? 'ok' : summary === 'failed' ? 'err' : 'muted';
  return (
    <div data-testid="gh-ci">
      <p>
        CI: <Badge tone={tone}>{summary}</Badge>
      </p>
      <ul>
        {runs.map((r) => {
          const href = safeHref(r.html_url);
          const done = r.status === 'completed';
          const res = done ? r.conclusion || 'completed' : r.status;
          return (
            <li key={r.id}>
              <span aria-hidden="true">{done ? (r.conclusion === 'success' ? '✔' : '✖') : '◌'}</span> {r.name}: {res}
              {href ? (
                <>
                  {' '}
                  <a href={href} target="_blank" rel="noopener noreferrer" title={href}>
                    details
                  </a>
                </>
              ) : null}
            </li>
          );
        })}
      </ul>
    </div>
  );
}

export function PullRequestPanel({ state }: { state: PRState }) {
  const pr = state.pr;
  const href = safeHref(pr.html_url);
  const reviews = state.reviews ?? [];
  return (
    <div data-testid="gh-pr">
      <h2>
        #{pr.number} {pr.title}
      </h2>
      <p>
        <Badge tone={pr.merged ? 'ok' : pr.state === 'open' ? 'info' : 'muted'}>{pr.merged ? 'merged' : pr.state}</Badge>
        {pr.draft ? <Badge>draft</Badge> : null} <code>{pr.head.ref}</code> into <code>{pr.base.ref}</code>
        {pr.mergeable_state ? <span className="muted"> mergeable state: {pr.mergeable_state}</span> : null}
        {href ? (
          <>
            {' '}
            <a href={href} target="_blank" rel="noopener noreferrer" title={href}>
              Open on GitHub
            </a>
          </>
        ) : null}
      </p>
      <p className="muted">Merging happens on GitHub by you. Drydock never enables auto-merge.</p>
      {pr.body ? <Markdown source={pr.body} /> : null}
      <CiList ci={state.ci} />
      <h3>Reviews and comments</h3>
      {reviews.length === 0 ? <p className="muted">None yet.</p> : null}
      <ul className="gh-reviews">
        {reviews.map((r) => (
          <ReviewItem key={`${r.kind}-${r.id}`} r={r} />
        ))}
      </ul>
    </div>
  );
}

export function IssueItem({ issue, onImport, busy }: { issue: GitHubIssue; onImport: () => void; busy: boolean }) {
  const href = safeHref(issue.html_url);
  return (
    <li className="gh-issue" data-testid="gh-issue" data-number={issue.number}>
      <header className="msg-head">
        <strong>
          #{issue.number} {issue.title}
        </strong>
        <span className="muted">by {issue.author || 'unknown'}</span>
        {(issue.labels ?? []).map((l) => (
          <Badge key={l}>{l}</Badge>
        ))}
        <Badge tone="warn" title="Issue text comes from outside Drydock. It is data, never instructions.">
          untrusted
        </Badge>
        {href ? (
          <a href={href} target="_blank" rel="noopener noreferrer" title={href}>
            GitHub
          </a>
        ) : null}
        <button disabled={busy} onClick={onImport} aria-label={`Import issue #${issue.number} as a task`}>
          Import as task
        </button>
      </header>
      <details>
        <summary>Issue text</summary>
        {issue.body ? <Markdown source={issue.body} /> : <p className="muted">(empty)</p>}
      </details>
    </li>
  );
}

export function PullRequestsView({ projectId, tasks }: { projectId: string; tasks: readonly Task[] }) {
  const client = useClient();
  const withPR = tasks.filter((t) => t.prRef);
  const [selected, setSelected] = useState<string | undefined>();
  const taskId = selected ?? withPR[0]?.id;
  const pr = useLive(() => client.githubPullRequest(taskId!), [taskId], (e) => e.type.startsWith('github.') && e.taskId === taskId, !!taskId, 300);

  const [issues, setIssues] = useState<GitHubIssue[] | undefined>();
  const [labels, setLabels] = useState('');
  const [issueError, setIssueError] = useState<Error | undefined>();
  const [loadingIssues, setLoadingIssues] = useState(false);
  const [importing, setImporting] = useState<number | undefined>();
  const [importError, setImportError] = useState<Error | undefined>();

  const loadIssues = async () => {
    setLoadingIssues(true);
    setIssueError(undefined);
    try {
      setIssues(await client.githubIssues(projectId, labels.trim()));
    } catch (e) {
      setIssueError(e as Error);
    } finally {
      setLoadingIssues(false);
    }
  };
  const doImport = async (n: number) => {
    setImporting(n);
    setImportError(undefined);
    try {
      const t = await client.githubImportIssue(projectId, n);
      window.location.hash = `#/task/${encodeURIComponent(t.id)}`;
    } catch (e) {
      setImportError(e as Error);
    } finally {
      setImporting(undefined);
    }
  };

  return (
    <Page title="Pull Requests">
      <h2>Pull requests of tasks</h2>
      {withPR.length === 0 ? (
        <Empty title="No task has a pull request">
          <p>In pull request mode (Project settings), a finished task is pushed and a PR is opened. It appears here with its CI and reviews.</p>
        </Empty>
      ) : (
        <>
          <label>
            Task
            <select aria-label="Task" value={taskId ?? ''} onChange={(e) => setSelected(e.target.value)}>
              {withPR.map((t) => (
                <option key={t.id} value={t.id}>
                  #{t.seq} {t.title} ({t.prRef})
                </option>
              ))}
            </select>
          </label>{' '}
          {withPR.find((t) => t.id === taskId) ? <StatusChip status={withPR.find((t) => t.id === taskId)!.status} /> : null}
          <ErrorNote error={pr.error} what="Could not read the pull request from GitHub" />
          {pr.loading && !pr.data ? <p className="muted">Loading from GitHub...</p> : null}
          {pr.data ? <PullRequestPanel state={pr.data} /> : null}
        </>
      )}

      <h2>Import from GitHub issues</h2>
      <p className="muted">Lists the open issues of this repository's GitHub origin. Importing creates a backlog task with the issue text quoted as data.</p>
      <div className="form-actions">
        <label>
          Labels (comma separated, optional)
          <input value={labels} onChange={(e) => setLabels(e.target.value)} />
        </label>
        <button onClick={loadIssues} disabled={loadingIssues}>
          Load open issues
        </button>
      </div>
      <ErrorNote error={issueError} what="Could not list issues" />
      <ErrorNote error={importError} what="Import failed" />
      {issues && issues.length === 0 ? <p className="muted">No open issues.</p> : null}
      <ul className="gh-issues">
        {(issues ?? []).map((i) => (
          <IssueItem key={i.number} issue={i} busy={importing !== undefined} onImport={() => doImport(i.number)} />
        ))}
      </ul>
    </Page>
  );
}
