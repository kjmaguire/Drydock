// Import servers from the mcp.json or claude_desktop_config.json another app uses: paste it or pick the file, see each server
// as a card (what it will run or reach, which secrets go to the credential store), choose, import. Importing only saves:
// nothing is started and no address is contacted (audit 2026-10-16, M24). A file can come from a repository and its servers can
// name any program, so a server that runs a program is not ticked for the person, says so in a badge, and shows its whole command
// line; the person ticks it only if they know where the file came from. A server is started later, by the button on its own card.

import { css, on, type Handle } from 'remix/component';
import { FileUp } from 'lucide';
import { action, services } from '../../state/services.tsx';
import { Button } from '../../ui/button.tsx';
import { Badge, faint, mono } from '../../ui/data.tsx';
import { ErrorNote } from '../../ui/feedback.tsx';
import { Field, formActions } from '../../ui/form.tsx';
import { visible } from '../../lib/visible.ts';
import { t } from '../../ui/tokens.ts';
import { CommandLine } from './command-line.tsx';
import { parseMcpConfig, runsProgram, startingChoice, type ImportResult } from './import.ts';

const wrapCss = css({ display: 'grid', gap: t.s3, '& textarea': { minHeight: '140px', fontFamily: t.mono, fontSize: t.textSm } });
const listCss = css({
  display: 'grid',
  gap: t.s2,
  margin: '0',
  padding: '0',
  listStyle: 'none',
  '& > li': { display: 'grid', gap: '6px', padding: t.s2, border: `1px solid ${t.border}`, borderRadius: t.rMd, minWidth: '0' },
  '& > li[data-kind="program"]': { borderColor: `color-mix(in srgb, ${t.warning} 45%, ${t.border})` },
  '& label': { display: 'flex', alignItems: 'center', gap: t.s2, flexWrap: 'wrap', fontWeight: '500', cursor: 'pointer' },
  '& code': { overflowWrap: 'anywhere' },
  '& p': { margin: '0' },
});

export function ImportServers(handle: Handle<{ existingIds: readonly string[]; onDone: (imported: number) => void; onCancel: () => void }>) {
  const s = services(handle);
  const act = action(handle);
  let text = '';
  let parsed: ImportResult | undefined;
  let parseError: Error | undefined;
  let chosen = new Set<string>();
  let failures: string[] = [];
  const set = (f: () => void) => {
    f();
    void handle.update();
  };
  const read = () =>
    set(() => {
      parseError = undefined;
      try {
        parsed = parseMcpConfig(text, handle.props.existingIds);
        // a server that reaches an address is ticked; a server that runs a program is left for the person to tick
        chosen = new Set(startingChoice(parsed));
      } catch (e) {
        parsed = undefined;
        parseError = e as Error;
      }
    });

  // Saving is all this does: no server is started or reached here. Its tools are looked up later, by the button on its own card.
  const run = async () => {
    if (!parsed) return;
    failures = [];
    let n = 0;
    await act.run('import', async () => {
      for (const c of parsed!.candidates.filter((x) => chosen.has(x.input.id))) {
        try {
          await s.client.mcpLibSave(c.input);
          n++;
        } catch (e) {
          failures.push(`${c.name}: ${(e as Error).message}`);
        }
      }
    });
    if (failures.length === 0) handle.props.onDone(n);
    else void handle.update();
  };

  return () => {
    const programs = parsed ? parsed.candidates.filter(runsProgram).length : 0;
    return (
      <div mix={wrapCss} data-testid="mcp-import">
        <p mix={faint} style={{ margin: '0' }}>
          Paste the file, or pick it. Claude Desktop keeps it in <code>%APPDATA%\Claude\claude_desktop_config.json</code>, Cursor in <code>.cursor\mcp.json</code>, VS Code in{' '}
          <code>.vscode\mcp.json</code>. Environment values and bearer tokens in it go to the Windows credential store. Importing only saves the servers you tick: it does not run
          a program or contact an address. A server is started later, by the button on its own card, which shows what it will run.
        </p>
        <Field label="Configuration (JSON)">
          {(id) => <textarea id={id} spellcheck={false} value={text} mix={on('input', (e) => ((text = (e.currentTarget as HTMLTextAreaElement).value), void handle.update()))} />}
        </Field>
        <div mix={formActions} style={{ justifyContent: 'flex-start' }}>
          <label>
            <span class="sr-only">Pick a configuration file</span>
            <input
              type="file"
              accept=".json,application/json"
              aria-label="Pick a configuration file"
              mix={on('change', async (e) => {
                const f = (e.currentTarget as HTMLInputElement).files?.[0];
                if (f) {
                  text = await f.text();
                  read();
                }
              })}
            />
          </label>
          <Button size="sm" icon={FileUp} mix={[on('click', read)]}>
            Read servers
          </Button>
        </div>
        <ErrorNote error={parseError ?? act.error} what="Could not import" />
        {failures.length ? <ErrorNote error={new Error(failures.join('; '))} what="Some servers were not imported" /> : null}
        {parsed ? (
          <>
            {programs > 0 ? (
              <p mix={faint} role="note" data-testid="import-programs-note">
                {programs === 1 ? 'One server in this file runs' : `${programs} servers in this file run`} a program on this computer, so {programs === 1 ? 'it is' : 'they are'} not ticked.
                Tick one only if you know where the file came from and what the program does.
              </p>
            ) : null}
            <ul mix={listCss} aria-label="Servers in the file">
              {parsed.candidates.map((c) => {
                const program = runsProgram(c);
                return (
                  <li key={c.input.id} data-testid="import-candidate" data-id={c.input.id} data-kind={program ? 'program' : 'address'}>
                    <label>
                      <input
                        type="checkbox"
                        checked={chosen.has(c.input.id)}
                        mix={on('change', (e) => set(() => ((e.currentTarget as HTMLInputElement).checked ? chosen.add(c.input.id) : chosen.delete(c.input.id))))}
                      />
                      {c.name}{' '}
                      {program ? (
                        <Badge tone="warn" testId="import-runs-program">
                          Runs a program on this computer
                        </Badge>
                      ) : (
                        <Badge>web address</Badge>
                      )}{' '}
                      <code mix={faint}>{c.input.id}</code>
                    </label>
                    {program ? (
                      <>
                        <p mix={faint}>The whole command line it would run:</p>
                        <CommandLine argv={c.input.argv ?? []} label={`Command line of ${c.name}`} testId="import-command" />
                        {c.input.cwd ? (
                          <p mix={faint}>
                            In the folder <code>{visible(c.input.cwd)}</code>
                          </p>
                        ) : null}
                      </>
                    ) : (
                      <code mix={mono} data-testid="import-address">
                        {visible(c.input.url)}
                      </code>
                    )}
                    {c.notes.map((n, i) => (
                      <span key={i} mix={faint}>
                        {n}
                      </span>
                    ))}
                  </li>
                );
              })}
              {parsed.skipped.map((x) => (
                <li key={`skip-${x.name}`} data-testid="import-skipped">
                  <span>
                    {x.name} <Badge tone="warn">not imported</Badge>
                  </span>
                  <span mix={faint}>Because {x.reason}.</span>
                </li>
              ))}
            </ul>
            <div mix={formActions}>
              <Button mix={[on('click', handle.props.onCancel)]}>Cancel</Button>
              <Button variant="primary" disabled={act.busy !== '' || chosen.size === 0} mix={[on('click', () => void run())]}>
                {act.busy ? 'Importing...' : `Import ${chosen.size} server${chosen.size === 1 ? '' : 's'}`}
              </Button>
            </div>
          </>
        ) : null}
      </div>
    );
  };
}
