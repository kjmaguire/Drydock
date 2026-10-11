// Connections: the agent CLIs found on this machine. Drydock uses the human's own installed CLI and its own sign-in; it never
// reads or transmits credentials. A CLI is usable only when it is ready and every notice on it has been accepted by the human.

import { css, on, type Handle } from 'remix/component';
import { Check, CircleCheck, FlaskConical, Link2, Minus, ScrollText, TriangleAlert, X } from 'lucide';
import type { Connection, ConnectionReport, GateStatus } from '../../api/types.ts';
import { query, services } from '../../state/services.tsx';
import { Button } from '../../ui/button.tsx';
import { Avatar, Badge, faint, kv, muted } from '../../ui/data.tsx';
import { Empty, ErrorNote, Loading, Notice } from '../../ui/feedback.tsx';
import { Checkbox } from '../../ui/form.tsx';
import { Icon } from '../../ui/icon.tsx';
import { card, Page } from '../../ui/layout.tsx';
import { t, tone } from '../../ui/tokens.ts';
import { cellText, matrixRows, prereqText, provTone, reportMeta, signedInText, statusTone, stepTone } from './connections.ts';

const introCss = css({ margin: '0', color: t.textMuted, fontSize: t.textMd, maxWidth: '86ch' });
const grid = css({ display: 'grid', gridTemplateColumns: 'repeat(auto-fill, minmax(420px, 1fr))', gap: t.s4, alignItems: 'start', '@media (max-width: 520px)': { gridTemplateColumns: '1fr' } });
const cardCss = css({ display: 'grid', gap: t.s3, '& p': { margin: '0' } });
const head = css({ display: 'flex', alignItems: 'center', gap: t.s2, flexWrap: 'wrap', '& h2': { fontSize: t.textLg, fontWeight: '600', margin: '0', marginRight: 'auto' } });
const subhead = css({ fontSize: t.textXs, fontWeight: '600', letterSpacing: '0.04em', textTransform: 'uppercase', color: t.textMuted, margin: '0' });
const gateCss = css({
  display: 'grid',
  gap: t.s2,
  padding: t.s3,
  borderRadius: t.rMd,
  border: `1px solid color-mix(in srgb, ${t.warning} 40%, ${t.border})`,
  background: t.warningSoft,
  '&[data-acked="true"]': { borderColor: t.border, background: t.bgApp },
  '& p': { margin: '0', fontSize: t.textMd },
});
const rowCss = css({ display: 'flex', alignItems: 'center', gap: t.s3, flexWrap: 'wrap' });
const confirmCss = css({ display: 'grid', gap: t.s2, padding: t.s3, borderRadius: t.rMd, background: t.warningSoft });
const detailsCss = css({ '& summary': { cursor: 'pointer', fontSize: t.textSm, color: t.textMuted }, '& ul': { margin: `${t.s2} 0 0`, padding: '0', listStyle: 'none', display: 'grid', gap: '6px' } });
const markCss = css({ display: 'inline-flex', flex: 'none', marginTop: '2px' });
const lineCss = css({ display: 'flex', gap: t.s2, alignItems: 'flex-start', fontSize: t.textMd });
const reportCss = css({ display: 'grid', gap: t.s2, padding: t.s3, borderRadius: t.rMd, border: `1px solid ${t.border}`, background: t.bgApp, '& p': { margin: '0' }, '& h4': { margin: '0' } });
const steps = css({ margin: '0', padding: '0', listStyle: 'none', display: 'grid', gap: '6px' });
const tableCss = css({ width: '100%', marginTop: t.s2, borderCollapse: 'collapse', fontSize: t.textSm, '& th, & td': { textAlign: 'left', padding: '4px 8px', borderBottom: `1px solid ${t.border}` } });
const problemCss = css({ padding: '6px 10px', borderRadius: t.rMd, background: t.dangerSoft, fontSize: t.textMd });

function Mark(handle: Handle<{ status: 'pass' | 'fail' | 'skip' }>) {
  return () => {
    const st = handle.props.status;
    const c = st === 'pass' ? tone.ok.fg : st === 'fail' ? tone.err.fg : tone.muted.fg;
    return (
      <span mix={markCss} style={{ color: c }} aria-hidden="true">
        <Icon icon={st === 'pass' ? Check : st === 'fail' ? X : Minus} size={14} />
      </span>
    );
  };
}

