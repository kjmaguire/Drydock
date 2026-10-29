// Agents (decision P98; it was Connections): the agent CLIs found on this machine, as a list beside the chosen one's detail.
// Drydock uses the person's own installed CLI and its own sign-in; it never reads or transmits credentials. A CLI is usable
// only when it is ready and every notice on it has been accepted by the person.
// The list (the kit's MasterDetail) groups the installs: Installed (the Windows install and each WSL distro's own, Claude Code
// first, the CLIs of custom manifests last: orderConnections), then Not installed. The detail leads with its header (Test
// connection, Sign in, Check again) and at most one notice, then three tabs: Overview (the notices to accept, the facts, the
// distro, the MCP gateway's switch for this CLI and the crew agents that run on it), Test results, Install and update.
// Choosing a row puts it in the address (/agents/<key>, replacing the entry); the controller keeps the page mounted.

import { css, navigate, on, ref, type Handle } from 'remix/component';
import { Check, CircleCheck, Copy, FlaskConical, LogIn, Minus, Plus, RefreshCw, ScrollText, ShieldAlert, Terminal, TriangleAlert, X } from 'lucide';
import type { McpAdapterRow } from '../../api/mcpClient.ts';
import type { Agent, Connection, ConnectionReport, CustomCLIs, GateStatus, WSLFacts } from '../../api/types.ts';
import { routes } from '../../routes.ts';
import { action, query, services, watch } from '../../state/services.tsx';
import { TerminalHost } from '../dock/terminal-panel.tsx';
import { AddCustomCLI, CustomCLIPanel } from './custom-cli.tsx';
import { adapterLabel, cliName } from '../shared/cli-name.ts';
import { supportsWSL } from '../team/run-mode.ts';
import { BrandLogo } from '../../ui/brand-logo.tsx';
import { Button } from '../../ui/button.tsx';
import { Avatar, Badge, faint, muted } from '../../ui/data.tsx';
import { Dialog } from '../../ui/dialog.tsx';
import { Disclosure } from '../../ui/disclosure.tsx';
import { Empty, ErrorNote, Loading } from '../../ui/feedback.tsx';
import { Checkbox, Switch } from '../../ui/form.tsx';
import { Icon } from '../../ui/icon.tsx';
import { Page } from '../../ui/layout.tsx';
import { DetailHeader, FactGrid, MasterDetail, SettingRow, SettingsGroup, type ListItem } from '../../ui/master-detail.tsx';
import { Tabs } from '../../ui/tabs.tsx';
import { t, tone } from '../../ui/tokens.ts';
import {
  agentGroups,
  agentNotice,
  agentState,
  agentSub,
  cellText,
  connectionKey,
  defaultAgent,
  distroBrand,
  gatewayState,
  headerLine,
  isInstalled,
  isWSL,
  matrixRows,
  netModeText,
  prereqText,
  provTone,
  readyLine,
  reportMeta,
  runsText,
  signedInLine,
  signedInText,
  stepTone,
  usedBy,
  type AgentNotice,
} from './agents.ts';

const MINUTE = 60_000;

