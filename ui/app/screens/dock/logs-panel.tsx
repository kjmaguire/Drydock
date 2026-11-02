// A task's check logs and agent streams (docs/ui.md 2, the Logs tab). Read by range: the tail first, then whatever is
// appended while the source is written; earlier output on request. It opens on the log that matters (pickLogSource: the running
// stream, else the newest failed check) until the person picks one, which is remembered per task (X22, X23); the list shows
// every log or only the failed checks (X25), and an agent stream is named by its agent (X210).

import { css, on, ref, type Handle } from 'remix/component';
import { ArrowUpToLine, Bot, FlaskConical, ScrollText } from 'lucide';
import type { LogSource } from '../../api/dockClient.ts';
import { formatTime } from '../../lib/format.ts';
import { sleep, whenVisible } from '../../lib/wait.ts';
import { query, services, watch } from '../../state/services.tsx';
import { Button } from '../../ui/button.tsx';
import { Avatar, Badge } from '../../ui/data.tsx';
import { Empty, ErrorNote, Loading } from '../../ui/feedback.tsx';
import { Checkbox } from '../../ui/form.tsx';
import { Icon } from '../../ui/icon.tsx';
import { t } from '../../ui/tokens.ts';
import { filterLogs, LOG_CHUNK, LogBuffer, pickLogSource, type LogFilter } from './dock-format.ts';
import { errorSlot, pane, paneHead, panel, segmented, sideList, split, toolbar } from './dock-ui.tsx';

const itemTop = css({ display: 'flex', alignItems: 'center', gap: '6px', minWidth: '0' });
const itemTitle = css({ color: t.text, fontWeight: '500', overflow: 'hidden', textOverflow: 'ellipsis', minWidth: '0' });
const itemMeta = css({ display: 'flex', alignItems: 'center', gap: '6px', paddingLeft: '20px', color: t.textFaint, fontSize: t.textXs });
const kindIcon = css({ display: 'inline-flex', color: t.textMuted, flex: 'none' });
const headActions = css({ marginLeft: 'auto', display: 'flex', alignItems: 'center', gap: t.s3, flex: 'none', '& label': { fontSize: t.textSm, color: t.textMuted } });
const fromStart = css({ color: t.textFaint, fontSize: t.textXs });
const logText = css({
  flex: '1',
  minHeight: '0',
  margin: '0',
  padding: `${t.s2} ${t.s3}`,
  overflow: 'auto',
  border: '0',
  borderRadius: '0',
  background: t.bgApp,
  color: t.text,
  fontFamily: t.mono,
  fontSize: t.textSm,
  lineHeight: '1.55',
  whiteSpace: 'pre-wrap',
  overflowWrap: 'anywhere',
  tabSize: '4',
  '&:focus-visible': { outline: `2px solid ${t.focus}`, outlineOffset: '-2px' },
});

/** A log's name in the list and over its text: an agent stream says whose it is ("Ada · work (step 2)"). */
export function logTitle(l: LogSource, agentName: (id: string) => string | undefined): string {
  const who = l.kind === 'agent' && l.agentId ? agentName(l.agentId) : undefined;
  return who ? `${who} · ${l.title}` : l.title;
}

export function LogsPanel(handle: Handle<{ taskId: string }>) {
  const s = services(handle);
  watch(handle, s.project);
  let selected = s.ui.dockView(handle.props.taskId).log;
  let filter: LogFilter = 'all';
  const list = query<LogSource[]>(handle, {
    load: () => s.client.logsList(handle.props.taskId),
    when: (e) => e.taskId === handle.props.taskId,
    key: () => handle.props.taskId,
    debounceMs: 500,
  });

  return () => {
    const taskId = handle.props.taskId;
    if (list.loading && !list.data) return <Loading label="Loading logs..." />;
    const all = list.data ?? [];
    const logs = filterLogs(all, filter);
    const current = logs.find((l) => l.id === selected) ?? pickLogSource(logs);
    const agents = s.project.agentList;
    const agentName = (id: string) => agents.find((a) => a.id === id)?.name;
    const failed = all.filter((l) => l.passed === false).length;
    return (
      <div mix={panel} data-testid="logs-panel">
        {list.error ? (
          <div mix={errorSlot}>
            <ErrorNote error={list.error} what="Could not list the logs" />
          </div>
        ) : null}
        {all.length > 0 ? (
          <div role="toolbar" aria-label="Log filter" mix={toolbar}>
            <div role="group" aria-label="Which logs" mix={segmented}>
              {(['all', 'failed'] as const).map((m) => (
                <button
                  key={m}
                  type="button"
                  aria-pressed={filter === m}
                  mix={on('click', () => {
                    filter = m;
                    void handle.update();
                  })}
                >
                  {m === 'all' ? 'All' : failed > 0 ? `Failed (${failed})` : 'Failed'}
                </button>
              ))}
            </div>
          </div>
        ) : null}
        {all.length > 0 && logs.length === 0 ? (
          <Empty title="No failed checks" icon={ScrollText}>
            <p>No check has failed.</p>
          </Empty>
        ) : logs.length === 0 ? (
          <Empty title="No logs yet" icon={ScrollText}>
            <p>No logs yet. Check output and agent streams appear here once the task runs.</p>
          </Empty>
        ) : (
          <div mix={split}>
            <ul aria-label="Logs" class="two-line" mix={sideList}>
              {logs.map((l) => (
                <li key={l.id}>
                  <button
                    type="button"
                    aria-current={l === current ? 'true' : undefined}
                    mix={on('click', () => {
                      selected = l.id;
                      s.ui.setDockView(taskId, { log: l.id });
                      void handle.update();
                    })}
                  >
                    <span mix={itemTop}>
                      <span mix={kindIcon} title={l.kind === 'agent' ? 'Agent stream' : 'Check output'}>
                        {l.kind === 'agent' && l.agentId && agentName(l.agentId) ? (
                          <Avatar id={l.agentId} name={agentName(l.agentId)!} kind="agent" size={14} />
                        ) : (
                          <Icon icon={l.kind === 'agent' ? Bot : FlaskConical} size={14} />
                        )}
                      </span>
                      <span class="sr-only">{l.kind === 'agent' ? 'Agent: ' : 'Check: '}</span>
                      <strong mix={itemTitle}>{logTitle(l, agentName)}</strong>
                    </span>
                    <span mix={itemMeta}>
                      {l.passed === true ? <Badge tone="ok">passed</Badge> : l.passed === false ? <Badge tone="err">failed</Badge> : null}
                      {l.running ? <Badge tone="info">running</Badge> : null}
                      <span>{formatTime(l.created)}</span>
                    </span>
                  </button>
                </li>
              ))}
            </ul>
            {current ? <LogView key={current.id} taskId={taskId} source={current} title={logTitle(current, agentName)} /> : null}
          </div>
        )}
      </div>
    );
  };
}