/** Plain-language result of Test connection: a verdict, the steps, what is degraded, and the capability matrix with provenance. */
export function ReportView(handle: Handle<{ r: ConnectionReport }>) {
  return () => {
    const { r } = handle.props;
    const matrix = matrixRows(r);
    return (
      <div mix={reportCss} data-testid="connection-report" data-ok={String(r.ok)}>
        <p>
          <Badge tone={r.ok ? 'ok' : 'err'}>{r.ok ? 'passed' : 'did not pass'}</Badge> {r.summary}
        </p>
        <p mix={faint}>{reportMeta(r)}</p>
        <h3 mix={subhead}>Steps</h3>
        <ol mix={steps}>
          {(r.steps ?? []).map((s) => (
            <li key={s.id} data-status={s.status} mix={lineCss}>
              <Mark status={s.status === 'pass' ? 'pass' : s.status === 'fail' ? 'fail' : 'skip'} />
              <span>
                <strong>{s.id}</strong> {s.name}: <span style={{ color: tone[stepTone(s.status)].fg }}>{s.status}</span>
                {s.reason ? <span mix={muted}> ({s.reason})</span> : null}
                {s.detail ? <div mix={faint}>{s.detail}</div> : null}
              </span>
            </li>
          ))}
        </ol>
        {(r.degraded ?? []).length > 0 ? (
          <>
            <h3 mix={subhead}>What is degraded</h3>
            <ul mix={steps}>
              {(r.degraded ?? []).map((d, i) => (
                <li key={i} mix={lineCss}>
                  <span mix={markCss} style={{ color: tone.warn.fg }}>
                    <Icon icon={TriangleAlert} size={14} />
                  </span>
                  {d}
                </li>
              ))}
            </ul>
          </>
        ) : null}
        {matrix.length > 0 ? (
          <details mix={detailsCss}>
            <summary>Capability matrix ({matrix.length}) with where each value comes from</summary>
            <table mix={tableCss} aria-label="Capability matrix">
              <thead>
                <tr>
                  <th scope="col">Capability</th>
                  <th scope="col">Value</th>
                  <th scope="col">Evidence</th>
                </tr>
              </thead>
              <tbody>
                {matrix.map(([k, c]) => (
                  <tr key={k}>
                    <td>{k}</td>
                    <td>{cellText(c)}</td>
                    <td>
                      <Badge tone={provTone(c.provenance)} title="observed: seen during this test. declared: the CLI says so. unknown: not verified.">
                        {c.provenance}
                      </Badge>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </details>
        ) : null}
      </div>
    );
  };
}

/** A notice the human must read and accept before the CLI is usable. Only a person can accept it: a tick, then Accept. */
function Gate(handle: Handle<{ g: GateStatus; onAck: (id: string) => Promise<void> }>) {
  let checked = false;
  let busy = false;
  return () => {
    const { g, onAck } = handle.props;
    return (
      <div mix={gateCss} data-testid="gate" data-gate={g.id} data-acked={String(g.acked)}>
        <p mix={lineCss}>
          <span mix={markCss} style={{ color: g.acked ? tone.ok.fg : tone.warn.fg }}>
            <Icon icon={g.acked ? CircleCheck : ScrollText} size={15} />
          </span>
          <span>{g.text}</span>
        </p>
        {g.acked ? (
          <div>
            <Badge tone="ok">accepted</Badge>
          </div>
        ) : (
          <div mix={rowCss}>
            <Checkbox label="I have read this and accept it" checked={checked} onChange={(c) => ((checked = c), void handle.update())} />
            <Button
              size="sm"
              variant="primary"
              disabled={!checked || busy}
              mix={[
                on('click', async () => {
                  busy = true;
                  void handle.update();
                  try {
                    await onAck(g.id);
                  } finally {
                    busy = false;
                    void handle.update();
                  }
                }),
              ]}
            >
              Accept
            </Button>
          </div>
        )}
      </div>
    );
  };
}

function ConnectionCard(handle: Handle<{ c: Connection; onChanged: () => void }>) {
  const s = services(handle);
  let confirming = false;
  let testing = false;
  let prompt = false; // the only step that spends tokens: never without the person ticking it
  let report: ConnectionReport | undefined;
  let error: Error | undefined;
  const upd = (f: () => void) => {
    f();
    void handle.update();
  };
  const ack = async (id: string) => {
    upd(() => (error = undefined));
    try {
      await s.client.connectionAcknowledge(id);
      handle.props.onChanged();
    } catch (e) {
      upd(() => (error = e as Error));
    }
  };
  const runTest = async () => {
    upd(() => {
      testing = true;
      error = undefined;
      confirming = false;
    });
    try {
      report = await s.client.connectionTest(handle.props.c.adapter, prompt);
      handle.props.onChanged();
    } catch (e) {
      error = e as Error;
    } finally {
      upd(() => (testing = false));
    }
  };

  return () => {
    const { c } = handle.props;
    return (
      <article mix={[card, cardCss]} data-testid="connection" data-adapter={c.adapter} data-usable={String(c.usable)}>
        <header mix={head}>
          <Avatar id={c.adapter} name={c.adapter} size={32} square />
          <h2>{c.adapter}</h2>
          <Badge tone={statusTone(c.status)}>{c.status}</Badge>
          <Badge tone={c.usable ? 'ok' : 'warn'} title="Usable means the CLI is ready and every notice below is accepted">
            {c.usable ? 'usable' : 'not usable yet'}
          </Badge>
        </header>
        <dl mix={kv}>
          <dt>Installed</dt>
          <dd>{c.installed ? 'yes' : 'no'}</dd>
          {c.version ? (
            <>
              <dt>Version</dt>
              <dd>
                {c.version}
                {c.compatClass ? <span mix={muted}> (evidence: {c.compatClass})</span> : null}
              </dd>
            </>
          ) : null}
          {c.path ? (
            <>
              <dt>Path</dt>
              <dd>
                <code>{c.path}</code>
              </dd>
            </>
          ) : null}
          <dt>Signed in</dt>
          <dd>{signedInText(c)}</dd>
          {c.transport ? (
            <>
              <dt>Transport</dt>
              <dd>{c.transport}</dd>
            </>
          ) : null}
          <dt>Platform</dt>
          <dd>{c.platform}</dd>
          {c.reason ? (
            <>
              <dt>Reason</dt>
              <dd>
                <code>{c.reason}</code>
              </dd>
            </>
          ) : null}
        </dl>
        {c.authDetail ? <p mix={muted}>{c.authDetail}</p> : null}
        {(c.problems ?? []).map((p, i) => (
          <p key={i} mix={problemCss}>
            {p}
          </p>
        ))}
        {(c.warnings ?? []).map((w, i) => (
          <Notice key={i} tone="warn">
            {w}
          </Notice>
        ))}
        {c.installHint ? (
          <p mix={faint}>
            Install guidance (Drydock never runs this for you): <code>{c.installHint}</code>
          </p>
        ) : null}
        {(c.prereqs ?? []).length > 0 ? (
          <details mix={detailsCss}>
            <summary>Prerequisites</summary>
            <ul>
              {(c.prereqs ?? []).map((p) => (
                <li key={p.tool} data-ok={String(p.ok)} mix={lineCss}>
                  <Mark status={p.ok ? 'pass' : 'fail'} />
                  <span>
                    {prereqText(p)}
                    {p.guide ? <div mix={faint}>{p.guide}</div> : null}
                  </span>
                </li>
              ))}
            </ul>
          </details>
        ) : null}
        {(c.gates ?? []).map((g) => (
          <Gate key={g.id} g={g} onAck={ack} />
        ))}
        <ErrorNote error={error} what="Failed" />
        {!confirming ? (
          <div mix={rowCss}>
            <Button size="sm" icon={FlaskConical} disabled={testing} mix={[on('click', () => upd(() => (confirming = true)))]}>
              {testing ? 'Testing...' : 'Test connection...'}
            </Button>
          </div>
        ) : (
          <div mix={confirmCss}>
            <p>Test starts the real {c.adapter} CLI on this machine and checks that it starts, answers and stops.</p>
            <Checkbox
              label="Also send one tiny prompt"
              description="On the cheapest model. It uses your account: a fraction of a cent. Without it the answer is not checked."
              checked={prompt}
              onChange={(v) => upd(() => (prompt = v))}
            />
            <div mix={rowCss}>
              <Button size="sm" variant="primary" mix={[on('click', () => void runTest())]}>
                Run the test
              </Button>
              <Button size="sm" mix={[on('click', () => upd(() => (confirming = false)))]}>
                Cancel
              </Button>
            </div>
          </div>
        )}
        {report ? <ReportView r={report} /> : null}
      </article>
    );
  };
}

export function ConnectionsScreen(handle: Handle) {
  const s = services(handle);
  const q = query(handle, { load: () => s.client.connectionList(), when: (e) => e.type.startsWith('connection.') });
  return () => {
    const list = q.data ?? [];
    const usable = list.filter((c) => c.usable).length;
    return (
      <Page title="Connections" icon={Link2} subtitle={q.data ? `${usable} of ${list.length} usable` : undefined}>
        <p mix={introCss}>
          Agent CLIs found on this machine. Drydock uses your own installed CLI and its own sign-in; it never reads or transmits your credentials. A CLI is usable only when it is ready
          and every notice below has been accepted by you.
        </p>
        <ErrorNote error={q.error} what="Could not list connections" />
        {q.loading && !q.data ? <Loading label="Detecting..." /> : null}
        {q.data && list.length === 0 ? (
          <div mix={card}>
            <Empty title="No agent CLIs found" icon={Link2}>
              <p>Install an agent CLI and sign in with it; it appears here.</p>
            </Empty>
          </div>
        ) : null}
        <div mix={grid}>
          {list.map((c) => (
            <ConnectionCard key={c.adapter} c={c} onChanged={q.reload} />
          ))}
        </div>
      </Page>
    );
  };
}
