// The project folder's field (decision P58), used by the new-project wizard and by Project settings (attach a repository):
// type the path or press "Choose folder..." for the window's own folder dialog, see what is there (a repository, a folder
// that is not one, a folder that does not exist yet, one inside another repository), and, when there is no repository yet,
// "Create a Git repository here" so Drydock makes one (the folder if needed, branch main, a first commit): for a folder that does
// not exist yet or is empty the box comes ticked (nothing of the person's is there), for a folder with files in it the choice is
// a button in the status line (B6). The dialog opens at the folder in the field (B5). On Windows the hint recommends a Dev Drive
// while the path is not on one.

import { css, on, type Handle } from 'remix/component';
import { FolderOpen } from 'lucide';
import type { FolderHints, RepoFolder } from '../../api/folderClient.ts';
import { action, query, services } from '../../state/services.tsx';
import { Button } from '../../ui/button.tsx';
import { ErrorNote } from '../../ui/feedback.tsx';
import { Checkbox, Field } from '../../ui/form.tsx';
import { t } from '../../ui/tokens.ts';
import { autoInit, currentCheck, folderHint } from './wizard.ts';

const rowCss = css({ display: 'flex', gap: t.s2, alignItems: 'center', minWidth: '0', '& input': { flex: '1', minWidth: '0' }, '& button': { flex: 'none' } });
const checkCss = css({
  margin: '0',
  fontSize: t.textSm,
  color: t.textMuted,
  overflowWrap: 'anywhere',
  '&[data-state="repository"]': { color: t.success },
  '&[data-state="inside"], &[data-state="no_branch"], &[data-state="not_folder"], &[data-state="invalid"]': { color: t.danger },
});

const noHints: FolderHints = { canPick: false, windows: false, devDrives: [] };

export function FolderField(
  handle: Handle<{
    label?: string;
    value: string;
    onChange: (path: string) => void;
    check: RepoFolder | undefined;
    onCheck: (c: RepoFolder | undefined) => void;
    initRepo: boolean;
    onInitRepo: (v: boolean) => void;
  }>,
) {
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
  let checked = '';
  let timer: ReturnType<typeof setTimeout> | undefined;
  handle.signal.addEventListener('abort', () => clearTimeout(timer));

  // what is at the folder, asked a moment after the typing stops (and at once after the dialog)
  const schedule = (path: string, wait: number) => {
    clearTimeout(timer);
    if (!path.trim()) {
      handle.props.onCheck(undefined);
      return;
    }
    timer = setTimeout(() => {
      void Promise.resolve()
        .then(() => s.client.repoCheck(path))
        .then(
        (c) => {
          if (!handle.signal.aborted && c.path === handle.props.value.trim().replace(/^"+|"+$/g, '').trim()) {
            handle.props.onCheck(c);
            // a new folder's answer sets the choice afresh: ticked where nothing of the person's is, else left to them
            handle.props.onInitRepo(autoInit(c));
          }
        },
        () => undefined,
      );
    }, wait);
  };

  const pick = async () => {
    const path = await act.run('pick', () => s.client.fsPickFolder("Choose the project's folder", handle.props.value.trim()));
    if (path) {
      checked = path;
      handle.props.onChange(path);
      schedule(path, 0);
    }
  };

  return () => {
    const { label = 'Repository path', value, onChange, initRepo, onInitRepo } = handle.props;
    if (value !== checked) {
      checked = value;
      schedule(value, 300);
    }
    const h = hints.data;
    const check = currentCheck({ localPath: value, localCheck: handle.props.check });
    return (
      <div style={{ display: 'grid', gap: 'var(--space-2)', minWidth: '0' }} data-testid="folder-field">
        <Field label={label} hint={<span data-testid="folder-hint" title={h?.devDriveReason ? `No Dev Drive was found: ${h.devDriveReason}.` : undefined}>{folderHint(h, value)}</span>}>
          {(id) => (
            <div mix={rowCss}>
              <input
                id={id}
                value={value}
                placeholder={h?.canPick ? 'or type the full path, for example D:\\src\\my-app' : 'D:\\src\\my-app'}
                spellcheck={false}
                autocomplete="off"
                mix={on('input', (e) => onChange(e.currentTarget.value))}
              />
              {h?.canPick ? (
                <Button type="button" variant="secondary" size="sm" icon={FolderOpen} data-testid="folder-choose" disabled={!!act.busy} mix={on('click', () => void pick())}>
                  {act.busy ? 'Choosing…' : 'Choose folder…'}
                </Button>
              ) : null}
            </div>
          )}
        </Field>
        <ErrorNote error={act.error} what="The folder dialog did not open" />
        {check ? (
          <p mix={checkCss} role="status" data-testid="folder-check" data-state={check.state}>
            {check.message}
            {check.canInit && !initRepo && !autoInit(check) ? (
              <>
                {' '}
                <Button type="button" variant="secondary" size="sm" data-testid="folder-init-button" mix={on('click', () => onInitRepo(true))}>
                  Create a Git repository here
                </Button>
              </>
            ) : null}
          </p>
        ) : null}
        {check?.canInit && (initRepo || autoInit(check)) ? (
          <div data-testid="folder-init">
            <Checkbox
              label="Create a Git repository here"
              description={`${check.state === 'missing' ? 'Drydock makes the folder, ' : 'Drydock '}runs git init with the branch main and makes a first commit, so the agents' worktrees can start from it.`}
              checked={initRepo}
              onChange={onInitRepo}
            />
          </div>
        ) : null}
      </div>
    );
  };
}
