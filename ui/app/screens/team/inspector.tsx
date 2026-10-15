// Agent Inspector (docs/ui.md section 10), in the details panel (~320px): only what the core can report today. Everything
// else is stated as absent.

import { css, navigate, on, type Handle } from 'remix/component';
import { MessageSquare, ScanSearch, ShieldAlert, ShieldCheck } from 'lucide';
import type { AgentRun } from '../../api/types.ts';
import { formatDateTime, presenceFor } from '../../lib/format.ts';
import { routes } from '../../routes.ts';
import { action, query, services, watch } from '../../state/services.tsx';
import { Button } from '../../ui/button.tsx';
import { Avatar, Badge, PresenceDot, muted, presenceColor } from '../../ui/data.tsx';
import { Empty, ErrorNote, Loading } from '../../ui/feedback.tsx';
import { Icon } from '../../ui/icon.tsx';
import { t, tone } from '../../ui/tokens.ts';
import { TaskDecisions } from '../shared/explain.tsx';
import { UsageTable } from '../shared/usage.tsx';

const time = (ms: number) => (ms > 0 ? formatDateTime(ms) : '');

const rootCss = css({ display: 'grid', gap: t.s4, minWidth: '0', fontSize: t.textMd });
const headCss = css({
  display: 'flex',
  alignItems: 'center',
  gap: t.s3,
  minWidth: '0',
  '& h3': { margin: '0', fontSize: t.textLg, fontWeight: '600', whiteSpace: 'nowrap', overflow: 'hidden', textOverflow: 'ellipsis' },
  '& .who': { display: 'grid', gap: '2px', minWidth: '0' },
  '& .sub': { color: t.textMuted, fontSize: t.textSm, whiteSpace: 'nowrap', overflow: 'hidden', textOverflow: 'ellipsis' },
});

// a stacked definition list: label above value, which reads well in a narrow panel
const propsCss = css({
  display: 'grid',
  gap: '0',
  margin: '0',
  border: `1px solid ${t.border}`,
  borderRadius: t.rLg,
  background: t.bgSurface,
  overflow: 'hidden',
  '& > div': { display: 'grid', gap: '2px', padding: `${t.s2} ${t.s3}`, borderTop: `1px solid ${t.border}`, minWidth: '0' },
  '& > div:first-child': { borderTop: '0' },
  '& dt': { color: t.textFaint, fontSize: t.textXs, fontWeight: '600', letterSpacing: '0.03em', textTransform: 'uppercase' },
  '& dd': { margin: '0', minWidth: '0', overflowWrap: 'anywhere', fontSize: t.textMd },
  '& ul': { margin: `${t.s1} 0 0`, paddingLeft: t.s4, fontSize: t.textSm },
});

const sectionHead = css({ margin: '0', color: t.textFaint, fontSize: t.textXs, fontWeight: '600', letterSpacing: '0.04em', textTransform: 'uppercase' });

const runCss = css({
  display: 'grid',
  gap: t.s2,
  padding: `${t.s2} ${t.s3}`,
  border: `1px solid ${t.border}`,
  borderRadius: t.rMd,
  background: t.bgSurface,
  fontSize: t.textSm,
  '& .top': { display: 'flex', alignItems: 'center', gap: t.s2, color: t.textMuted },
  '& dl': { display: 'grid', gridTemplateColumns: 'max-content 1fr', gap: '2px 10px', margin: '0' },
  '& dt': { color: t.textMuted },
  '& dd': { margin: '0', minWidth: '0', overflowWrap: 'anywhere' },
  '& .err': { margin: '0', color: t.danger, overflowWrap: 'anywhere' },
});

const isoCss = css({ display: 'flex', flexWrap: 'wrap', alignItems: 'center', gap: '6px' });
const wide = css({ width: '100%' });

function Run(handle: Handle<{ r: AgentRun }>) {
  return () => {
    const r = handle.props.r;
    return (
      <div mix={runCss} data-testid="agent-run">
        <div class="top">
          <Badge tone={r.status === 'failed' ? 'err' : r.status === 'running' ? 'info' : 'muted'}>{r.status}</Badge>
          <span>{time(r.startedAt)}</span>
        </div>
        <dl>
          <dt>PID</dt>
          <dd>{r.pid > 0 ? r.pid : 'not reported'}</dd>
          <dt>Working dir</dt>
          <dd>{r.cwd ? <code>{r.cwd}</code> : 'not reported'}</dd>
          <dt>Ended</dt>
          <dd>{r.endedAt > 0 ? time(r.endedAt) : 'not ended'}</dd>
          {r.taskId ? (
            <>
              <dt>Task</dt>
              <dd>
                <a href={routes.task.href({ id: r.taskId })}>{r.taskId}</a> · <a href={routes.run.href({ id: r.taskId })}>what it was given and why</a>
              </dd>
            </>
          ) : null}
        </dl>
        {r.error ? <p class="err">{r.error}</p> : null}
      </div>
    );
  };
}

