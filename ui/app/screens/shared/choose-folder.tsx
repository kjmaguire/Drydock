// "Choose folder..." next to a typed folder path (decision P58's folder dialog, used by the chat and Security scan pages): it
// opens the window's own folder dialog through the core (`fs.pickFolder`) and hands the chosen folder to the field. It shows
// only where the window has a dialog (the desktop app; not the browser in development, where the path is typed). Cancelling
// changes nothing.

import { css, on, type Handle } from 'remix/component';
import { FolderOpen } from 'lucide';
import type { FolderHints } from '../../api/folderClient.ts';
import { action, query, services } from '../../state/services.tsx';
import { Button, IconButton } from '../../ui/button.tsx';

/** A row holding the path input and the button: the input takes the room. */
export const folderRow = css({ display: 'flex', gap: '8px', alignItems: 'center', minWidth: '0', '& input': { flex: '1', minWidth: '0' }, '& button': { flex: 'none' } });

const noHints: FolderHints = { canPick: false, windows: false, devDrives: [] };

/** `compact` draws it as an icon button named "Choose folder…" (inside a path field, as the Security scan's header has it). */
export function ChooseFolder(handle: Handle<{ title: string; onPick: (path: string) => void; disabled?: boolean; testId?: string; compact?: boolean }>) {
  const s = services(handle);
  const act = action(handle);
  const hints = query(handle, {
    load: async () => {
      try {
        return await s.client.fsFolderHints();
      } catch {
        return noHints;
      }
    },
  });
  const pick = async () => {
    const path = await act.run('pick', () => s.client.fsPickFolder(handle.props.title));
    if (path) handle.props.onPick(path);
    void handle.update();
  };
  return () => {
    if (!hints.data?.canPick) return null;
    if (handle.props.compact) {
      return (
        <IconButton
          icon={FolderOpen}
          label={act.busy ? 'Choosing…' : 'Choose folder…'}
          data-testid={handle.props.testId ?? 'choose-folder'}
          disabled={handle.props.disabled || act.busy !== ''}
          mix={on('click', () => void pick())}
        />
      );
    }
    return (
      <Button
        type="button"
        variant="secondary"
        size="sm"
        icon={FolderOpen}
        data-testid={handle.props.testId ?? 'choose-folder'}
        disabled={handle.props.disabled || act.busy !== ''}
        mix={on('click', () => void pick())}
      >
        {act.busy ? 'Choosing…' : 'Choose folder…'}
      </Button>
    );
  };
}
