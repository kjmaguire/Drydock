// What a button that reaches an MCP server will do, said next to the button (audit 2026-10-16, M24). A server the person
// described, or one a file brought, may run any program on this computer when its tools are looked up. So wherever such a button
// is, the program it starts is in front of the person: its whole command line, in a box that scrolls and is never cut.

import { css, type Handle } from 'remix/component';
import type { McpLibraryServer } from '../../api/mcpClient.ts';
import { visible } from '../../lib/visible.ts';
import { t } from '../../ui/tokens.ts';
import { commandText } from './import.ts';

const commandCss = css({
  display: 'block',
  margin: '0',
  padding: '6px 10px',
  maxHeight: '120px',
  overflow: 'auto',
  borderRadius: t.rMd,
  border: `1px solid ${t.border}`,
  background: t.bgApp,
  fontFamily: t.mono,
  fontSize: t.textSm,
  whiteSpace: 'pre-wrap',
  overflowWrap: 'anywhere',
  '&:focus-visible': { outline: 'none', boxShadow: t.focus },
});

/**
 * A program's whole command line. The text comes from a file or a form, so characters that are not drawn are written out
 * (lib/visible.ts); an argument with a space in it is quoted. The box has a most height and scrolls, and the keyboard can reach it.
 */
export function CommandLine(handle: Handle<{ argv: readonly string[]; label: string; testId?: string }>) {
  return () => {
    const { argv, label, testId = 'command-line' } = handle.props;
    return (
      <pre mix={commandCss} role="group" aria-label={label} tabIndex={0} data-testid={testId}>
        {visible(commandText(argv))}
      </pre>
    );
  };
}

const lineCss = css({ margin: '0', minWidth: '0', fontSize: t.textSm, color: t.textMuted, '& code': { overflowWrap: 'anywhere' } });
const noteCss = css({ display: 'grid', gap: '6px', minWidth: '0', '& p': { margin: '0', fontSize: t.textSm, color: t.textMuted }, '& code': { overflowWrap: 'anywhere' } });

/**
 * Says what pressing a button does to a server: it starts the program (shown whole) or connects to the address. `lead` starts the
 * sentence ("Pressing Find tools"). Give the element an `id` and point the button at it with aria-describedby, so it is read with
 * the button.
 */
export function ReachNote(handle: Handle<{ server: Pick<McpLibraryServer, 'display' | 'transport' | 'url' | 'argv' | 'cwd'>; lead: string; id?: string }>) {
  return () => {
    const { server, lead, id } = handle.props;
    if (server.transport === 'http') {
      return (
        <p id={id} mix={lineCss} data-testid="reach-note" data-transport="http">
          {lead} connects to this web address: <code>{visible(server.url)}</code>
        </p>
      );
    }
    return (
      <div id={id} mix={noteCss} data-testid="reach-note" data-transport="stdio">
        <p>{lead} starts this program on this computer, with your rights:</p>
        <CommandLine argv={server.argv ?? []} label={`Command line of ${server.display}`} />
        {server.cwd ? (
          <p>
            In the folder <code>{visible(server.cwd)}</code>
          </p>
        ) : null}
      </div>
    );
  };
}