const padCss = css({ padding: `${t.s5} ${t.s6}` });
const markCss = css({ display: 'inline-flex', flex: 'none', marginTop: '2px' });
const lineCss = css({ display: 'flex', gap: t.s2, alignItems: 'flex-start', fontSize: t.textMd });
const rowCss = css({ display: 'flex', alignItems: 'center', gap: t.s3, flexWrap: 'wrap' });
const subhead = css({ fontSize: t.textXs, fontWeight: '600', letterSpacing: '0.04em', textTransform: 'uppercase', color: t.textMuted, margin: '0' });
const reportCss = css({ display: 'grid', gap: t.s2, padding: t.s4, borderRadius: t.rLg, border: `1px solid ${t.border}`, background: t.bgSurface, '& p': { margin: '0' } });
const stepsCss = css({ margin: '0', padding: '0', listStyle: 'none', display: 'grid', gap: '6px' });
const tableCss = css({ width: '100%', marginTop: t.s2, borderCollapse: 'collapse', fontSize: t.textSm, '& th, & td': { textAlign: 'left', padding: '4px 8px', borderBottom: `1px solid ${t.border}` } });
const gateCss = css({
  display: 'grid',
  gap: t.s2,
  padding: t.s3,
  borderRadius: t.rLg,
  border: `1px solid color-mix(in srgb, ${t.warning} 40%, ${t.border})`,
  background: t.warningSoft,
  '&[data-acked="true"]': { borderColor: t.border, background: t.bgSurface },
  '& p': { margin: '0', fontSize: t.textMd },
});
const noticeCss = css({
  display: 'flex',
  alignItems: 'center',
  flexWrap: 'wrap',
  gap: t.s2,
  margin: '0',
  fontSize: t.textMd,
  '& > span:first-of-type': { display: 'inline-flex', flex: 'none' },
  '& > [data-part="text"]': { flex: '1 1 260px', minWidth: '0' },
});
const signInBox = css({ display: 'flex', flexDirection: 'column', height: '300px', border: `1px solid ${t.border}`, borderRadius: t.rLg, overflow: 'hidden' });
const signInHead = css({ display: 'flex', alignItems: 'center', gap: t.s2, padding: `${t.s1} ${t.s2}`, borderBottom: `1px solid ${t.border}`, '& > span': { flex: '1' } });
const panelCss = css({ display: 'grid', gap: t.s5, paddingTop: t.s4, minWidth: '0' });
const quietP = css({ margin: '0', color: t.textMuted, fontSize: t.textSm });
const signedInCss = css({ display: 'inline-flex', gap: '6px', alignItems: 'center', color: tone.ok.fg });
const runsCss = css({ display: 'inline-flex', alignItems: 'center', gap: t.s2 });
const whoCss = css({
  display: 'inline-flex',
  alignItems: 'center',
  gap: t.s2,
  minWidth: '0',
  '& a': { display: 'inline-flex', alignItems: 'center', minHeight: '28px', minWidth: '28px', color: t.text, fontWeight: '500', textDecoration: 'none' },
  '& a:hover': { textDecoration: 'underline' },
});
const noteListCss = css({ margin: '0', padding: `${t.s2} ${t.s4}`, listStyle: 'none', display: 'grid', gap: t.s2, fontSize: t.textMd });
const flushList = css({ padding: '0' });
const detailCss = css({ display: 'grid', gap: t.s4, minWidth: '0' });
const confirmCss = css({ display: 'grid', gap: t.s3, '& p': { margin: '0' } });
const codeRow = css({ display: 'flex', alignItems: 'center', gap: t.s2, minWidth: '0', '& code': { overflowWrap: 'anywhere' } });

/** A read whose failure leaves the page as it is without it (an older core). */
const orElse = async <T,>(read: () => Promise<T>, fallback: T): Promise<T> => {
  try {
    return await read();
  } catch {
    return fallback;
  }
};

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
        <ol mix={stepsCss}>
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
            <ul mix={stepsCss}>
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

/** A notice the person must read and accept before the CLI is usable. Only a person can accept it: a tick, then Accept. */
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
              mix={on('click', async () => {
                busy = true;
                void handle.update();
                try {
                  await onAck(g.id);
                } finally {
                  busy = false;
                  void handle.update();
                }
              })}
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
    const facts = [
      { label: 'Windows programs from inside (interop)', value: f.interop ? 'On' : "Off: Drydock's tools cannot reach an agent here" },
      { label: 'Networking', value: netModeText(f.netMode) },
      ...(f.mountRoot ? [{ label: 'Windows drives at', value: f.mountRoot, mono: true }] : []),
      ...(f.home ? [{ label: 'Linux home', value: f.home, mono: true }] : []),
      ...(f.git ? [{ label: 'Git', value: f.git }] : []),
    ];
    return (
      <SettingsGroup title="The distro" label="The distro" bare>
        <FactGrid facts={facts} label="The distro" testId="wsl-facts" />
        {(f.notes ?? []).length > 0 ? (
          <ul mix={[noteListCss, flushList]} data-testid="wsl-notes">
            {(f.notes ?? []).map((n, i) => (
              <li key={i} mix={quietP}>
                {n}
              </li>
            ))}
          </ul>
        ) : null}
      </SettingsGroup>
    );
  };
}

/** A command to copy: Drydock shows install guidance, it never runs it. */
function CopyCommand(handle: Handle<{ text: string }>) {
  let copied = false;
  let timer: ReturnType<typeof setTimeout> | undefined;
  handle.signal.addEventListener('abort', () => clearTimeout(timer));
  const copy = async () => {
    try {
      await navigator.clipboard.writeText(handle.props.text);
      copied = true;
      void handle.update();
      clearTimeout(timer);
      timer = setTimeout(() => ((copied = false), void handle.update()), 1500);
    } catch {
      /* the clipboard can be refused; the command is still there to select */
    }
  };
  return () => (
    <Button size="sm" variant="ghost" icon={copied ? Check : Copy} data-testid="copy-install" mix={on('click', () => void copy())}>
      {copied ? 'Copied' : 'Copy'}
    </Button>
  );
}