/** `onNavigate` replaces the router's navigate (tests). */
export function AgentInspector(handle: Handle<{ agentId: string | undefined; onNavigate?: (href: string) => void }>) {
  const s = services(handle);
  watch(handle, s.project);
  const act = action(handle);
  const q = query(handle, {
    load: () => s.client.agentGet(handle.props.agentId!),
    key: () => handle.props.agentId,
    enabled: () => !!handle.props.agentId,
    when: (e) => e.agentId === handle.props.agentId || (e.type.startsWith('task.') && !e.agentId) || e.type === 'room.message_posted',
    debounceMs: 200,
  });

  const message = (agentId: string, projectId: string) =>
    act.run('dm', async () => {
      const room = await s.client.roomCreateDM(projectId || s.workspace.projectId || '', agentId);
      const href = routes.room.href({ id: room.id });
      if (handle.props.onNavigate) handle.props.onNavigate(href);
      else void navigate(href);
    });

  return () => {
    const agentId = handle.props.agentId;
    if (!agentId) {
      return (
        <Empty title="No agent chosen" icon={ScanSearch}>
          <p>Choose an agent and press Inspect.</p>
        </Empty>
      );
    }
    const d = q.data;
    if (!d) {
      return (
        <div>
          <ErrorNote error={q.error} what="Could not load the agent" />
          {q.loading || !q.error ? <Loading /> : null}
        </div>
      );
    }
    const a = d.agent;
    const runs = d.runs ?? [];
    const usage = d.usage ?? [];
    const p = presenceFor(a.id, s.project.presence.data);
    const latestTask = runs.find((r) => r.taskId)?.taskId;
    const iso = d.isolation;
    return (
      <div mix={rootCss} data-testid="inspector" data-agent={a.id}>
        <div mix={headCss}>
          <Avatar id={a.id} name={a.name} size={40} status={presenceColor(p)} />
          <div class="who">
            <h3 title={a.name}>{a.name}</h3>
            <span class="sub">
              {a.role} · {a.adapter}
            </span>
          </div>
        </div>
        <ErrorNote error={act.error} what="Could not open a direct message" />
        <Button icon={MessageSquare} disabled={act.busy === 'dm'} mix={[wide, on('click', () => void message(a.id, a.projectId))]}>
          Message {a.name}
        </Button>

        <dl mix={propsCss}>
          <div>
            <dt>Presence</dt>
            <dd>
              <PresenceDot presence={p} />
              {p?.activity ? <div mix={muted}>{p.activity}</div> : null}
            </dd>
          </div>
          <div>
            <dt>Id</dt>
            <dd>
              <code>{a.id}</code>
            </dd>
          </div>
          <div>
            <dt>Adapter</dt>
            <dd>{a.adapter}</dd>
          </div>
          <div>
            <dt>Role</dt>
            <dd>{a.role}</dd>
          </div>
          <div>
            <dt>Purpose</dt>
            <dd data-testid="inspector-purpose" style={{ whiteSpace: 'pre-wrap' }}>
              {a.purpose || 'none set'}
            </dd>
          </div>
          <div>
            <dt>Model</dt>
            <dd>{a.model || 'adapter default (not set)'}</dd>
          </div>
          <div>
            <dt>Effort</dt>
            <dd>{a.effort || 'not set'}</dd>
          </div>
          <div>
            <dt>Permission mode</dt>
            <dd>{a.permissionMode}</dd>
          </div>
          {iso ? (
            <div>
              <dt>Isolation</dt>
              <dd data-testid="inspector-isolation" data-level={iso.level}>
                <span mix={isoCss}>
                  <span style={{ display: 'inline-flex', color: iso.level === 'guarded' ? tone.ok.fg : tone.warn.fg }}>
                    <Icon icon={iso.level === 'guarded' ? ShieldCheck : ShieldAlert} size={14} />
                  </span>
                  <Badge tone={iso.level === 'guarded' ? 'ok' : 'warn'}>{iso.level === 'guarded' ? 'guarded' : 'not held in'}</Badge>
                </span>{' '}
                {iso.summary}
                {(iso.details ?? []).length > 0 ? (
                  <ul>
                    {(iso.details ?? []).map((x, i) => (
                      <li key={i} mix={muted}>
                        {x}
                      </li>
                    ))}
                  </ul>
                ) : null}
              </dd>
            </div>
          ) : null}
          <div>
            <dt>Why this model?</dt>
            <dd mix={muted} data-testid="inspector-routing">
              {(a.routeModels ?? []).length > 0
                ? `Routed on every turn: the model router chooses between ${(a.routeModels ?? []).map((m) => `${m.model} (${m.tier})`).join(', ')} for its task work, reviews, template steps (spec, tests, design, security review) and room replies, and each decision says why.`
                : 'Not routed: every turn uses the configured model. To let the router choose, list models under Edit (models the router may choose).'}
            </dd>
          </div>
        </dl>

        <h4 mix={sectionHead}>Runs</h4>
        {runs.length === 0 ? <p mix={muted}>This agent has not run yet.</p> : null}
        {runs.length > 0 ? (
          <div style={{ display: 'grid', gap: '8px' }}>
            {runs.map((r) => (
              <Run key={r.id} r={r} />
            ))}
          </div>
        ) : null}

        <h4 mix={sectionHead}>Usage</h4>
        <UsageTable rows={usage} />

        {latestTask ? (
          <>
            <h4 mix={sectionHead}>Why this model? (latest task)</h4>
            <TaskDecisions taskId={latestTask} />
          </>
        ) : null}
      </div>
    );
  };
}
