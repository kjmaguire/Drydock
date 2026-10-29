// "Import from Claude Code" (decision P109): a one-time, read-only import of the chats Claude Code keeps on this PC and in
// its WSL distros. The dialog lists the sessions chat.importList found, every one not imported yet chosen, and chat.import
// makes each chosen one a chat that continues its Claude Code session. A session imported before is shown with a link to its
// chat and cannot be chosen again; one that names no folder cannot be continued and is shown with the reason.

import { css, navigate, on, type Handle } from 'remix/component';
import type { ImportCandidate, ImportList, ImportResult } from '../../api/chatClient.ts';
import { routes } from '../../routes.ts';
import { action, services, watch } from '../../state/services.tsx';
import { Button } from '../../ui/button.tsx';
import { Badge } from '../../ui/data.tsx';
import { Dialog } from '../../ui/dialog.tsx';
import { ErrorNote, Loading, Notice } from '../../ui/feedback.tsx';
import { formActions } from '../../ui/form.tsx';
import { t } from '../../ui/tokens.ts';
import { defaultSelection, importable, importButton, importFolders, importKey, importLine, importSummary } from './chat-import.ts';

const listCss = css({
  display: 'grid',
  gridTemplateColumns: 'minmax(0, 1fr)',
  gap: '2px',
  maxHeight: 'min(420px, 50vh)',
  overflowY: 'auto',
  margin: '0',
  padding: '0',
  listStyle: 'none',
  border: `1px solid ${t.border}`,
  borderRadius: t.rLg,
});
const rowCss = css({
  '& > label': { display: 'flex', alignItems: 'flex-start', gap: t.s2, padding: '8px 10px', borderRadius: t.rMd, cursor: 'pointer' },
  '& > label:hover': { background: t.bgHover },
  '& input': { marginTop: '2px', flex: 'none' },
  '& .text': { display: 'grid', gap: '2px', minWidth: '0' },
  '& .title': { display: 'flex', alignItems: 'center', gap: '6px', minWidth: '0', fontSize: t.textMd, fontWeight: '500' },
  '& .title > span': { overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' },
  '& .line, & .problem': { color: t.textFaint, fontSize: t.textXs, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' },
  '& .problem': { color: t.warning, whiteSpace: 'normal' },
  '&[data-disabled] > label': { cursor: 'default' },
});
const barCss = css({ display: 'flex', alignItems: 'center', gap: t.s2, fontSize: t.textSm, color: t.textMuted, '& .count': { marginLeft: 'auto' } });
const skippedCss = css({ margin: '0', paddingLeft: t.s4, fontSize: t.textSm, color: t.textMuted, display: 'grid', gap: '2px' });

export function ImportClaudeDialog(handle: Handle<{ open: boolean; onClose: () => void; onNavigate?: (href: string) => void }>) {
  const s = services(handle);
  watch(handle, s.workspace);
  const load = action(handle);
  const act = action(handle);
  let list: ImportList | undefined;
  let chosen = new Set<string>();
  let result: ImportResult | undefined;
  let wasOpen = false;
  let reads = 0; // a read answered after a newer one started is dropped

  const read = async (fresh: boolean) => {
    const n = ++reads;
    const got = await load.run('list', () => s.client.chatImportList());
    if (n !== reads || !got) return;
    list = got;
    if (fresh) chosen = defaultSelection(got);
    else chosen = new Set([...chosen].filter((k) => got.sessions.some((c) => importKey(c) === k && importable(c))));
    void handle.update();
  };

  const toggle = (c: ImportCandidate, on: boolean) => {
    const k = importKey(c);
    if (on) chosen.add(k);
    else chosen.delete(k);
    void handle.update();
  };

  const runImport = async () => {
    if (!list || chosen.size === 0 || act.busy) return;
    const refs = list.sessions.filter((c) => chosen.has(importKey(c))).map((c) => ({ sessionId: c.sessionId, wslDistro: c.wslDistro }));
    const r = await act.run('import', () => s.client.chatImport(refs));
    if (r) {
      result = r;
      chosen = new Set();
      void read(false); // the imported ones now show their chat
    }
  };

  const go = (href: string) => {
    handle.props.onClose();
    if (handle.props.onNavigate) handle.props.onNavigate(href);
    else void navigate(href).catch(() => {});
  };

  return () => {
    const { open, onClose } = handle.props;
    if (open && !wasOpen) {
      // each opening reads Claude Code's sessions again and starts without the last result
      list = undefined;
      result = undefined;
      handle.queueTask(() => {
        load.clear();
        act.clear();
        void read(true);
      });
    }
    wasOpen = open;
    const sessions = list?.sessions ?? [];
    const choosable = sessions.filter(importable);
    const projectName = (id: string) => s.workspace.projectList.find((p) => p.id === id)?.name ?? '';
    const broken = (list?.sources ?? []).filter((x) => x.error);
    return (
      <Dialog open={open} onClose={onClose} title="Import from Claude Code" wide>
        <div style={{ display: 'grid', gap: '14px' }} data-testid="chat-import">
          <p style={{ margin: '0' }}>
            Copies the chats Claude Code keeps on this PC into Chats. Each one continues its Claude Code session in the folder it ran in. Claude Code's files are only read, and its sign-in is never opened.
          </p>
          <ErrorNote error={load.error} what="Could not read Claude Code's chats" />
          {broken.map((x) => (
            <Notice key={`${x.wslDistro}:${x.folder}`} tone="warn">
              {x.wslDistro ? `WSL: ${x.wslDistro}. ` : ''}
              {x.error}
            </Notice>
          ))}
          {result ? (
            <Notice tone={result.imported.length ? 'ok' : 'info'} role="status" testId="chat-import-result">
              {importSummary(result)}
              {result.skipped.some((x) => !x.chatId) ? (
                <ul mix={skippedCss}>
                  {result.skipped
                    .filter((x) => !x.chatId)
                    .map((x) => (
                      <li key={importKey(x)}>
                        {sessions.find((c) => importKey(c) === importKey(x))?.title || x.sessionId}: {x.reason}
                      </li>
                    ))}
                </ul>
              ) : null}
            </Notice>
          ) : null}
          {!list && load.busy ? <Loading label="Looking for Claude Code chats..." /> : null}
          {list && sessions.length === 0 ? (
            <p style={{ margin: '0', color: 'var(--text-muted)' }} data-testid="chat-import-none">
              No Claude Code chats were found{importFolders(list) ? ` in ${importFolders(list)}` : ''}.
            </p>
          ) : null}
          {sessions.length ? (
            <>
              <div mix={barCss}>
                <Button size="sm" variant="ghost" disabled={!choosable.length} mix={[on('click', () => ((chosen = new Set(choosable.map(importKey))), void handle.update()))]}>
                  Select all
                </Button>
                <Button size="sm" variant="ghost" disabled={!chosen.size} mix={[on('click', () => ((chosen = new Set()), void handle.update()))]}>
                  Select none
                </Button>
                <span class="count" aria-live="polite" data-testid="chat-import-count">
                  {chosen.size} of {choosable.length} chosen
                </span>
              </div>
              <ul mix={listCss} aria-label="Claude Code chats">
                {sessions.map((c) => {
                  const k = importKey(c);
                  const can = importable(c);
                  return (
                    <li key={k} mix={rowCss} data-testid="chat-import-row" data-session={c.sessionId} data-disabled={can ? undefined : 'true'}>
                      <label>
                        <input type="checkbox" checked={can && chosen.has(k)} disabled={!can || !!act.busy} aria-label={c.title || c.sessionId} mix={on('change', (e) => toggle(c, e.currentTarget.checked))} />
                        <span class="text">
                          <span class="title">
                            <span>{c.title || 'Untitled chat'}</span>
                            {c.chatId ? <Badge>imported</Badge> : null}
                          </span>
                          <span class="line" title={c.cwd}>
                            {importLine(c, c.projectId ? projectName(c.projectId) : '')}
                          </span>
                          {c.problem ? <span class="problem">{c.problem}</span> : null}
                        </span>
                      </label>
                      {c.chatId ? (
                        <Button size="sm" variant="link" mix={[on('click', () => go(routes.chat.href({ id: c.chatId! })))]} data-testid="chat-import-open">
                          Open the chat
                        </Button>
                      ) : null}
                    </li>
                  );
                })}
              </ul>
            </>
          ) : null}
          <ErrorNote error={act.error} what="Could not import" />
          <div mix={formActions} style={{ justifyContent: 'flex-end' }}>
            <Button variant="ghost" mix={[on('click', () => onClose())]}>
              {result ? 'Done' : 'Cancel'}
            </Button>
            <Button variant="primary" disabled={!chosen.size || !!act.busy || !!load.busy} mix={[on('click', () => void runImport())]} data-testid="chat-import-go">
              {act.busy ? 'Importing...' : importButton(chosen.size)}
            </Button>
          </div>
        </div>
      </Dialog>
    );
  };
}
