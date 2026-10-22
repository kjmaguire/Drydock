// Connections: the agent CLIs found on this machine. Drydock uses the human's own installed CLI and its own sign-in; it never
// reads or transmits credentials. A CLI is usable only when it is ready and every notice on it has been accepted by the human.
// The overview is a grid of tiles (the shared kit in ui/tiles.tsx, as on Integrations): one per connection, the Windows install
// and each WSL distro's own, with the CLI's logo, what it is and its state, Claude Code first and the CLIs of custom manifests
// last (orderConnections). The selected tile's full card opens below the grid.
// Then the MCP gateway (screens/mcp/cli-gateway.tsx): which agent CLIs receive the MCP servers given to an agent. The CI
// runners (decision P56) are on the GitHub page (screens/github/ci-runners.tsx).

import { css, on, type Handle } from 'remix/component';
import { Check, CircleCheck, FlaskConical, Link2, LogIn, Minus, RefreshCw, ScrollText, ShieldAlert, Terminal, TriangleAlert, X } from 'lucide';
import type { Connection, ConnectionReport, GateStatus, WSLFacts } from '../../api/types.ts';
import { query, services } from '../../state/services.tsx';
import { TerminalHost } from '../dock/terminal-panel.tsx';
import { CliGateway } from '../mcp/cli-gateway.tsx';
import { BrandLogo, brandKey } from '../../ui/brand-logo.tsx';
import { Button } from '../../ui/button.tsx';
import { Avatar, Badge, faint, kv, muted } from '../../ui/data.tsx';
import { ErrorNote, Loading, Notice } from '../../ui/feedback.tsx';
import { Checkbox } from '../../ui/form.tsx';
import { Disclosure } from '../../ui/disclosure.tsx';
import { Icon } from '../../ui/icon.tsx';
import { card, Page } from '../../ui/layout.tsx';
import { emptyLine, Tile, tileGrid } from '../../ui/tiles.tsx';
import { t, tone } from '../../ui/tokens.ts';
import {
  cellText,
  connectionBlurb,
  connectionKey,
  connectionName,
  connectionState,
  defaultConnection,
  distroBrand,
  isWSL,
  matrixRows,
  netModeText,
  orderConnections,
  prereqText,
  provTone,
  reportMeta,
  runsText,
  signedInLine,
  signedInText,
  statusTone,
  stepTone,
} from './connections.ts';

const cardCss = css({ display: 'grid', gap: t.s3, '& p': { margin: '0' } });
const head = css({ display: 'flex', alignItems: 'center', gap: t.s2, flexWrap: 'wrap', '& h2': { fontSize: t.textLg, fontWeight: '600', margin: '0', marginRight: 'auto' } });
const runsCss = css({ display: 'flex', alignItems: 'center', gap: t.s2 });
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
const markCss = css({ display: 'inline-flex', flex: 'none', marginTop: '2px' });
const lineCss = css({ display: 'flex', gap: t.s2, alignItems: 'flex-start', fontSize: t.textMd });
const reportCss = css({ display: 'grid', gap: t.s2, padding: t.s3, borderRadius: t.rMd, border: `1px solid ${t.border}`, background: t.bgApp, '& p': { margin: '0' }, '& h4': { margin: '0' } });
const steps = css({ margin: '0', padding: '0', listStyle: 'none', display: 'grid', gap: '6px' });
const tableCss = css({ width: '100%', marginTop: t.s2, borderCollapse: 'collapse', fontSize: t.textSm, '& th, & td': { textAlign: 'left', padding: '4px 8px', borderBottom: `1px solid ${t.border}` } });
// the sign-in's own terminal, in the card: a Connection is a workspace page, open with or without a project (the dock is not)
const signInBox = css({ display: 'flex', flexDirection: 'column', height: '300px', border: `1px solid ${t.border}`, borderRadius: t.rMd, overflow: 'hidden' });
const signInHead = css({ display: 'flex', alignItems: 'center', gap: t.s2, padding: `${t.s1} ${t.s2}`, borderBottom: `1px solid ${t.border}`, '& > span': { flex: '1' } });
const problemCss = css({ padding: '6px 10px', borderRadius: t.rMd, background: t.dangerSoft, fontSize: t.textMd });
const signedInCss = css({ display: 'flex', gap: t.s2, alignItems: 'center', fontSize: t.textMd, fontWeight: '500', color: tone.ok.fg });

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