function LogView(handle: Handle<{ taskId: string; source: LogSource; title: string }>) {
  const s = services(handle);
  const buf = new LogBuffer();
  let start = 0;
  let next = 0;
  let error: Error | undefined;
  let follow = true;
  let box: HTMLPreElement | undefined;
  const signal = handle.signal;

  // first the tail, then whatever arrives while the source is still being written
  const tick = async (first: boolean) => {
    const { taskId, source } = handle.props;
    try {
      const r = await s.client.logsRead(taskId, source.id, first ? { tail: true, limit: LOG_CHUNK } : { from: next, limit: LOG_CHUNK });
      if (signal.aborted) return;
      if (first) start = r.start;
      next = r.next;
      const before = buf.text();
      buf.append(r.text);
      const changed = buf.text() !== before;
      const hadError = !!error;
      error = undefined;
      if (first || changed || hadError) void handle.update();
    } catch (e) {
      if (signal.aborted) return;
      error = e instanceof Error ? e : new Error(String(e));
      void handle.update();
    }
  };
  // The poll ends with the component (the signal) and rests while the window is hidden; nothing it adds outlives one turn.
  const loop = async () => {
    await tick(true);
    while (!signal.aborted) {
      await sleep(handle.props.source.running ? 1000 : 4000, signal);
      await whenVisible(signal);
      if (signal.aborted) return;
      await tick(false);
    }
  };
  void loop();

  const earlier = async () => {
    const { taskId, source } = handle.props;
    try {
      const from = Math.max(0, start - LOG_CHUNK);
      const r = await s.client.logsRead(taskId, source.id, { from, limit: start - from });
      if (signal.aborted) return;
      start = from;
      buf.prepend(r.text);
      void handle.update();
    } catch (e) {
      error = e instanceof Error ? e : new Error(String(e));
      void handle.update();
    }
  };

  const scrollToEnd = () => {
    if (follow && box) box.scrollTop = box.scrollHeight;
  };

  return () => {
    const { source, title } = handle.props;
    handle.queueTask(scrollToEnd);
    return (
      <div mix={pane}>
        <div mix={paneHead}>
          <strong>{title}</strong>
          <span>{source.kind === 'agent' ? 'agent stream' : 'check'}</span>
          <div mix={headActions}>
            {start > 0 ? (
              <Button size="sm" variant="ghost" icon={ArrowUpToLine} mix={on('click', () => void earlier())}>
                Show earlier output
              </Button>
            ) : (
              <span mix={fromStart}>From the beginning</span>
            )}
            <Checkbox
              label="Follow"
              checked={follow}
              onChange={(v) => {
                follow = v;
                void handle.update();
              }}
            />
          </div>
        </div>
        {error ? (
          <div mix={errorSlot}>
            <ErrorNote error={error} what="Could not read the log" />
          </div>
        ) : null}
        <pre
          tabIndex={0}
          role="log"
          // the text is replaced as a whole on every poll: as a live region it would read the whole buffer out each time.
          // It stays a focusable, named log to read on demand.
          aria-live="off"
          aria-label={`${title} log`}
          data-testid="log-text"
          mix={[
            logText,
            ref((n) => {
              box = n as HTMLPreElement;
            }),
            on('scroll', (e) => {
              const el = e.currentTarget;
              const atEnd = el.scrollHeight - el.scrollTop - el.clientHeight < 8;
              if (atEnd !== follow) {
                follow = atEnd;
                void handle.update();
              }
            }),
          ]}
        >
          {buf.text()}
        </pre>
      </div>
    );
  };
}
