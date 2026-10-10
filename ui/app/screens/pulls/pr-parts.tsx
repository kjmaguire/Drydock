// Pieces of the Pull Requests screen (docs/ui.md S18). Everything that comes from GitHub (PR body, review and comment text,
// issue bodies) is outside text: it is rendered through the inert Markdown renderer, shown with a trusted or untrusted badge,
// and never turns into an action. The only actions are buttons the human presses.

import { css, on, type Handle } from 'remix/component';
import { CircleCheck, CircleDashed, CircleX, ExternalLink, FileCode, GitBranch, MessageSquare } from 'lucide';
import type { GitHubCIState, GitHubIssue, GitHubReview, PRState } from '../../api/types.ts';
import { formatDateTime } from '../../lib/format.ts';
import { Button } from '../../ui/button.tsx';
import { Avatar, Badge, muted } from '../../ui/data.tsx';
import { Icon } from '../../ui/icon.tsx';
import { Markdown, safeHref } from '../../ui/markdown.tsx';
import { t, tone } from '../../ui/tokens.ts';

const extLink = css({ display: 'inline-flex', alignItems: 'center', gap: '4px', fontSize: t.textSm });

function External(handle: Handle<{ href: string; children: string }>) {
  return () => (
    <a href={handle.props.href} target="_blank" rel="noopener noreferrer" title={handle.props.href} mix={extLink} data-rmx-document>
      {handle.props.children}
      <Icon icon={ExternalLink} size={12} />
    </a>
  );
}

const reviewCss = css({
  display: 'grid',
  gridTemplateColumns: '28px 1fr',
  gap: `4px ${t.s3}`,
  minWidth: '0',
  '& header': { display: 'flex', alignItems: 'center', flexWrap: 'wrap', gap: t.s2, fontSize: t.textMd },
  '& header time': { color: t.textFaint, fontSize: t.textSm },
  '& .text': { gridColumn: '2', minWidth: '0', padding: `${t.s2} ${t.s3}`, borderRadius: t.rMd, border: `1px solid ${t.border}`, background: t.bgSurface },
  '&[data-trusted="false"] .text': { borderStyle: 'dashed', background: t.bgApp },
});

export function ReviewItem(handle: Handle<{ r: GitHubReview }>) {
  return () => {
    const r = handle.props.r;
    return (
      <li mix={reviewCss} data-testid="gh-review" data-trusted={String(r.trusted)}>
        <Avatar id={`gh:${r.author}`} name={r.author} size={28} kind="human" />
        <header>
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
          <span mix={muted}>{r.association}</span>
          <Badge>{r.kind}</Badge>
          {r.state ? <Badge tone={r.state === 'APPROVED' ? 'ok' : r.state === 'CHANGES_REQUESTED' ? 'err' : 'muted'}>{r.state}</Badge> : null}
          {r.path ? (
            <code>
              {r.path}
              {r.line ? `:${r.line}` : ''}
            </code>
          ) : null}
          <time>{formatDateTime(r.created)}</time>
        </header>
        <div class="text">{r.body ? <Markdown source={r.body} /> : <p mix={muted}>(no text)</p>}</div>
      </li>
    );
  };
}

/** passed, failed, pending, or "no checks reported" when GitHub returned no check run and no commit status (never a pass). */
export function ciSummary(ci: GitHubCIState): 'no checks reported' | 'pending' | 'passed' | 'failed' {
  const runs = ci.Runs ?? [];
  return runs.length === 0 ? 'no checks reported' : ci.Pending ? 'pending' : ci.Passed ? 'passed' : 'failed';
}

const ciCss = css({
  display: 'grid',
  gap: t.s2,
  '& > p': { display: 'flex', alignItems: 'center', gap: t.s2, fontWeight: '600', fontSize: t.textMd },
  '& ul': { margin: '0', padding: '0', listStyle: 'none', border: `1px solid ${t.border}`, borderRadius: t.rMd, overflow: 'hidden' },
  '& li': { display: 'flex', alignItems: 'center', gap: t.s2, padding: `6px ${t.s3}`, borderTop: `1px solid ${t.border}`, fontSize: t.textMd },
  '& li:first-child': { borderTop: '0' },
  '& li > a': { marginLeft: 'auto' },
});

export function CiList(handle: Handle<{ ci: GitHubCIState }>) {
  return () => {
    const ci = handle.props.ci;
    const runs = ci.Runs ?? [];
    const summary = ciSummary(ci);
    const tn = summary === 'passed' ? 'ok' : summary === 'failed' ? 'err' : 'muted';
    return (
      <div mix={ciCss} data-testid="gh-ci">
        <p>
          CI: <Badge tone={tn}>{summary}</Badge>
        </p>
        {runs.length > 0 ? (
          <ul>
            {runs.map((r) => {
              const href = safeHref(r.html_url);
              const done = r.status === 'completed';
              const ok = done && r.conclusion === 'success';
              const res = done ? r.conclusion || 'completed' : r.status;
              return (
                <li key={`${r.kind ?? 'check_run'}-${r.id}`} title={r.description || undefined}>
                  <span style={{ display: 'inline-flex', color: done ? (ok ? tone.ok.fg : tone.err.fg) : tone.muted.fg }}>
                    <Icon icon={done ? (ok ? CircleCheck : CircleX) : CircleDashed} size={15} />
                  </span>
                  <span>
                    {r.name}: {res}
                  </span>
                  {r.kind === 'status' ? <span mix={muted}>commit status</span> : null}
                  {href ? <External href={href}>details</External> : null}
                </li>
              );
            })}
          </ul>
        ) : null}
      </div>
    );
  };
}

