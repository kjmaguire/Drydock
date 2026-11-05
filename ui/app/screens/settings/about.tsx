// About Drydock (decision P129): the build's version, the third-party notices the build ships (THIRD_PARTY_NOTICES.md, read by
// the core from beside the executable and shown here, not opened as a file) and the keyboard map, read-only, from the shortcuts
// that exist (lib/keys.ts KEYMAP), with the keys a person bound them to (decision P132).

import { css, on, type Handle } from 'remix/component';
import { BadgeInfo } from 'lucide';
import type { Licenses } from '../../api/shellClient.ts';
import { KEYMAP, keysOf } from '../../lib/keys.ts';
import { action, services, watch } from '../../state/services.tsx';
import { Button } from '../../ui/button.tsx';
import { kv, muted } from '../../ui/data.tsx';
import { Dialog } from '../../ui/dialog.tsx';
import { ErrorNote, Loading } from '../../ui/feedback.tsx';
import { Markdown } from '../../ui/markdown.tsx';
import { t } from '../../ui/tokens.ts';
import { Group, panel, tableWrap } from './settings-kit.tsx';

const keysCss = css({ display: 'inline-flex', flexWrap: 'wrap', alignItems: 'center', gap: '4px', whiteSpace: 'nowrap', '& .then': { color: t.textMuted, fontSize: t.textSm } });

/** The keys of one line of the map, each a <kbd> ("then" between keys pressed one after the other). */
export function Keys(handle: Handle<{ keys: string[] }>) {
  return () => (
    <span mix={keysCss}>
      {handle.props.keys.map((k, i) =>
        k === 'then' ? (
          <span key={i} class="then">
            then
          </span>
        ) : (
          <kbd key={i}>{k}</kbd>
        ),
      )}
    </span>
  );
}

/** The version line: "0.4.2", with the build id when the release set one; "a build from the source" for a development build. */
export function versionWords(version: string | undefined, build: string | undefined): string {
  const v = (version ?? '').trim();
  if (!v) return 'unknown';
  const words = v === '0.0.0-dev' ? `${v} (a build from the source)` : v;
  return build?.trim() ? `${words}, build ${build.trim()}` : words;
}

/**
 * About Drydock. `keymap: false` leaves the keyboard map out: Settings > Help & about shows the version and the licenses, and
 * the map is under Settings > Shortcuts beside the keys a person changes (decision P140).
 */
export function AboutDrydock(handle: Handle<{ keymap?: boolean }>) {
  const s = services(handle);
  watch(handle, s.workspace);
  watch(handle, s.ui); // the keys in use (decision P132)
  const act = action(handle);
  let open = false;
  let licenses: Licenses | undefined;
  const show = () =>
    void act.run('licenses', async () => {
      open = true;
      licenses = licenses ?? (await s.client.appLicenses());
    });

  return () => {
    const info = s.workspace.info.data;
    return (
      <Group title="About Drydock" icon={BadgeInfo} label="About Drydock" testId="about-section" description={handle.props.keymap === false ? "The version and the licenses of the software inside." : "The version, the licenses of the software inside, and the keyboard shortcuts."}>
        <div mix={panel}>
          <dl mix={kv}>
            <dt>Version</dt>
            <dd data-testid="about-version">{versionWords(info?.version, info?.build)}</dd>
            <dt>Licenses</dt>
            <dd>
              <Button variant="link" data-testid="about-licenses" disabled={act.busy === 'licenses'} mix={on('click', show)}>
                {act.busy === 'licenses' ? 'Reading the notices...' : 'Third-party licenses'}
              </Button>
            </dd>
          </dl>
          <ErrorNote error={act.error} what="The notices could not be read" />
        </div>
        {handle.props.keymap === false ? null : <KeymapTable />}
        <Dialog open={open} wide title="Third-party licenses" onClose={() => ((open = false), void handle.update())}>
          {!licenses ? (
            <Loading />
          ) : licenses.shipped ? (
            <div data-testid="about-licenses-text">
              <Markdown source={licenses.text ?? ''} maxChars={400_000} />
            </div>
          ) : (
            <p data-testid="about-licenses-note">{licenses.note}</p>
          )}
        </Dialog>
      </Group>
    );
  };
}

/** The keyboard map, read-only, with the keys in use (a bound shortcut shows the person's keys). */
export function KeymapTable(handle: Handle) {
  const s = services(handle);
  watch(handle, s.ui); // the keys in use (decision P132)
  return () => (
    <>
      <div mix={tableWrap}>
        <table aria-label="Keyboard shortcuts" data-testid="about-keymap">
          <thead>
            <tr>
              <th scope="col">Keys</th>
              <th scope="col">What they do</th>
              <th scope="col">Where</th>
            </tr>
          </thead>
          <tbody>
            {KEYMAP.map((k) => (
              <tr key={k.keys.join(' ') + k.where}>
                <td>
                  <Keys keys={keysOf(k)} />
                </td>
                <td>{k.action}</td>
                <td>{k.where}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
      <p mix={muted} style={{ margin: '0' }}>
        The keys in use: change them under Keyboard shortcuts. None acts inside the terminal or the diff editor, which keep their own keys.
      </p>
    </>
  );
}
