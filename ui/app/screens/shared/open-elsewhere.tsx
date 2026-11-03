// "More ways to open" a task's worktree, a project's repository or a file in either (effort review X26): Reveal in Explorer, Open
// in an editor found on this computer, and Copy path. The page names only the task or the project and a path relative to its
// folder; the core resolves and confines it and starts only Explorer or an editor of its allowlist (shell.openPath). A window that
// cannot start another program (the browser harness) offers Copy path alone.

import type { Handle } from 'remix/component';
import { ClipboardCopy, Code, FolderOpen } from 'lucide';
import type { EditorList, OpenPathReq } from '../../api/shellClient.ts';
import { services } from '../../state/services.tsx';
import { MoreMenu, type MenuItem } from '../../ui/menu.tsx';

/** shell.editors is asked once per client: the editors on a computer do not change while Drydock runs. */
const editorsOf = new WeakMap<object, Promise<EditorList>>();

/** A path inside a folder as the folder writes paths ("C:\w\a\b.go" for "a/b.go" in "C:\w"). */
export function joinPath(dir: string, rel: string): string {
  if (!rel) return dir;
  const sep = dir.includes('\\') ? '\\' : '/';
  const tail = sep === '\\' ? rel.replaceAll('/', '\\') : rel;
  return dir.endsWith(sep) ? dir + tail : dir + sep + tail;
}

export interface OpenWhere {
  taskId?: string;
  projectId?: string;
  /** A file or folder inside it, relative ("internal/calc.go"); none: the folder itself. */
  path?: string;
}

export function OpenElsewhere(handle: Handle<{ where: OpenWhere; /** The absolute path Copy path copies; none: no Copy path. */ fullPath?: string; label?: string; testId?: string }>) {
  const s = services(handle);
  let list: EditorList = { available: false, editors: [] };
  let p = editorsOf.get(s.client);
  if (!p) {
    // a core that cannot say is one that cannot open anything: Copy path stays
    p = Promise.resolve()
      .then(() => s.client.shellEditors())
      .catch((): EditorList => ({ available: false, editors: [] }));
    editorsOf.set(s.client, p);
  }
  void p.then((v) => {
    if (handle.signal.aborted) return;
    list = v;
    void handle.update();
  });

  const open = async (req: OpenPathReq) => {
    try {
      await s.client.shellOpenPath(req);
    } catch (e) {
      s.ui.toast({ text: 'Could not open it', detail: e instanceof Error ? e.message : String(e), tone: 'warn' });
    }
  };
  const copy = async (text: string) => {
    try {
      await navigator.clipboard.writeText(text);
      s.ui.toast({ text: 'Path copied', tone: 'ok' });
    } catch {
      s.ui.toast({ text: 'The clipboard refused the path', tone: 'warn' });
    }
  };

  return () => {
    const { where, fullPath, label = 'More ways to open', testId } = handle.props;
    const items: MenuItem[] = [];
    if (list.available) {
      items.push({ name: 'explorer', label: where.path ? 'Reveal in Explorer' : 'Open in Explorer', icon: FolderOpen });
      for (const e of list.editors) items.push({ name: `editor:${e.id}`, label: `Open in ${e.name}`, icon: Code });
    }
    if (fullPath) items.push({ name: 'copy', label: 'Copy path', icon: ClipboardCopy, separatorBefore: items.length > 0 });
    if (!items.length) return null;
    return (
      <MoreMenu
        label={label}
        testId={testId}
        items={items}
        onSelect={(name) => {
          const base = { ...(where.taskId ? { taskId: where.taskId } : {}), ...(where.projectId ? { projectId: where.projectId } : {}), ...(where.path ? { path: where.path } : {}) };
          if (name === 'explorer') void open({ ...base, with: 'explorer' });
          else if (name.startsWith('editor:')) void open({ ...base, with: 'editor', editor: name.slice('editor:'.length) });
          else if (name === 'copy' && fullPath) void copy(fullPath);
        }}
      />
    );
  };
}
