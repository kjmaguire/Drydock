// What stands between an agent CLI and a chat, fixed in place (A9, D5): the first action fits the CLI's state. Not signed in:
// Sign in runs the CLI's own sign-in in a terminal here (connection.signIn, decision P50; Drydock never reads what it stores),
// then checks again (connection.check). Not installed: the install command with Copy and a link to its page. A notice to
// accept: its text, a tick and Accept (connection.acknowledge). "Use another CLI" comes last. Used by a chat's warning chip,
// the New chat refusal, the new chat dialog and the Inbox's agent-CLI item; the Agents page keeps the full detail.

import { css, on, type Handle, type MixInput, type RemixNode } from 'remix/component';
import { Terminal } from 'lucide';
import type { Connection, GateStatus } from '../../api/types.ts';
import { routes } from '../../routes.ts';
import { query, services } from '../../state/services.tsx';
import { Button } from '../../ui/button.tsx';
import { ErrorNote, Loading } from '../../ui/feedback.tsx';
import { Checkbox } from '../../ui/form.tsx';
import { Icon } from '../../ui/icon.tsx';
import { Popover } from '../../ui/popover.tsx';
import { t } from '../../ui/tokens.ts';
import { TerminalHost } from '../dock/terminal-panel.tsx';
import { agentNotice, agentState, isWSL, signedInLine } from '../agents/agents.ts';
import { CopyCommand } from '../agents/agents-screen.tsx';
import { adapterLabel } from './cli-name.ts';

const boxCss = css({ display: 'grid', gap: t.s3, minWidth: '0', fontSize: t.textMd, '& p': { margin: '0' }, '& .quiet': { color: t.textMuted, fontSize: t.textSm } });
const rowCss = css({ display: 'flex', flexWrap: 'wrap', alignItems: 'center', gap: t.s2 });
const codeCss = css({ display: 'flex', alignItems: 'center', gap: t.s2, minWidth: '0', '& code': { overflowWrap: 'anywhere', fontSize: t.textSm } });
const termCss = css({ display: 'grid', gap: t.s2, '& > div:last-child': { height: '220px', minHeight: '0' } });

/** The install a chat runs on: its CLI on Windows, or inside the WSL distro. */
export function connectionFor(list: readonly Connection[], adapter: string, distro?: string): Connection | undefined {
  return list.find((c) => c.adapter === adapter && (distro ? isWSL(c) && c.distro === distro : !isWSL(c)));
}

/** The distro of a Connection key ("claude@wsl:Ubuntu"), '' for the Windows install. */
export const distroOfKey = (key: string | undefined): string => (key && key.includes('@wsl:') ? key.slice(key.indexOf('@wsl:') + 5) : '');

/** What the first button of the fix does for this install: 'sign-in', 'install', 'notice', or 'check' for anything else. */
export function fixKind(c: Connection | undefined): 'sign-in' | 'install' | 'notice' | 'check' | 'ready' {
  if (!c) return 'check';
  const st = agentState(c).kind;
  if (st === 'ready') return 'ready';
  if (st === 'sign-in' && c.signIn) return 'sign-in';
  if (st === 'install') return 'install';
  if (st === 'notice') return 'notice';
  return 'check';
}

function GateAccept(handle: Handle<{ g: GateStatus; onAck: (id: string) => Promise<void> }>) {
  let ticked = false;
  let busy = false;
  return () => {
    const { g, onAck } = handle.props;
    return (
      <div style={{ display: 'grid', gap: '8px' }} data-testid="cli-fix-gate" data-gate={g.id}>
        <p>{g.text}</p>
        <div mix={rowCss}>
          <Checkbox label="I have read this and accept it" checked={ticked} onChange={(v) => ((ticked = v), void handle.update())} />
          <Button
            size="sm"
            variant="primary"
            disabled={!ticked || busy}
            data-testid="cli-fix-accept"
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
      </div>
    );
  };
}