/** Plain-language result of Test connection: a verdict, the steps, what is limited, and the capability matrix with provenance. */
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
            <h3 mix={subhead}>What is limited</h3>
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
          <Disclosure summary={`Capability matrix (${matrix.length}) with where each value comes from`}>
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
          </Disclosure>
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

/** What the probe found in a WSL distro, in plain words. */
function DistroFacts(handle: Handle<{ f: WSLFacts }>) {
  return () => {
    const { f } = handle.props;
    return (
      <Disclosure summary="The distro" testId="wsl-facts">
        <dl mix={kv}>
          <dt>Windows programs from inside (interop)</dt>
          <dd>{f.interop ? 'on' : 'off: Drydock’s tools cannot reach an agent here'}</dd>
          <dt>Networking</dt>
          <dd>{netModeText(f.netMode)}</dd>
          {f.mountRoot ? (
            <>
              <dt>Windows drives at</dt>
              <dd>
                <code>{f.mountRoot}</code>
              </dd>
            </>
          ) : null}
          {f.home ? (
            <>
              <dt>Linux home</dt>
              <dd>
                <code>{f.home}</code>
              </dd>
            </>
          ) : null}
          {f.git ? (
            <>
              <dt>Git</dt>
              <dd>{f.git}</dd>
            </>
          ) : null}
        </dl>
        <ul>
          {(f.notes ?? []).map((n, i) => (
            <li key={i} mix={lineCss}>
              {n}
            </li>
          ))}
        </ul>
      </Disclosure>
    );
  };
}

