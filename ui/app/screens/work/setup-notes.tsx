// What failed after the project was made (B2), one line each on the project's Work page with the action that fixes it: a CLI not
// usable gets its fix in place (Sign in, or Set up <CLI>: the shared CliFixButton), a decision waiting goes to the Inbox, a
// connector to the project's Integrations. Each line stays (lib/setup-notes.ts, this browser) until the person dismisses it; a CLI
// line goes by itself once a check finds the CLI usable (connection.list {cached}: ready and its notice accepted, which is what
// project.setup asked; adapter.list knows nothing of notices). Reading the kept checks starts no CLI.

import { css, on, type Handle } from 'remix/component';
import { TriangleAlert, X } from 'lucide';
import type { Connection, SetupWarning } from '../../api/types.ts';
import { dropSetupNote, setupNotes } from '../../lib/setup-notes.ts';
import { routes } from '../../routes.ts';
import { query, services } from '../../state/services.tsx';
import { IconButton } from '../../ui/button.tsx';
import { Icon } from '../../ui/icon.tsx';
import { t } from '../../ui/tokens.ts';
import { CliFixButton } from '../shared/cli-fix.tsx';
import { adapterLabel } from '../shared/cli-name.ts';
import { browserStore } from '../workspace/start.ts';

const listCss = css({ display: 'grid', gap: '2px', margin: '0', padding: `0 ${t.s3} ${t.s2}`, listStyle: 'none' });
const lineCss = css({
  display: 'flex',
  alignItems: 'flex-start',
  gap: t.s2,
  padding: `${t.s1} ${t.s2}`,
  borderRadius: t.rMd,
  background: t.warningSoft,
  fontSize: t.textSm,
  '& > svg': { flex: 'none', marginTop: '2px', color: t.warning },
  '& .text': { flex: '1', minWidth: '0', overflowWrap: 'anywhere' },
  '& .acts': { display: 'flex', alignItems: 'center', gap: t.s1, flex: 'none' },
});

/** The action a line offers: a fix in place for a CLI, a link for a decision or a connector, none for the rest. */
export function noteAction(w: SetupWarning): { kind: 'cli'; label: string } | { kind: 'link'; label: string; href: string } | undefined {
  if (w.kind === 'cli' && w.adapter) return { kind: 'cli', label: w.status === 'unauthenticated' ? 'Sign in' : `Set up ${adapterLabel(w.adapter)}` };
  if (w.kind === 'decision') return { kind: 'link', label: 'Decide', href: routes.needsYou.href() };
  if (w.kind === 'connector') return { kind: 'link', label: 'Open Integrations', href: routes.projectIntegrations.href() };
  return undefined;
}

/** Whether a CLI line is resolved: the newest check of that CLI on Windows (the install project.setup checked) finds it usable. */
export function cliNoteResolved(w: SetupWarning, checked: readonly Connection[] | undefined): boolean {
  if (w.kind !== 'cli' || !w.adapter) return false;
  const c = checked?.find((x) => x.adapter === w.adapter && x.runMode !== 'wsl');
  return !!c && c.usable;
}

export function SetupNotes(handle: Handle<{ projectId: string }>) {
  const s = services(handle);
  const store = browserStore();
  let lines = setupNotes(store, handle.props.projectId);
  const conns = query(handle, {
    enabled: () => lines.some((w) => w.kind === 'cli'),
    load: () => s.client.connectionList(true),
    when: (e) => e.type.startsWith('connection.') && e.type !== 'connection.sign_in_started',
  });
  const drop = (w: SetupWarning) => {
    lines = dropSetupNote(store, handle.props.projectId, w);
    void handle.update();
  };

  return () => {
    // a CLI line is resolved once its CLI is ready: it goes, and is not kept
    for (const w of lines) {
      if (cliNoteResolved(w, conns.data)) handle.queueTask(() => drop(w));
    }
    const shown = lines.filter((w) => !cliNoteResolved(w, conns.data));
    if (shown.length === 0) return null;
    return (
      <ul mix={listCss} aria-label="What did not work when the project was made" data-testid="setup-notes">
        {shown.map((w) => {
          const a = noteAction(w);
          return (
            <li key={`${w.kind}:${w.text}`} mix={lineCss} data-testid="setup-note" data-kind={w.kind}>
              <Icon icon={TriangleAlert} size={13} />
              <span class="text">{w.text}</span>
              <span class="acts">
                {a?.kind === 'cli' ? <CliFixButton adapter={w.adapter!} label={a.label} testId="setup-note-fix" /> : null}
                {a?.kind === 'link' ? (
                  <a href={a.href} data-testid="setup-note-link">
                    {a.label}
                  </a>
                ) : null}
                <IconButton icon={X} size={13} label="Dismiss" data-testid="setup-note-dismiss" mix={[on('click', () => drop(w))]} />
              </span>
            </li>
          );
        })}
      </ul>
    );
  };
}