export function CliFix(handle: Handle<{ adapter: string; distro?: string; onOther?: () => void; otherLabel?: string; onFixed?: () => void }>) {
  const s = services(handle);
  const conns = query(handle, {
    load: () => s.client.connectionList(),
    when: (e) => e.type.startsWith('connection.') && e.type !== 'connection.sign_in_started',
    key: () => `${handle.props.adapter}@${handle.props.distro ?? ''}`,
  });
  let error: Error | undefined;
  let checked: Connection | undefined;
  let checking = false;
  let signingIn = false;
  let term: string | undefined;
  let ended: number | undefined;
  const upd = (f: () => void) => (f(), void handle.update());
  const current = () => checked ?? connectionFor(conns.data ?? [], handle.props.adapter, handle.props.distro);
  const check = async () => {
    upd(() => ((checking = true), (error = undefined)));
    try {
      checked = await s.client.connectionCheck(handle.props.adapter, handle.props.distro || undefined);
      if (fixKind(checked) === 'ready') handle.props.onFixed?.();
    } catch (e) {
      error = e as Error;
    } finally {
      upd(() => (checking = false));
    }
  };
  const signIn = async () => {
    upd(() => ((signingIn = true), (error = undefined)));
    try {
      const info = await s.client.connectionSignIn(handle.props.adapter, handle.props.distro || undefined);
      if (info.id !== term) ended = undefined;
      term = info.id;
    } catch (e) {
      error = e as Error;
    } finally {
      upd(() => (signingIn = false));
    }
  };
  const ack = async (id: string) => {
    upd(() => (error = undefined));
    try {
      await s.client.connectionAcknowledge(id);
      checked = undefined;
      await conns.reload();
      if (fixKind(current()) === 'ready') handle.props.onFixed?.();
    } catch (e) {
      upd(() => (error = e as Error));
    }
  };
  handle.signal.addEventListener('abort', () => {
    if (term) void s.client.terminalClose(term).catch(() => {});
  });

  return () => {
    const { adapter, onOther, otherLabel } = handle.props;
    const name = adapterLabel(adapter);
    const c = current();
    if (conns.loading && !conns.data && !checked) return <Loading label={`Checking ${name}...`} />;
    const kind = fixKind(c);
    const notice = c ? agentNotice(c) : undefined;
    const page = routes.agents.href({ key: c?.key || adapter });
    return (
      <div mix={boxCss} data-testid="cli-fix" data-kind={kind}>
        {kind === 'ready' ? (
          <p data-testid="cli-fix-ready">{c?.authenticated ? `${name} is ready. ${signedInLine(c)}.` : `${name} is ready.`}</p>
        ) : (
          <p data-testid="cli-fix-text">{notice?.text ?? `${name} cannot be used yet.`}</p>
        )}
        {kind === 'sign-in' && c ? (
          term ? (
            <div mix={termCss} data-testid="cli-fix-terminal">
              <p class="quiet" data-testid="cli-fix-sign-in-state">
                {ended === undefined ? `${c.signIn} is running: answer it here.` : checking ? 'The sign-in ended. Checking whether the CLI is signed in now...' : ended === 0 ? 'The sign-in finished.' : `The sign-in ended with code ${ended}. Read what it printed above, then try again.`}
              </p>
              <TerminalHost
                key={term}
                id={term}
                program="the sign-in"
                onExit={(code) => {
                  ended = code;
                  void check();
                }}
              />
            </div>
          ) : (
            <div mix={rowCss}>
              <Button size="sm" variant="primary" disabled={signingIn} data-testid="cli-fix-sign-in" mix={on('click', () => void signIn())}>
                {signingIn ? 'Starting...' : 'Sign in'}
              </Button>
              <span class="quiet">
                <Icon icon={Terminal} size={12} /> Runs <code>{c.signIn}</code> here
              </span>
            </div>
          )
        ) : null}
        {kind === 'install' ? (
          c?.installHint ? (
            <div mix={codeCss} data-testid="cli-fix-install">
              <code>{c.installHint}</code>
              <CopyCommand text={c.installHint} />
            </div>
          ) : (
            <p class="quiet">Install it the way its vendor documents.</p>
          )
        ) : null}
        {kind === 'notice' && c ? (c.gates ?? []).filter((g) => !g.acked).map((g) => <GateAccept key={g.id} g={g} onAck={ack} />) : null}
        <div mix={rowCss}>
          {kind !== 'ready' ? (
            <Button size="sm" variant={kind === 'check' ? 'primary' : 'ghost'} disabled={checking} data-testid="cli-fix-check" mix={on('click', () => void check())}>
              {checking ? 'Checking...' : 'Check again'}
            </Button>
          ) : null}
          <a href={page} data-testid="cli-fix-page">
            {kind === 'install' ? 'How to install' : 'Open Agent CLIs'}
          </a>
          {onOther ? (
            <Button size="sm" variant="ghost" data-testid="cli-fix-other" mix={on('click', () => onOther())}>
              {otherLabel ?? 'Use another CLI'}
            </Button>
          ) : null}
        </div>
        <ErrorNote error={error ?? conns.error} what="Failed" />
      </div>
    );
  };
}

/** The fix in a popover from a button (a chat's warning chip, the New chat refusal, the new chat dialog). */
export function CliFixButton(
  handle: Handle<{ adapter: string; distro?: string; onOther?: () => void; otherLabel?: string; trigger?: (mix: MixInput<HTMLElement>, open: boolean) => RemixNode; label?: string; testId?: string }>,
) {
  let open = false;
  return () => {
    const { adapter, distro, onOther, otherLabel, trigger, label, testId } = handle.props;
    return (
      <Popover
        open={open}
        onOpen={() => ((open = true), void handle.update())}
        onClose={() => ((open = false), void handle.update())}
        label={`Set up ${adapterLabel(adapter)}`}
        placement="bottom-start"
        testId="cli-fix-popover"
        trigger={(mix) =>
          trigger ? (
            trigger(mix, open)
          ) : (
            <Button size="sm" variant="secondary" mix={mix} aria-expanded={open ? 'true' : 'false'} data-testid={testId ?? 'cli-fix-open'}>
              {label ?? `Set up ${adapterLabel(adapter)}`}
            </Button>
          )
        }
      >
        <CliFix adapter={adapter} distro={distro} onOther={onOther ? () => ((open = false), void handle.update(), onOther()) : undefined} otherLabel={otherLabel} onFixed={() => void handle.update()} />
      </Popover>
    );
  };
}