/** `name` is the connection's name on the page (connectionName with the page's list: the distro only to tell two apart). */
function ConnectionCard(handle: Handle<{ c: Connection; name: string; onChanged: () => void }>) {
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
  // the CLI's own sign-in (decision P50), started by the core in a terminal shown here in the card (inside the distro for a WSL
  // install): Drydock never reads what it stores, and the card checks itself again when the sign-in ends. Asking again while one
  // runs shows the same terminal.
  let signingIn = false;
  let signInTerm: string | undefined;
  let signInEnded: number | undefined;
  // a check of this one Connection (connection.check: the sign-in asked afresh, past the probe cache), when the sign-in ends or
  // on "Check again". Its answer is shown until the page's list is read again after it (that read started later, so it is as new).
  let checking = false;
  let checkedAfterSignIn = false;
  let checked: Connection | undefined;
  let checkedFor: Connection | undefined;
  const check = async (afterSignIn: boolean) => {
    upd(() => {
      checking = true;
      error = undefined;
    });
    try {
      const c = handle.props.c;
      const now = await s.client.connectionCheck(c.adapter, isWSL(c) ? c.distro : undefined);
      checked = now;
      checkedFor = handle.props.c;
      if (afterSignIn) checkedAfterSignIn = true;
      handle.props.onChanged();
    } catch (e) {
      error = e as Error;
    } finally {
      upd(() => (checking = false));
    }
  };
  const signIn = async () => {
    upd(() => {
      signingIn = true;
      error = undefined;
    });
    try {
      const c = handle.props.c;
      const term = await s.client.connectionSignIn(c.adapter, isWSL(c) ? c.distro : undefined);
      if (term.id !== signInTerm) {
        signInEnded = undefined;
        checkedAfterSignIn = false;
      }
      signInTerm = term.id;
    } catch (e) {
      error = e as Error;
    } finally {
      upd(() => (signingIn = false));
    }
  };
  const closeSignIn = async () => {
    const id = signInTerm;
    upd(() => {
      signInTerm = undefined;
      signInEnded = undefined;
    });
    if (id) await s.client.terminalClose(id).catch(() => {}); // already gone: nothing to stop
  };
  const runTest = async () => {
    upd(() => {
      testing = true;
      error = undefined;
      confirming = false;
    });
    try {
      const c = handle.props.c;
      report = isWSL(c) ? await s.client.connectionTest(c.adapter, prompt, c.distro) : await s.client.connectionTest(c.adapter, prompt);
      handle.props.onChanged();
    } catch (e) {
      error = e as Error;
    } finally {
      upd(() => (testing = false));
    }
  };

  return () => {
    if (checked && handle.props.c !== checkedFor) checked = undefined; // the list was read again after the check
    const c = checked ?? handle.props.c;
    const inWSL = isWSL(c);
    const signedIn = c.authenticated === true;
    const checkAgain = (
      <Button size="sm" variant="ghost" icon={RefreshCw} disabled={checking} data-testid="sign-in-check" mix={on('click', () => void check(false))} title="Ask the CLI again whether it is signed in">
        {checking ? 'Checking...' : 'Check again'}
      </Button>
    );
    return (
      <article
        mix={[card, cardCss]}
        data-testid="connection"
        data-adapter={c.adapter}
        data-key={connectionKey(c)}
        data-run-mode={inWSL ? 'wsl' : 'native'}
        data-usable={String(c.usable)}
      >
        <header mix={head}>
          {brandKey(c.adapter) ? <BrandLogo id={c.adapter} size={32} /> : <Avatar id={c.adapter} name={c.adapter} size={32} square />}
          <h2>{handle.props.name}</h2>
          <Badge tone={statusTone(c.status)}>{c.status}</Badge>
          <Badge tone={c.usable ? 'ok' : 'warn'} title="Usable means the CLI is ready and every notice below is accepted">
            {c.usable ? 'usable' : 'not usable yet'}
          </Badge>
        </header>
        <dl mix={kv}>
          <dt>Runs</dt>
          {/* the distro is named here, in the card's details, not in its name (the owner: "we don't need to see the Ubuntu") */}
          <dd mix={inWSL ? runsCss : undefined}>
            {inWSL ? <BrandLogo id={distroBrand(c.distro)} size={16} label /> : null}
            {runsText(c)}
          </dd>
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
        {signedIn ? (
          <p mix={signedInCss} data-testid="signed-in" data-tone="ok">
            <Icon icon={CircleCheck} size={15} />
            {signedInLine(c)}
          </p>
        ) : c.authDetail ? (
          <p mix={muted}>{c.authDetail}</p>
        ) : null}
        {c.installed && c.signIn ? (
          <div mix={rowCss}>
            <Button size="sm" icon={LogIn} disabled={signingIn || (!!signInTerm && signInEnded === undefined)} data-testid="sign-in" mix={on('click', () => void signIn())}>
              {signingIn ? 'Starting the sign-in...' : inWSL ? `Sign in inside ${c.distro}` : 'Sign in'}
            </Button>
            {checkAgain}
            <span mix={faint}>
              {inWSL
                ? `Runs the CLI's own sign-in (${c.signIn}) inside the distro, in a terminal here: this install's sign-in is separate from the Windows one. Drydock never reads or copies it.`
                : `Runs the CLI's own sign-in (${c.signIn}) in a terminal here. Drydock never reads or copies what it stores, and this card checks itself again when the sign-in ends.`}
            </span>
          </div>
        ) : (
          <>
            {inWSL ? (
              <p mix={faint}>
                This install has its own sign-in, separate from Windows: open a terminal in the distro (<code>wsl -d {c.distro}</code>) and sign in the way the CLI documents.
                Drydock never reads or copies it.
              </p>
            ) : null}
            {c.installed ? <div mix={rowCss}>{checkAgain}</div> : null}
          </>
        )}
        {signInTerm ? (
          <div mix={signInBox} data-testid="sign-in-terminal">
            <div mix={signInHead}>
              <Icon icon={Terminal} size={14} />
              <span mix={faint} data-testid="sign-in-state">
                {signInEnded === undefined
                  ? `${c.signIn} is running: answer it here.`
                  : checking
                    ? 'The sign-in ended. Checking whether the CLI is signed in now...'
                    : checkedAfterSignIn && signedIn
                      ? `The sign-in finished. ${signedInLine(c)}.`
                      : signInEnded === 0
                        ? checkedAfterSignIn
                          ? 'The sign-in finished, but the CLI still does not say it is signed in. Read what it printed above, then check again or sign in again.'
                          : 'The sign-in finished.'
                        : `The sign-in ended with code ${signInEnded}. Read what it printed above, then try again.`}
              </span>
              <Button size="sm" variant="ghost" icon={X} data-testid="sign-in-close" mix={on('click', () => void closeSignIn())} title="Close: stops the sign-in if it still runs">
                Close
              </Button>
            </div>
            <TerminalHost
              key={signInTerm}
              id={signInTerm}
              program="the sign-in"
              onExit={(code) => {
                signInEnded = code;
                void check(true);
              }}
            />
          </div>
        ) : null}
        {c.wsl ? <DistroFacts f={c.wsl} /> : null}
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
          <Disclosure summary="Prerequisites">
            <ul mix={steps}>
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
          </Disclosure>
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
            <p>
              Test starts the real {c.adapter} CLI {inWSL ? `inside the WSL distro ${c.distro ?? ''}` : 'on this machine'} and checks that it starts, answers and stops.
            </p>
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

/** One connection on the overview: the CLI's logo, its name ("Claude Code WSL" for a WSL install), what it is and its state. */
function ConnectionTile(handle: Handle<{ c: Connection; name: string; selected: boolean; onSelect: () => void }>) {
  return () => {
    const { c, name, selected, onSelect } = handle.props;
    const st = connectionState(c);
    return (
      <Tile
        title={name}
        logo={c.adapter}
        icon={Terminal}
        blurb={connectionBlurb(c)}
        state={st.text}
        tone={st.tone}
        selected={selected}
        onSelect={onSelect}
        testId="connection-tile"
        data={{ key: connectionKey(c), adapter: c.adapter, 'run-mode': isWSL(c) ? 'wsl' : 'native', usable: String(c.usable) }}
      />
    );
  };
}

/** `initial` opens that connection (its key) instead of the first one that needs attention. */
export function ConnectionsScreen(handle: Handle<{ initial?: string }>) {
  const s = services(handle);
  // a started sign-in changes nothing yet; its end (connection.sign_in_finished) does
  const q = query(handle, { load: () => s.client.connectionList(), when: (e) => e.type.startsWith('connection.') && e.type !== 'connection.sign_in_started' });
  let selected = handle.props.initial ?? '';
  return () => {
    // Claude Code first (its WSL installs right after it), then the other vendors by name, the custom manifests' CLIs last
    const list = orderConnections(q.data ?? []);
    const usable = list.filter((c) => c.usable).length;
    // the chosen tile; until one is chosen (or when the chosen one is gone) the first that needs attention, else the first.
    // It stays chosen: accepting its notice does not move the selection to the next one that needs attention.
    if (q.data && !list.some((c) => connectionKey(c) === selected)) selected = defaultConnection(list);
    const key = selected;
    const open = list.find((c) => connectionKey(c) === key);
    return (
      <Page title="Connections" icon={Link2} subtitle={q.data ? `${usable} of ${list.length} usable` : undefined} guide="connect-cli">
        <ErrorNote error={q.error} what="Could not list connections" />
        {q.loading && !q.data ? <Loading label="Detecting..." /> : null}
        {q.data && list.length === 0 ? (
          <p mix={emptyLine}>
            <Icon icon={ShieldAlert} size={14} />
            <strong>No agent CLIs found.</strong> Install an agent CLI and sign in with it; it appears here.
          </p>
        ) : null}
        {list.length > 0 ? (
          <ul mix={tileGrid} aria-label="Agent CLIs">
            {list.map((c) => (
              <ConnectionTile
                key={connectionKey(c)}
                c={c}
                name={connectionName(c, list)}
                selected={connectionKey(c) === key}
                onSelect={() => ((selected = connectionKey(c)), void handle.update())}
              />
            ))}
          </ul>
        ) : null}
        {open ? <ConnectionCard key={connectionKey(open)} c={open} name={connectionName(open, list)} onChanged={q.reload} /> : null}
        <CliGateway />
      </Page>
    );
  };
}
