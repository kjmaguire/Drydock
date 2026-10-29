// A person's own agent CLI on the Agents page (decision P115): Add a custom CLI (a dialog that starts from Drydock's template
// for the id typed, and sends the manifest to the core, which checks it strictly and keeps it as a file), and on a custom
// CLI's detail where its manifest is kept and Remove. Drydock runs one process of that program per turn, as its manifest
// describes; the CLI's own notice (custom-<id>) is accepted on its detail before an agent can use it.

import { css, on, ref, type Handle } from 'remix/component';
import { Trash2 } from 'lucide';
import type { Connection, CustomCLIs } from '../../api/types.ts';
import { action, services } from '../../state/services.tsx';
import { Button } from '../../ui/button.tsx';
import { muted } from '../../ui/data.tsx';
import { Dialog } from '../../ui/dialog.tsx';
import { ErrorNote } from '../../ui/feedback.tsx';
import { errorAttrs, Field } from '../../ui/form.tsx';
import { SettingRow, SettingsGroup } from '../../ui/master-detail.tsx';
import { t } from '../../ui/tokens.ts';
import { customIdProblem } from './agents.ts';

const formCss = css({
  display: 'grid',
  gap: t.s3,
  minWidth: 'min(720px, 80vw)',
  '& p': { margin: '0' },
  '& textarea': { minHeight: '320px', fontFamily: t.mono, fontSize: t.textXs, width: '100%' },
  '& .row': { display: 'flex', gap: t.s2, alignItems: 'end', flexWrap: 'wrap' },
  '& .row > div': { flex: '1 1 220px' },
});

/** The dialog that adds a person's own CLI. onAdded gets the new Connection. */
export function AddCustomCLI(handle: Handle<{ open: boolean; dir: string; taken: readonly string[]; onClose: () => void; onAdded: (c: Connection) => void }>) {
  const s = services(handle);
  const act = action(handle);
  let id = '';
  let yaml = '';
  let idProblem = '';
  let formEl: HTMLElement | null = null;
  const set = (f: () => void) => {
    f();
    void handle.update();
  };
  const focusInvalid = () => handle.queueTask(() => formEl?.querySelector<HTMLElement>('[aria-invalid="true"]')?.focus());
  const fill = async () => {
    idProblem = customIdProblem(id, handle.props.taken);
    if (idProblem) return set(focusInvalid);
    const text = await act.run('template', () => s.client.connectionCustomTemplate(id.trim()));
    if (text !== undefined) set(() => (yaml = text));
  };
  const add = async () => {
    if (!yaml.trim()) {
      await fill();
      return;
    }
    const c = await act.run('add', () => s.client.connectionCustomAdd(yaml));
    if (c) {
      set(() => ((id = ''), (yaml = '')));
      handle.props.onAdded(c);
    }
  };
  return () => {
    const { open, dir, onClose } = handle.props;
    return (
      <Dialog open={open} title="Add a custom CLI" wide onClose={onClose}>
        <form mix={[formCss, ref((el) => (formEl = el as HTMLElement)), on('submit', (e) => (e.preventDefault(), void add()))]} data-testid="custom-cli-form">
          <p>
            Your own agent CLI, described by a manifest: the program, its arguments, how it takes the prompt and what it prints. Drydock runs one process of it per turn, with your
            account's permissions, and keeps the manifest in <code>{dir || 'the adapters/custom folder'}</code>.
          </p>
          <div class="row">
            <Field label="Id" error={idProblem || undefined}>
              {(fid) => (
                <input
                  id={fid}
                  value={id}
                  autocomplete="off"
                  spellcheck={false}
                  placeholder="my-cli"
                  {...errorAttrs(fid, idProblem || undefined)}
                  mix={on('input', (e) => set(() => ((id = e.currentTarget.value), (idProblem = ''))))}
                />
              )}
            </Field>
            <Button size="sm" disabled={!!act.busy} data-testid="custom-cli-template" mix={on('click', () => void fill())}>
              {act.busy === 'template' ? 'Reading...' : 'Start from the template'}
            </Button>
          </div>
          <Field label="Manifest (YAML)">
            {(fid) => <textarea id={fid} value={yaml} spellcheck={false} data-testid="custom-cli-yaml" mix={on('input', (e) => set(() => (yaml = e.currentTarget.value)))} />}
          </Field>
          <p mix={muted}>Keep keys out of the manifest: pass the variable your CLI reads on by name in Settings. The manifest is checked before it is kept; the reason is shown here.</p>
          <ErrorNote error={act.error} what="Not added" />
          <div class="row">
            <Button type="submit" variant="primary" size="sm" disabled={!!act.busy || !yaml.trim()} data-testid="custom-cli-add">
              {act.busy === 'add' ? 'Adding...' : 'Add the CLI'}
            </Button>
            <Button size="sm" mix={on('click', onClose)}>
              Cancel
            </Button>
          </div>
        </form>
      </Dialog>
    );
  };
}

/** On a custom CLI's detail: where its manifest is, why it did not load, and Remove (refused while an agent runs on it). */
export function CustomCLIPanel(handle: Handle<{ adapter: string; files?: CustomCLIs; readOnly: boolean; onRemoved: () => void }>) {
  const s = services(handle);
  const act = action(handle);
  let confirming = false;
  return () => {
    const { adapter, files, readOnly, onRemoved } = handle.props;
    const f = files?.files.find((x) => x.id === adapter);
    return (
      <SettingsGroup title="Your CLI" testId="custom-cli" label="Your CLI">
        <SettingRow label="Manifest">
          <code>{f?.path ?? `${files?.dir ?? 'adapters/custom'}/${adapter}.yaml`}</code>
        </SettingRow>
        <SettingRow
          label="Remove"
          control={
            confirming ? (
              <span style={{ display: 'inline-flex', gap: '8px' }}>
                <Button
                  size="sm"
                  variant="danger"
                  icon={Trash2}
                  disabled={readOnly || !!act.busy}
                  data-testid="custom-cli-remove-confirm"
                  mix={on('click', async () => {
                    if (await act.run('remove', () => s.client.connectionCustomRemove(adapter))) onRemoved();
                  })}
                >
                  {act.busy ? 'Removing...' : `Remove ${adapter}`}
                </Button>
                <Button size="sm" mix={on('click', () => ((confirming = false), void handle.update()))}>
                  Cancel
                </Button>
              </span>
            ) : (
              <Button size="sm" variant="ghost" icon={Trash2} disabled={readOnly} data-testid="custom-cli-remove" mix={on('click', () => ((confirming = true), void handle.update()))}>
                Remove...
              </Button>
            )
          }
        >
          <span>Deletes the manifest. Agents that run on it are given another CLI or removed first.</span>
        </SettingRow>
        <ErrorNote error={act.error} what="Not removed" />
      </SettingsGroup>
    );
  };
}