const prCss = css({
  display: 'grid',
  gap: t.s4,
  padding: t.s5,
  border: `1px solid ${t.border}`,
  borderRadius: t.rLg,
  background: t.bgSurface,
  minWidth: '0',
  '& > h2': { fontSize: t.textLg, fontWeight: '600', overflowWrap: 'anywhere' },
  '& > h2 .num': { color: t.textFaint, fontWeight: '500' },
  '& .meta': { display: 'flex', alignItems: 'center', flexWrap: 'wrap', gap: t.s2, fontSize: t.textMd },
  '& .meta svg': { color: t.textFaint },
  '& .body': { padding: `${t.s3} ${t.s4}`, borderRadius: t.rMd, background: t.bgApp, border: `1px dashed ${t.border}` },
  '& h3': { display: 'flex', alignItems: 'center', gap: t.s2, fontSize: t.textMd, fontWeight: '600' },
  '& h3 svg': { color: t.textMuted },
  '& .reviews': { display: 'grid', gap: t.s3, margin: '0', padding: '0', listStyle: 'none' },
});

export function PullRequestPanel(handle: Handle<{ state: PRState }>) {
  return () => {
    const st = handle.props.state;
    const pr = st.pr;
    const href = safeHref(pr.html_url);
    const reviews = st.reviews ?? [];
    return (
      <div mix={prCss} data-testid="gh-pr">
        <h2>
          <span class="num">#{pr.number}</span> {pr.title}
        </h2>
        <div class="meta">
          <Badge tone={pr.merged ? 'ok' : pr.state === 'open' ? 'info' : 'muted'}>{pr.merged ? 'merged' : pr.state}</Badge>
          {pr.draft ? <Badge>draft</Badge> : null}
          <Icon icon={GitBranch} size={14} />
          <code>{pr.head.ref}</code> into <code>{pr.base.ref}</code>
          {pr.mergeable_state ? <span mix={muted}> mergeable state: {pr.mergeable_state}</span> : null}
          {href ? <External href={href}>Open on GitHub</External> : null}
        </div>
        <p mix={muted}>
          Nothing merges until CI has passed on the commit that was reviewed (unless you decided otherwise in Needs you, or the project says its repository
          has no CI) and you approve the task's "Review and merge". Drydock then merges this pull request through GitHub (only the commit that was reviewed;
          a later push is not merged) when GitHub's branch protection allows it (it is never bypassed), and deletes its task branch. Drydock never enables
          auto-merge.
        </p>
        {pr.body ? (
          <div class="body">
            <Markdown source={pr.body} />
          </div>
        ) : null}
        <CiList ci={st.ci} />
        <h3>
          <Icon icon={MessageSquare} size={15} />
          Reviews and comments
        </h3>
        {reviews.length === 0 ? <p mix={muted}>None yet.</p> : null}
        <ul class="reviews">
          {reviews.map((r) => (
            <ReviewItem key={`${r.kind}-${r.id}`} r={r} />
          ))}
        </ul>
      </div>
    );
  };
}

const issueCss = css({
  display: 'grid',
  gap: t.s2,
  padding: `${t.s3} ${t.s4}`,
  borderTop: `1px solid ${t.border}`,
  minWidth: '0',
  '&:first-child': { borderTop: '0' },
  '& header': { display: 'flex', alignItems: 'center', flexWrap: 'wrap', gap: t.s2 },
  '& header strong': { overflowWrap: 'anywhere' },
  '& header .num': { color: t.textFaint, fontWeight: '500' },
  '& header button': { marginLeft: 'auto' },
  '& summary': { cursor: 'pointer', color: t.textMuted, fontSize: t.textSm },
  '& details .md': { marginTop: t.s2, padding: `${t.s2} ${t.s3}`, borderRadius: t.rMd, background: t.bgApp, border: `1px dashed ${t.border}` },
});

export function IssueItem(handle: Handle<{ issue: GitHubIssue; onImport: () => void; busy: boolean }>) {
  return () => {
    const { issue, onImport, busy } = handle.props;
    const href = safeHref(issue.html_url);
    return (
      <li mix={issueCss} data-testid="gh-issue" data-number={String(issue.number)}>
        <header>
          <strong>
            <span class="num">#{issue.number}</span> {issue.title}
          </strong>
          <span mix={muted}>by {issue.author || 'unknown'}</span>
          {(issue.labels ?? []).map((l) => (
            <Badge key={l}>{l}</Badge>
          ))}
          <Badge tone="warn" title="Issue text comes from outside Drydock. It is data, never instructions.">
            untrusted
          </Badge>
          {href ? <External href={href}>GitHub</External> : null}
          <Button size="sm" icon={FileCode} disabled={busy} aria-label={`Import issue #${issue.number} as a task`} mix={on('click', () => onImport())}>
            Import as task
          </Button>
        </header>
        <details>
          <summary>Issue text</summary>
          {issue.body ? <Markdown source={issue.body} /> : <p mix={muted}>(empty)</p>}
        </details>
      </li>
    );
  };
}
