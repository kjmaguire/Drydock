// Import servers from the mcp.json or claude_desktop_config.json another app uses: paste it or pick the file, see each server
// as a card (what it will run or reach, which secrets go to the credential store), choose, import. Each imported server is
// then reached once so it shows what it says about itself.

import { css, on, type Handle } from 'remix/component';
import { FileUp } from 'lucide';
import { action, services } from '../../state/services.tsx';
import { Button } from '../../ui/button.tsx';
import { Badge, faint, mono } from '../../ui/data.tsx';
import { ErrorNote } from '../../ui/feedback.tsx';
import { Field, formActions } from '../../ui/form.tsx';
import { t } from '../../ui/tokens.ts';
import { parseMcpConfig, type ImportResult } from './import.ts';

const wrapCss = css({ display: 'grid', gap: t.s3, '& textarea': { minHeight: '140px', fontFamily: t.mono, fontSize: t.textSm } });
const listCss = css({
  display: 'grid',
  gap: t.s2,
  margin: '0',
  padding: '0',
  listStyle: 'none',
  '& > li': { display: 'grid', gap: '4px', padding: t.s2, border: `1px solid ${t.border}`, borderRadius: t.rMd },
  '& label': { display: 'flex', alignItems: 'center', gap: t.s2, fontWeight: '500', cursor: 'pointer' },
  '& code': { overflowWrap: 'anywhere' },
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
        chosen = new Set(parsed.candidates.map((c) => c.input.id));
      } catch (e) {
        parsed = undefined;
        parseError = e as Error;
      }
    });

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
          continue;
        }
        try {
          await s.client.mcpLibDiscover(c.input.id);
        } catch {
          // kept on the server's card as its last error
        }
      }
    });
    if (failures.length === 0) handle.props.onDone(n);
    else void handle.update();
  };

  return () => (
    <div mix={wrapCss} data-testid="mcp-import">
      <p mix={faint} style={{ margin: '0' }}>
        Paste the file, or pick it. Claude Desktop keeps it in <code>%APPDATA%\Claude\claude_desktop_config.json</code>, Cursor in <code>.cursor\mcp.json</code>, VS Code in{' '}
        <code>.vscode\mcp.json</code>. Environment values and bearer tokens in it go to the Windows credential store.
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
      {failures.length ? (
        <ErrorNote error={new Error(failures.join('; '))} what="Some servers were not imported" />
      ) : null}
      {parsed ? (
        <>
          <ul mix={listCss} aria-label="Servers in the file">
            {parsed.candidates.map((c) => (
              <li key={c.input.id} data-testid="import-candidate" data-id={c.input.id}>
                <label>
                  <input
                    type="checkbox"
                    checked={chosen.has(c.input.id)}
                    mix={on('change', (e) => set(() => ((e.currentTarget as HTMLInputElement).checked ? chosen.add(c.input.id) : chosen.delete(c.input.id))))}
                  />
                  {c.name} <Badge>{c.input.transport === 'http' ? 'web address' : 'program'}</Badge> <code mix={faint}>{c.input.id}</code>
                </label>
                <code mix={mono}>{c.input.transport === 'http' ? c.input.url : (c.input.argv ?? []).join(' ')}</code>
                {c.notes.map((n, i) => (
                  <span key={i} mix={faint}>
                    {n}
                  </span>
                ))}
              </li>
            ))}
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
}