/** A crew agent that runs on the install, with the name of its project. */
export interface CrewUse {
  agent: Agent;
  project: string;
}

type Tab = 'overview' | 'tests' | 'install';

interface DetailProps {
  c: Connection;
  /** The install's name on the page (cliName with the page's list: the distro only to tell two apart). */
  name: string;
  checkedAt?: number;
  now: number;
  gateway?: McpAdapterRow;
  gatewayError?: unknown;
  gatewayRunning?: boolean;
  gatewayBusy: boolean;
  /** Another install of the same CLI is listed: the gateway's setting is the CLI's, for all of them. */
  shared: boolean;
  readOnly: boolean;
  onGateway: (enabled: boolean) => void;
  report?: ConnectionReport;
  testing: boolean;
  testError?: unknown;
  onTest: (prompt: boolean) => void;
  crew: CrewUse[];
  onOpenAgent: (a: Agent) => void;
  onChanged: () => void;
  /** The person's own CLIs (connection.custom.list), for a custom CLI's panel. */
  customs?: CustomCLIs;
}

/** The chosen install: its header with the actions, one notice, and the tabs. */
function AgentDetail(handle: Handle<DetailProps>) {
  const s = services(handle);
  let tab: Tab = 'overview';
  let confirming = false;
  let prompt = false; // the only step that spends tokens: never without the person ticking it
  let error: Error | undefined;
  let detailEl: HTMLElement | null = null;
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
  // the CLI's own sign-in (decision P50), started by the core in a terminal shown here (inside the distro for a WSL install):
  // Drydock never reads what it stores, and the detail checks itself again when the sign-in ends. Asking again while one runs
  // shows the same terminal.
  let signingIn = false;
  let signInTerm: string | undefined;
  let signInEnded: number | undefined;
  // a check of this one install (connection.check: the sign-in asked afresh, past the probe cache), when the sign-in ends or on
  // "Check again". Its answer is shown until the page's list is read again after it (that read started later, so it is as new).
  let checking = false;
  let checkedAfterSignIn = false;
  let checked: Connection | undefined;
  let checkedFor: Connection | undefined;
  let checkedAt = 0;
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
      checkedAt = Date.now();
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
  const startTest = () =>
    upd(() => {
      confirming = false;
      tab = 'tests';
      handle.props.onTest(prompt);
    });
  const fix = (n: AgentNotice) => {
    if (n.fix === 'install') upd(() => (tab = 'install'));
    else if (n.fix === 'sign-in') void signIn();
    else if (n.fix === 'test') upd(() => (confirming = true));
    else if (n.fix === 'check') void check(false);
    else if (n.fix === 'notice') {
      handle.queueTask(() => detailEl?.querySelector<HTMLElement>('[data-testid="gate"][data-acked="false"] input')?.focus());
      upd(() => (tab = 'overview'));
    }
  };

  return () => {
    const p = handle.props;
    if (checked && p.c !== checkedFor) checked = undefined; // the list was read again after the check
    const c = checked ?? p.c;
    const inWSL = isWSL(c);
    const installed = isInstalled(c);
    const signedIn = c.authenticated === true;
    const st = agentState(c);
    const notice = agentNotice(c);
    const signInBusy = signingIn || (!!signInTerm && signInEnded === undefined);
    const where = inWSL ? `inside ${c.distro ?? 'the distro'}` : 'on this PC';
    const gw = gatewayState(p.gateway);
    const product = adapterLabel(c.adapter);
    const crew = p.crew;
    const projects = new Set(crew.map((u) => u.agent.projectId)).size;
    // the core's other problem lines and warnings (the notice already says one of them)
    const notes = [...(c.problems ?? []).filter((x) => x !== notice?.from), ...(c.warnings ?? [])];
    // say it once (the UI audit of 2026-10-26: "Untested" in the list, "Version not tested yet" in the header and the notice, and
    // Test connection twice): with a notice the header keeps no state pill, and the notice offers its fix only when the header's
    // own buttons do not already (Test connection, Sign in, Check again are there)
    const fixInHeader =
      !!notice && ((notice.fix === 'test' && installed) || (notice.fix === 'sign-in' && installed && !!c.signIn) || notice.fix === 'check');

    const actions = (
      <>
        {installed ? (
          <Button variant="primary" size="sm" icon={FlaskConical} disabled={p.testing} data-testid="test-connection" mix={on('click', () => upd(() => (confirming = true)))}>
            {p.testing ? 'Testing...' : 'Test connection'}
          </Button>
        ) : null}
        {installed && c.signIn ? (
          <Button
            size="sm"
            icon={LogIn}
            disabled={signInBusy}
            data-testid="sign-in"
            title={inWSL ? `Runs ${c.signIn} inside ${c.distro ?? 'the distro'}` : `Runs ${c.signIn} in a terminal here`}
            mix={on('click', () => void signIn())}
          >
            {signingIn ? 'Starting the sign-in...' : signedIn ? 'Sign in again' : 'Sign in'}
          </Button>
        ) : null}
        <Button size="sm" variant="ghost" icon={RefreshCw} disabled={checking} data-testid="sign-in-check" mix={on('click', () => void check(false))} title="Ask the CLI again whether it is ready and signed in">
          {checking ? 'Checking...' : 'Check again'}
        </Button>
      </>
    );

    const overview = (
      <div mix={panelCss}>
        {(c.gates ?? []).length > 0 ? (
          <SettingsGroup title={(c.gates ?? []).length === 1 ? 'Notice' : 'Notices'} bare testId="gates">
            {(c.gates ?? []).map((g) => (
              <Gate key={g.id} g={g} onAck={ack} />
            ))}
          </SettingsGroup>
        ) : null}
        <SettingsGroup title="This install" bare>
          <FactGrid
            label="This install"
            testId="agent-facts"
            facts={[
              { label: 'Version', value: c.version ? <>{c.version}{c.compatClass ? <span mix={muted}> (evidence: {c.compatClass})</span> : null}</> : 'Not found', mono: !!c.version },
              { label: 'Program', value: c.path ?? 'Not found', mono: !!c.path },
              {
                label: 'Signed in',
                value: signedIn ? (
                  <span mix={signedInCss} data-testid="signed-in" data-tone="ok" title={signedInLine(c)}>
                    <Icon icon={CircleCheck} size={14} />
                    {signedInText(c)}
                  </span>
                ) : (
                  signedInText(c)
                ),
              },
              { label: 'Transport', value: c.transport || 'Not reported' },
              {
                label: 'Runs',
                value: (
                  <span mix={inWSL ? runsCss : undefined}>
                    {inWSL ? <BrandLogo id={distroBrand(c.distro)} size={16} label /> : null}
                    {runsText(c).replace(/^./, (x) => x.toUpperCase())}
                  </span>
                ),
              },
            ]}
          />
          {installed && c.signIn ? (
            <p mix={quietP} data-testid="sign-in-about">
              {inWSL
                ? `Its own sign-in (${c.signIn}) runs inside the distro, in a terminal here: this install's sign-in is separate from the Windows one. Drydock never reads or copies it.`
                : `Its own sign-in (${c.signIn}) runs in a terminal here. Drydock never reads or copies what it stores.`}
            </p>
          ) : inWSL ? (
            <p mix={quietP} data-testid="sign-in-about">
              This install has its own sign-in, separate from Windows: open a terminal in the distro (<code>wsl -d {c.distro}</code>) and sign in the way the CLI documents. Drydock never
              reads or copies it.
            </p>
          ) : !signedIn && c.authDetail ? (
            <p mix={quietP}>{c.authDetail}</p>
          ) : null}
        </SettingsGroup>
        {c.wsl ? <DistroFacts f={c.wsl} /> : null}
        <SettingsGroup title="MCP gateway" testId="cli-gateway" label="MCP gateway">
          <SettingRow
            label="Give it MCP servers"
            testId="gateway-row"
            control={
              gw.canSwitch || gw.kind === 'verified' ? (
                <Switch
                  label={<span class="sr-only">{`Let ${c.adapter} use the gateway`}</span>}
                  checked={gw.on}
                  disabled={!gw.canSwitch || p.readOnly || p.gatewayBusy}
                  onChange={(v) => p.onGateway(v)}
                />
              ) : null
            }
          >
            <span data-testid="gateway-state" data-kind={gw.kind} mix={muted}>
              {p.gatewayRunning === false ? 'The gateway is not running. ' : ''}
              {gw.text}
              {p.shared ? ` It applies to every ${product} install.` : ''}
            </span>
          </SettingRow>
        </SettingsGroup>
        <ErrorNote error={p.gatewayError} what="MCP gateway" />
        <SettingsGroup
          title="Used by"
          testId="used-by"
          label="Used by"
          actions={crew.length > 0 ? <span mix={faint}>{`${crew.length} ${crew.length === 1 ? 'agent' : 'agents'} in ${projects} ${projects === 1 ? 'project' : 'projects'}`}</span> : undefined}
        >
          {crew.length === 0 ? (
            <SettingRow label={<span mix={faint}>No agents</span>}>
              <span mix={muted}>No crew agent runs on this install yet.</span>
            </SettingRow>
          ) : (
            crew.map((u) => (
              <SettingRow
                key={u.agent.id}
                testId="used-by-agent"
                label={
                  <span mix={whoCss}>
                    <Avatar id={u.agent.id} name={u.agent.name} size={22} kind="agent" />
                    <a href={routes.agent.href({ id: u.agent.id })} mix={on('click', () => p.onOpenAgent(u.agent))}>
                      {u.agent.name}
                    </a>
                  </span>
                }
              >
                <span mix={muted}>{[u.project, u.agent.role, u.agent.model].filter(Boolean).join(' · ')}</span>
              </SettingRow>
            ))
          )}
        </SettingsGroup>
        {notes.length > 0 ? (
          <SettingsGroup title="Also" testId="agent-notes" label="Also">
            <ul mix={noteListCss}>
              {notes.map((n, i) => (
                <li key={`note:${i}`} mix={lineCss}>
                  <span mix={markCss} style={{ color: tone.warn.fg }} aria-hidden="true">
                    <Icon icon={TriangleAlert} size={14} />
                  </span>
                  {n}
                </li>
              ))}
            </ul>
          </SettingsGroup>
        ) : null}
      </div>
    );

    const tests = (
      <div mix={panelCss} data-testid="test-results">
        {p.testing ? <Loading label={`Testing ${p.name}...`} /> : null}
        <ErrorNote error={p.testError} what="Test connection" />
        {p.report ? (
          <ReportView r={p.report} />
        ) : !p.testing ? (
          <p mix={quietP}>
            {installed ? `No test of ${p.name} in this session yet. Test connection starts the real CLI ${where} and checks that it starts, answers and stops.` : `${p.name} is not installed.`}
          </p>
        ) : null}
      </div>
    );

    const prereqs = c.prereqs ?? [];
    const install = (
      <div mix={panelCss} data-testid="install-tab">
        {c.custom && c.adapter !== 'custom' && !inWSL ? <CustomCLIPanel adapter={c.adapter} files={p.customs} readOnly={p.readOnly} onRemoved={p.onChanged} /> : null}
        <SettingsGroup title={installed ? 'Update' : 'Install'}>
          {c.installHint ? (
            <SettingRow label="Command" control={<CopyCommand text={c.installHint} />}>
              <span mix={codeRow}>
                <code data-testid="install-hint">{c.installHint}</code>
              </span>
            </SettingRow>
          ) : null}
          <SettingRow label="Who runs it">
            <span mix={muted}>
              {`Drydock never runs this for you: install or update ${product} yourself${inWSL ? ` inside the distro (wsl -d ${c.distro ?? ''})` : ''} the way its vendor documents, then check again.`}
            </span>
          </SettingRow>
          {installed ? (
            <SettingRow label="Found">
              <span>{[c.version, c.path].filter(Boolean).join(' · ') || 'Found, version not reported'}</span>
            </SettingRow>
          ) : null}
        </SettingsGroup>
        {prereqs.length > 0 ? (
          <SettingsGroup title="Prerequisites" testId="prereqs">
            <ul mix={noteListCss}>
              {prereqs.map((x) => (
                <li key={x.tool} data-ok={String(x.ok)} mix={lineCss}>
                  <Mark status={x.ok ? 'pass' : 'fail'} />
                  <span>
                    {prereqText(x)}
                    {x.guide ? <div mix={faint}>{x.guide}</div> : null}
                  </span>
                </li>
              ))}
            </ul>
          </SettingsGroup>
        ) : null}
      </div>
    );

    return (
      <article
        mix={[detailCss, ref((el) => (detailEl = el as HTMLElement))]}
        aria-label={p.name}
        data-testid="agent-detail"
        data-adapter={c.adapter}
        data-key={connectionKey(c)}
        data-run-mode={inWSL ? 'wsl' : 'native'}
        data-usable={String(c.usable)}
        data-state={st.kind}
      >
        <DetailHeader
          title={p.name}
          logo={c.adapter}
          icon={Terminal}
          status={notice ? undefined : { text: st.pill, tone: st.tone }}
          sub={headerLine(c, checked ? checkedAt : p.checkedAt, p.now)}
          actions={actions}
          testId="agent-header"
        />
        {notice ? (
          <p mix={noticeCss} data-testid="agent-notice" data-tone={notice.tone} data-fix={notice.fix}>
            <span style={{ color: tone[notice.tone].fg }} aria-hidden="true">
              <Icon icon={TriangleAlert} size={14} />
            </span>
            <span data-part="text">{notice.text}</span>
            {notice.fix && notice.label && !fixInHeader && !(notice.fix === 'sign-in' && signInBusy) ? (
              <Button size="sm" variant="ghost" data-testid="agent-notice-fix" disabled={(notice.fix === 'test' && p.testing) || (notice.fix === 'check' && checking)} mix={on('click', () => fix(notice))}>
                {notice.label}
              </Button>
            ) : null}
          </p>
        ) : null}
        <ErrorNote error={error} what="Failed" />
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
        <Tabs
          label={`${p.name} sections`}
          items={[
            { name: 'overview', label: 'Overview', testId: 'agent-tab-overview' },
            { name: 'tests', label: 'Test results', testId: 'agent-tab-tests' },
            { name: 'install', label: 'Install and update', testId: 'agent-tab-install' },
          ]}
          active={tab}
          onChange={(n) => upd(() => (tab = n as Tab))}
        >
          {(n) => (n === 'tests' ? tests : n === 'install' ? install : overview)}
        </Tabs>
        <Dialog open={confirming} title="Test connection" onClose={() => upd(() => (confirming = false))}>
          <div mix={confirmCss} data-testid="test-confirm">
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
              <Button variant="primary" mix={on('click', () => startTest())}>
                Run the test
              </Button>
              <Button mix={on('click', () => upd(() => (confirming = false)))}>Cancel</Button>
            </div>
          </div>
        </Dialog>
      </article>
    );
  };
}

