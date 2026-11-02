// The app a worker started in the task's sandbox (decision P134): forward its port to this computer and open it in Drydock's own
// window, in one click ("Open the app": sandbox.forward.show forwards, then opens; the forwarded ports and Stop fold under it). That window is a process of its own with a browser profile of its own, deleted when it closes, and no bridge to Drydock:
// the page is code an agent wrote, so it is never opened in the person's browser, which holds the cookies of their other local apps.

import { css, on, type Handle } from 'remix/component';
import { ExternalLink, Square } from 'lucide';
import { action, query, services, watch } from '../../state/services.tsx';
import { Button } from '../../ui/button.tsx';
import { muted } from '../../ui/data.tsx';
import { ErrorNote } from '../../ui/feedback.tsx';
import { Field } from '../../ui/form.tsx';
import { card } from '../../ui/layout.tsx';
import { t } from '../../ui/tokens.ts';

const boxCss = css({
  display: 'grid',
  gap: t.s3,
  '& > p': { margin: '0' },
  '& ul': { listStyle: 'none', margin: '0', padding: '0', display: 'grid', gap: t.s2 },
  '& li': { display: 'flex', alignItems: 'center', gap: t.s3, flexWrap: 'wrap' },
  '& li > .where': { flex: '1', minWidth: '200px' },
  '& form': { display: 'flex', alignItems: 'end', gap: t.s3, flexWrap: 'wrap' },
  '& details > summary': { cursor: 'pointer', fontSize: t.textSm, color: t.textMuted },
  '& details > ul': { marginTop: t.s2 },
  '& input[type=number]': { width: '120px' },
});

const sameTask = (taskId: string) => (e: { type: string; taskId?: string }) =>
  e.taskId === taskId && (e.type.startsWith('sandbox.') || e.type === 'task.completed' || e.type === 'task.failed' || e.type === 'task.cancelled');

export function SandboxApp(handle: Handle<{ taskId: string }>) {
  const s = services(handle);
  watch(handle, s.workspace);
  const act = action(handle);
  const q = query(handle, {
    load: () => s.client.sandboxForwards(handle.props.taskId),
    key: () => handle.props.taskId,
    when: sameTask(handle.props.taskId),
  });
  let port = '';
  /** Whether the person typed in the port field: until then it holds the port forwarded last, so opening it again is one click. */
  let typed = false;
  let portError: string | undefined;
  /** The port this panel last forwarded or opened: the core lists forwards sorted by port, so the list's last is not necessarily it. */
  let lastPort: number | undefined;
  const set = (f: () => void) => {
    f();
    void handle.update();
  };
  /** The port in the field: what the person typed, or the port forwarded last here while it is still forwarded, else the list's last. */
  const shownPort = (v: { forwards: { port: number }[] }) => {
    if (typed) return port;
    if (lastPort !== undefined && v.forwards.some((f) => f.port === lastPort)) return String(lastPort);
    return v.forwards.length > 0 ? String(v.forwards[v.forwards.length - 1]!.port) : '';
  };
  /** With a viewer, "Open the app" forwards and opens in one call; without one, only forwarding is possible here. */
  const forward = async (value: string, show: boolean) => {
    const n = Number(value);
    if (value === '' || !Number.isInteger(n) || n < 1 || n > 65535) {
      set(() => (portError = 'Enter a port from 1 to 65535.'));
      return;
    }
    set(() => (portError = undefined));
    const taskId = handle.props.taskId;
    const f = await act.run('open', () => (show ? s.client.sandboxForwardShow(taskId, n) : s.client.sandboxForwardOpen(taskId, n)));
    if (f) {
      set(() => ((port = ''), (typed = false), (lastPort = f.port)));
      q.reload();
    }
  };

  return () => {
    const { taskId } = handle.props;
    const v = q.data;
    // a task with no running sandbox has nothing to forward
    if (!v || !v.running) return null;
    const ro = s.workspace.license.readOnly;
    const value = shownPort(v);
    const atMax = v.forwards.length >= v.max;
    // a port already forwarded opens again at the limit; a new one does not
    const canSubmit = v.canView || !atMax;
    return (
      <section aria-label="Sandbox app" style={{ display: 'grid', gap: '12px' }} data-testid="sandbox-app">
        <h2 style={{ fontSize: 'var(--text-md)', fontWeight: '600' }}>Sandbox app</h2>
        <ErrorNote error={act.error ?? q.error} what="Sandbox app" />
        <div mix={[card, boxCss]}>
          <p mix={muted}>
            Show a web app a worker started in this task&apos;s sandbox. Its port is reachable from this computer only, and it opens in a Drydock window of its own that
            shares nothing with Drydock or with your browser. It is never opened in your browser.
          </p>
          {v.error ? <p data-testid="sandbox-app-error">{v.error}</p> : null}
          {!v.canView ? <p mix={muted}>This window cannot open the app: use the Drydock desktop app.</p> : null}
          {canSubmit ? (
            <form novalidate mix={on('submit', (e) => (e.preventDefault(), void forward(value, v.canView)))}>
              <Field label="Port in the sandbox" error={portError}>
                {(id) => (
                  <input
                    id={id}
                    type="number"
                    min="1"
                    max="65535"
                    inputmode="numeric"
                    placeholder="3000"
                    value={value}
                    disabled={ro}
                    mix={on('input', (e) => set(() => ((port = e.currentTarget.value), (typed = true))))}
                  />
                )}
              </Field>
              <Button type="submit" variant="primary" icon={v.canView ? ExternalLink : undefined} disabled={ro || value === '' || !!act.busy} data-testid="sandbox-forward-add">
                {act.busy === 'open' ? (v.canView ? 'Opening...' : 'Forwarding...') : v.canView ? 'Open the app' : 'Forward port'}
              </Button>
            </form>
          ) : null}
          {atMax ? <p mix={muted}>{v.max} ports are forwarded, the most a task can have. Stop one to forward another.</p> : null}
          {v.forwards.length > 0 ? (
            <details data-testid="sandbox-forwards">
              <summary>Forwarded ports ({v.forwards.length})</summary>
              <ul aria-label="Forwarded ports">
                {v.forwards.map((f) => (
                  <li key={f.port} data-testid={`sandbox-forward-${f.port}`}>
                    <span class="where">
                      Port {f.port} <span mix={muted}>at {f.address}</span>
                    </span>
                    <Button
                      size="sm"
                      icon={ExternalLink}
                      disabled={ro || !v.canView || !!act.busy}
                      title={v.canView ? undefined : 'Only the Drydock desktop window can open it'}
                      data-testid={`sandbox-forward-open-${f.port}`}
                      mix={on('click', () => void act.run(`view${f.port}`, () => s.client.sandboxForwardView(taskId, f.port)))}
                    >
                      Open
                    </Button>
                    <Button
                      size="sm"
                      variant="ghost"
                      icon={Square}
                      disabled={ro || !!act.busy}
                      data-testid={`sandbox-forward-stop-${f.port}`}
                      mix={on('click', async () => {
                        await act.run(`stop${f.port}`, () => s.client.sandboxForwardClose(taskId, f.port));
                        q.reload();
                      })}
                    >
                      Stop
                    </Button>
                  </li>
                ))}
              </ul>
            </details>
          ) : null}
        </div>
      </section>
    );
  };
}
