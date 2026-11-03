// A file a page reads or writes, without typing or selecting its path (A11): "Choose file…" beside a typed file path (the
// window's Open dialog, fs.pickFile, for a kind Drydock reads), "Choose…" beside a "Save to" path (the window's Save dialog,
// fs.pickSave), and, once a file is written, "Show in folder" (File Explorer with the file selected, fs.reveal: only a file the
// core wrote in this session) and "Copy path". The dialog buttons and Show in folder appear only where the window has them (the
// desktop app; the browser in development types the path); Copy path is always there.

import { css, on, type Handle } from 'remix/component';
import { Copy, FileSearch, FolderOpen } from 'lucide';
import type { FolderHints } from '../../api/folderClient.ts';
import { action, query, services } from '../../state/services.tsx';
import { Button } from '../../ui/button.tsx';
import { ErrorNote } from '../../ui/feedback.tsx';
import { t } from '../../ui/tokens.ts';

const rowCss = css({ display: 'flex', flexWrap: 'wrap', alignItems: 'center', gap: t.s2 });

type Hints = Pick<FolderHints, 'canPickFile' | 'canPickSave' | 'canReveal'>;

function useHints(handle: Handle<any>) {
  const s = services(handle);
  return query(handle, {
    load: async (): Promise<Hints> => {
      try {
        return await s.client.fsFolderHints();
      } catch {
        return {};
      }
    },
  });
}

/** "Choose file…": the window's Open dialog for a kind of file Drydock reads. */
export function ChooseFile(handle: Handle<{ kind: 'license' | 'project-archive'; onPick: (path: string) => void; testId?: string; disabled?: boolean }>) {
  const s = services(handle);
  const act = action(handle);
  const hints = useHints(handle);
  return () =>
    hints.data?.canPickFile ? (
      <Button
        type="button"
        variant="secondary"
        size="sm"
        icon={FileSearch}
        data-testid={handle.props.testId ?? 'choose-file'}
        disabled={handle.props.disabled || act.busy !== ''}
        mix={on('click', async () => {
          const path = await act.run('pick', () => s.client.fsPickFile(handle.props.kind));
          if (path) handle.props.onPick(path);
        })}
      >
        {act.busy ? 'Choosing…' : 'Choose file…'}
      </Button>
    ) : null;
}

/** "Choose…" beside a Save to path: the window's Save dialog, opened at the folder of the path in the field. */
export function ChooseSave(handle: Handle<{ kind: 'support-bundle' | 'project-export'; dir: string; onPick: (path: string) => void; testId?: string; disabled?: boolean }>) {
  const s = services(handle);
  const act = action(handle);
  const hints = useHints(handle);
  return () =>
    hints.data?.canPickSave ? (
      <Button
        type="button"
        variant="secondary"
        size="sm"
        icon={FolderOpen}
        data-testid={handle.props.testId ?? 'choose-save'}
        disabled={handle.props.disabled || act.busy !== ''}
        mix={on('click', async () => {
          const path = await act.run('pick', () => s.client.fsPickSave(handle.props.kind, handle.props.dir.trim()));
          if (path) handle.props.onPick(path);
        })}
      >
        {act.busy ? 'Choosing…' : 'Choose…'}
      </Button>
    ) : null;
}

/** Show in folder and Copy path for a file Drydock just wrote. */
export function SavedFile(handle: Handle<{ path: string; testId?: string }>) {
  const s = services(handle);
  const act = action(handle);
  const hints = useHints(handle);
  let copied = false;
  let timer: ReturnType<typeof setTimeout> | undefined;
  handle.signal.addEventListener('abort', () => clearTimeout(timer));
  const copy = async () => {
    try {
      await navigator.clipboard.writeText(handle.props.path);
      copied = true;
      clearTimeout(timer);
      timer = setTimeout(() => ((copied = false), void handle.update()), 2000);
    } catch {
      copied = false; // the clipboard can be refused; the path is on the page to select
    }
    void handle.update();
  };
  return () => (
    <div style={{ display: 'grid', gap: 'var(--space-2)' }} data-testid={handle.props.testId ?? 'saved-file'}>
      <div mix={rowCss}>
        {hints.data?.canReveal ? (
          <Button type="button" size="sm" variant="secondary" icon={FolderOpen} data-testid="saved-show" disabled={act.busy !== ''} mix={on('click', () => void act.run('show', () => s.client.fsReveal(handle.props.path)))}>
            Show in folder
          </Button>
        ) : null}
        <Button type="button" size="sm" variant="ghost" icon={Copy} data-testid="saved-copy" mix={on('click', () => void copy())}>
          {copied ? 'Copied' : 'Copy path'}
        </Button>
      </div>
      <ErrorNote error={act.error} what="Could not show the folder" />
    </div>
  );
}