/**
 * The Agents page. `initial` opens that install (its key, from /agents/<key>) instead of the first that needs attention;
 * `onNavigate` replaces the router's navigate (tests).
 */
export function AgentsScreen(handle: Handle<{ initial?: string; onNavigate?: (href: string) => void }>) {
  const s = services(handle);
  watch(handle, s.workspace);
  // connection.list probes every CLI and distro afresh ("Check all again" reads it again); a started sign-in changes nothing
  // yet, its end (connection.sign_in_finished) does
  const q = query(handle, { load: () => s.client.connectionList(), when: (e) => e.type.startsWith('connection.') && e.type !== 'connection.sign_in_started' });
  const gateway = query(handle, { load: () => s.client.mcpStatus(), when: (e) => e.type.startsWith('mcp.') });
  const gwAct = action(handle);
  // every project's agents, and where each project runs them, for "Used by"
  const crew = query(handle, { load: () => orElse(() => s.client.agentListAll(), [] as Agent[]), when: (e) => e.type.startsWith('agent.') });
  // the adapters with the run modes each declares. No fallback here: a first read that fails leaves no answer, so the detail
  // says so while "Used by" goes by the installs found; a re-read that fails keeps the last answer (the query does), and the
  // note says the re-read failed
  const adapters = query(handle, { load: () => s.client.adapterList(), when: (e) => e.type.startsWith('connection.') || e.type.startsWith('adapter.') });
  // the person's own CLIs (decision P115): the folder, and a file that did not load with why; an older core has none
  const customs = query(handle, { load: () => orElse(() => s.client.connectionCustomList(), { dir: '', files: [] } as CustomCLIs), when: (e) => e.type.startsWith('connection.custom') });
  let adding = false;
  // a CLI just added: chosen once the list has it (a list read before it was added would put the choice back on the first row)
  let pendingKey = '';
  // whether an adapter can run in WSL, as the core decides where an agent runs (effectiveRunMode): once the adapters are known,
  // the adapter's declared run modes decide, and an adapter the core does not list cannot. Only while no answer has arrived yet
  // (the first read is on its way, or failed) does a WSL install of the CLI stand in for the answer.
  const canWSL = (adapter: string): boolean => {
    if (adapters.data) return supportsWSL(adapters.data.find((x) => x.id === adapter)?.capabilities);
    return (q.data ?? []).some((x) => x.adapter === adapter && isWSL(x));
  };
  // every project's run mode in one request (project.runModes), however many projects there are. A failed read leaves no
  // answer: an agent that follows its project is then listed under the Windows install, one with its own run mode where that
  // says, and the detail says the read failed.
  const runModes = query(handle, {
    load: async () => new Map(Object.entries(await s.client.projectRunModes())),
    when: (e) => e.type.startsWith('project.'),
  });

  // "Check all again": connection.list read again (the query keeps the old list on the page until it answers)
  let checkingAll = false;
  const checkAll = async () => {
    checkingAll = true;
    void handle.update();
    await q.reload();
    checkingAll = false;
    void handle.update();
  };

  let selected = handle.props.initial ?? '';
  let lastInitial = handle.props.initial;
  // when the list was last read (connection.list probes afresh each time)
  let seen: Connection[] | undefined;
  let checkedAt = 0;
  let now = Date.now();
  const timer = setInterval(() => ((now = Date.now()), void handle.update()), MINUTE);
  handle.signal.addEventListener('abort', () => clearInterval(timer));

  // Test connection, per install: its report stays when another row is chosen
  const reports = new Map<string, ConnectionReport>();
  const testErrors = new Map<string, unknown>();
  const testing = new Set<string>();
  const runTest = async (c: Connection, prompt: boolean) => {
    const key = connectionKey(c);
    testing.add(key);
    testErrors.delete(key);
    void handle.update();
    try {
      const r = isWSL(c) ? await s.client.connectionTest(c.adapter, prompt, c.distro) : await s.client.connectionTest(c.adapter, prompt);
      reports.set(key, r);
      void q.reload();
    } catch (e) {
      testErrors.set(key, e);
    } finally {
      testing.delete(key);
      void handle.update();
    }
  };

  const setGateway = async (adapter: string, enabled: boolean) => {
    const st = await gwAct.run(`try:${adapter}`, () => s.client.mcpTrySet(adapter, enabled));
    if (st) gateway.set(st);
  };

  // the key the address names (the page's own choice goes in it, replacing the entry, so Back leaves the page)
  let addressed = handle.props.initial ?? '';
  const choose = (key: string) => {
    selected = key;
    void handle.update();
    if (key === addressed) return;
    addressed = key;
    const href = routes.agents.href({ key });
    if (handle.props.onNavigate) handle.props.onNavigate(href);
    else void navigate(href, { history: 'replace' }).catch(() => {});
  };

  const openAgent = (a: Agent) => {
    // an agent of another project: its page reads the open project's crew, so open that project first
    if (a.projectId && a.projectId !== s.workspace.projectId) s.workspace.selectProject(a.projectId);
  };

  return () => {
    if (handle.props.initial !== lastInitial) {
      lastInitial = handle.props.initial;
      addressed = handle.props.initial ?? '';
      if (handle.props.initial) selected = handle.props.initial;
    }
    if (q.data !== seen) {
      seen = q.data;
      checkedAt = Date.now();
      now = checkedAt;
    }
    const list = q.data ?? [];
    const { installed, missing } = agentGroups(list);
    // the chosen row; until one is chosen (or when the chosen one is gone) the first that needs attention, else the first. It
    // stays chosen: accepting its notice does not move the selection to the next one that needs attention.
    if (pendingKey && list.some((c) => connectionKey(c) === pendingKey)) {
      const k = pendingKey;
      pendingKey = '';
      selected = k;
      handle.queueTask(() => choose(k));
    }
    if (q.data && !list.some((c) => connectionKey(c) === selected)) selected = defaultAgent(list);
    const open = list.find((c) => connectionKey(c) === selected);
    const rowOf = (c: Connection): ListItem => {
      const st = agentState(c);
      return {
        key: connectionKey(c),
        title: cliName(c, list),
        logo: c.adapter,
        icon: Terminal,
        sub: agentSub(c),
        status: { text: st.word, tone: st.kind === 'install' ? 'none' : st.tone },
        quiet: !isInstalled(c),
        testId: 'agent-row',
        data: { adapter: c.adapter, 'run-mode': isWSL(c) ? 'wsl' : 'native', usable: String(c.usable), state: st.kind },
      };
    };
    const groups = [
      ...(installed.length > 0 ? [{ key: 'installed', title: 'Installed', note: String(installed.length), items: installed.map(rowOf) }] : []),
      ...(missing.length > 0 ? [{ key: 'missing', title: 'Not installed', note: String(missing.length), items: missing.map(rowOf) }] : []),
    ];
    const projectName = (id: string) => s.workspace.projectList.find((p) => p.id === id)?.name ?? '';
    const gwRows = gateway.data?.adapters ?? [];

    let detail = null;
    if (open) {
      const key = connectionKey(open);
      detail = (
        <AgentDetail
          key={key}
          c={open}
          name={cliName(open, list)}
          checkedAt={checkedAt}
          now={now}
          gateway={gwRows.find((r) => r.adapter === open.adapter)}
          gatewayError={gateway.error ?? gwAct.error}
          gatewayRunning={gateway.data?.running}
          gatewayBusy={gwAct.busy !== ''}
          shared={list.some((x) => x !== open && x.adapter === open.adapter && isInstalled(x))}
          readOnly={s.workspace.license.readOnly}
          onGateway={(v) => void setGateway(open.adapter, v)}
          report={reports.get(key)}
          testing={testing.has(key)}
          testError={testErrors.get(key)}
          onTest={(prompt) => void runTest(open, prompt)}
          crew={usedBy(open, crew.data ?? [], runModes.data ?? new Map(), list, canWSL).map((a) => ({ agent: a, project: projectName(a.projectId) }))}
          onOpenAgent={openAgent}
          onChanged={() => void q.reload()}
          customs={customs.data}
        />
      );
    }

    return (
      <Page
        title="Agents"
        icon={Terminal}
        subtitle={q.data ? readyLine(list) : 'The agent CLIs your crew runs on'}
        guide="connect-cli"
        flush
        testId="agents-page"
        actions={
          <>
            <Button size="sm" variant="ghost" icon={Plus} disabled={s.workspace.license.readOnly} data-testid="custom-cli-open" mix={on('click', () => ((adding = true), void handle.update()))} title="Add your own agent CLI from a manifest">
              Add a custom CLI
            </Button>
            <Button size="sm" variant="ghost" icon={RefreshCw} disabled={checkingAll || q.loading} data-testid="check-all" mix={on('click', () => void checkAll())} title="Look for every agent CLI again, on Windows and in each WSL distro">
              {checkingAll ? 'Checking...' : 'Check all again'}
            </Button>
          </>
        }
      >
        <AddCustomCLI
          open={adding}
          dir={customs.data?.dir ?? ''}
          taken={list.map((x) => x.adapter)}
          onClose={() => ((adding = false), void handle.update())}
          onAdded={(c) => {
            adding = false;
            pendingKey = connectionKey(c);
            void customs.reload();
            void q.reload();
          }}
        />
        {(customs.data?.files ?? []).filter((f) => f.error).length > 0 ? (
          <div mix={padCss} data-testid="custom-cli-broken">
            <ErrorNote
              error={new Error(
                (customs.data?.files ?? [])
                  .filter((f) => f.error)
                  .map((f) => `${f.path}: ${f.error}`)
                  .join('\n'),
              )}
              what="A custom CLI was not loaded"
            />
          </div>
        ) : null}
        {q.error && !q.data ? (
          <div mix={padCss}>
            <ErrorNote error={q.error} what="Could not list the agent CLIs" />
          </div>
        ) : null}
        {q.loading && !q.data ? (
          <div mix={padCss}>
            <Loading label="Looking for agent CLIs..." />
          </div>
        ) : null}
        {q.data && list.length === 0 ? (
          <div mix={padCss} data-testid="agents-empty">
            <Empty title="No agent CLIs found" icon={ShieldAlert}>
              <p>Install an agent CLI and sign in with it, then check again. It appears here.</p>
            </Empty>
          </div>
        ) : null}
        {list.length > 0 ? (
          <MasterDetail
            label="Agent CLIs"
            groups={groups}
            selected={selected}
            onSelect={choose}
            search="Filter agents"
            detail={
              <>
                {q.error ? <ErrorNote error={q.error} what="Could not check the agent CLIs again" /> : null}
                {adapters.error ? <ErrorNote error={adapters.error} what="Could not read the adapters' run modes" /> : null}
                {runModes.error ? <ErrorNote error={runModes.error} what="Could not read where the projects' agents run" /> : null}
                {detail}
              </>
            }
            testId="agents"
            address={handle.props.initial}
          />
        ) : null}
      </Page>
    );
  };
}
