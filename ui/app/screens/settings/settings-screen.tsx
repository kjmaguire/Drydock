// Settings and about: the theme, what this session is connected to, what agents inherit, telemetry, how updates work, and
// the features that are not built yet. A workspace screen: it works with no project.

import { css, on, type Handle } from 'remix/component';
import { Check, Construction, Info, Palette, RefreshCw, Settings } from 'lucide';
import type { ThemeChoice } from '../../lib/theme.ts';
import { routes } from '../../routes.ts';
import { services, watch } from '../../state/services.tsx';
import { kv, muted } from '../../ui/data.tsx';
import { Icon } from '../../ui/icon.tsx';
import { Page } from '../../ui/layout.tsx';
import { t } from '../../ui/tokens.ts';
import { AgentEnvSection } from './agent-env.tsx';
import { NOT_BUILT, THEMES } from './settings.ts';
import { Group, panel, settingsColumn, tableWrap } from './settings-kit.tsx';
import { TelemetryPanel } from './telemetry.tsx';

// --- theme picker: three cards, each with a small picture of the app in that theme ---

const pickerCss = css({
  display: 'grid',
  gridTemplateColumns: 'repeat(3, minmax(0, 1fr))',
  gap: t.s3,
  margin: '0',
  padding: '0',
  border: '0',
  '@container (max-width: 520px)': { gridTemplateColumns: 'minmax(0, 1fr)' },
});

const optionCss = css({
  position: 'relative',
  display: 'grid',
  gap: t.s2,
  padding: t.s2,
  border: `1px solid ${t.border}`,
  borderRadius: t.rLg,
  background: t.bgSurface,
  cursor: 'pointer',
  transition: `border-color ${t.fast} ${t.ease}, box-shadow ${t.fast} ${t.ease}`,
  '&:hover': { borderColor: t.borderStrong },
  '&:has(input:checked)': { borderColor: t.accent, boxShadow: `0 0 0 1px ${t.accent}` },
  '&:has(input:focus-visible)': { boxShadow: t.focus },
  '& > input': { position: 'absolute', opacity: '0', pointerEvents: 'none', width: '1px', height: '1px' },
  '& .name': { display: 'flex', alignItems: 'center', justifyContent: 'space-between', gap: t.s2, padding: `0 ${t.s1}`, fontSize: t.textMd, fontWeight: '500' },
  '& .hint': { padding: `0 ${t.s1}`, marginTop: '-6px', fontSize: t.textSm, color: t.textMuted },
  '& .tick': { display: 'none', color: t.accentText },
  '&:has(input:checked) .tick': { display: 'inline-flex' },
});

const swatchCss = css({
  display: 'grid',
  height: '78px',
  borderRadius: t.rMd,
  overflow: 'hidden',
  border: `1px solid ${t.border}`,
});
const splitCss = css({ gridTemplateColumns: '1fr 1fr' });
const darkScheme = css({ colorScheme: 'dark' });
const lightScheme = css({ colorScheme: 'light' });

// A miniature window drawn with the theme tokens; color-scheme on the element picks the light or dark value of each token.
const miniCss = css({
  display: 'grid',
  gridTemplateColumns: '22% 1fr',
  minWidth: '0',
  background: t.bgApp,
  '& .side': { display: 'grid', alignContent: 'start', gap: '4px', padding: '8px 5px', background: t.bgSidebar, borderRight: `1px solid ${t.border}` },
  '& .side i': { display: 'block', height: '4px', borderRadius: '2px', background: t.borderStrong },
  '& .side i:first-child': { background: t.accent },
  '& .main': { display: 'grid', alignContent: 'start', gap: '5px', padding: '8px' },
  '& .main b': { display: 'block', height: '5px', width: '55%', borderRadius: '2px', background: t.text, opacity: '0.85' },
  '& .main i': { display: 'block', height: '14px', borderRadius: '3px', background: t.bgSurface, border: `1px solid ${t.border}` },
  '& .main s': { display: 'block', height: '4px', width: '80%', borderRadius: '2px', background: t.textFaint, textDecoration: 'none' },
});

function Mini(handle: Handle<{ scheme: 'light' | 'dark' }>) {
  return () => (
    <span mix={[miniCss, handle.props.scheme === 'dark' ? darkScheme : lightScheme]} aria-hidden="true">
      <span class="side">
        <i />
        <i />
        <i />
      </span>
      <span class="main">
        <b />
        <i />
        <s />
        <s />
      </span>
    </span>
  );
}

function ThemePicker(handle: Handle) {
  const s = services(handle);
  watch(handle, s.ui);
  return () => (
    <fieldset mix={pickerCss} role="radiogroup" aria-label="Theme">
      <legend class="sr-only">Theme</legend>
      {THEMES.map((th) => (
        <label key={th.value} mix={optionCss} data-testid={`theme-${th.value}`}>
          <input type="radio" name="theme" value={th.value} checked={s.ui.theme === th.value} mix={on('change', () => s.ui.setTheme(th.value as ThemeChoice))} />
          <span mix={[swatchCss, th.value === 'system' ? splitCss : undefined]}>
            {th.value === 'system' ? (
              <>
                <Mini scheme="light" />
                <Mini scheme="dark" />
              </>
            ) : (
              <Mini scheme={th.value === 'dark' ? 'dark' : 'light'} />
            )}
          </span>
          <span class="name">
            {th.label}
            <span class="tick">
              <Icon icon={Check} size={14} />
            </span>
          </span>
          <span class="hint">{th.hint}</span>
        </label>
      ))}
    </fieldset>
  );
}

// --- about this session ---

function AboutSession(handle: Handle) {
  const s = services(handle);
  watch(handle, s.workspace);
  const off = s.store.subscribe(() => void handle.update());
  handle.signal.addEventListener('abort', off);
  return () => {
    const snap = s.store.getSnapshot();
    const info = s.workspace.info.data;
    return (
      <div mix={panel}>
        <dl mix={kv}>
          <dt>Transport</dt>
          <dd>{s.transportName}</dd>
          <dt>Event stream</dt>
          <dd data-testid="stream-status">
            {snap.status}, last seq {snap.lastSeq}
          </dd>
          <dt>You are</dt>
          <dd>{info?.human ?? 'unknown'}</dd>
          <dt>Data location</dt>
          <dd>
            <code>{info?.dataDir ?? 'unknown'}</code>
          </dd>
        </dl>
      </div>
    );
  };
}

// --- updates: an honest placeholder ---

/** There is nothing to click. The update flow is configured when the app is built and has never run on Windows. */
export function UpdatesSection(_handle: Handle) {
  return () => (
    <Group
      title="Updates"
      icon={RefreshCw}
      label="Updates"
      testId="updates-section"
      description="How Drydock is updated, and what has not been tried yet."
    >
      <div mix={panel}>
        <p style={{ margin: '0' }}>
          <strong>Updates are configured when Drydock is built, not in this screen.</strong> This page cannot check for, download or install an update, and there is no
          setting here to change that.
        </p>
        <p mix={muted} style={{ margin: '0' }}>
          The planned flow uses the Wails updater plus Drydock&apos;s own policy: a backup before every update, and a rollback if the new version does not start. That flow
          has <strong>not been run on Windows</strong>, so treat any update as untested until it has. Your license decides which update channels you may use; see the{' '}
          <a href={routes.license.href()}>License</a> page. You can make a backup yourself at any time on the <a href={routes.backups.href()}>Backups</a> page.
        </p>
      </div>
    </Group>
  );
}

// --- not built yet ---

function NotBuilt(_handle: Handle) {
  return () => (
    <Group
      title="Not built yet"
      icon={Construction}
      label="Not built yet"
      stacked
      description="These features are not implemented. They are listed here instead of shown as disabled screens or sample data."
    >
      <div mix={tableWrap}>
        <table aria-label="Not built yet">
          <thead>
            <tr>
              <th scope="col">Feature</th>
              <th scope="col">Slice</th>
              <th scope="col">Meanwhile</th>
            </tr>
          </thead>
          <tbody>
            {NOT_BUILT.map((r) => (
              <tr key={r.feature}>
                <td>{r.feature}</td>
                <td style={{ whiteSpace: 'nowrap' }}>{r.slice}</td>
                <td>{r.meanwhile}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </Group>
  );
}

export function SettingsScreen(_handle: Handle) {
  return () => (
    <Page title="Settings and about" icon={Settings} subtitle="This computer">
      <div mix={settingsColumn}>
        <Group title="Appearance" icon={Palette} label="Appearance" description="Choose how Drydock looks. System follows the Windows light or dark setting.">
          <ThemePicker />
        </Group>
        <Group title="About this session" icon={Info} label="About this session" description="What this window is connected to and where Drydock keeps its data.">
          <AboutSession />
        </Group>
        <AgentEnvSection />
        <TelemetryPanel />
        <UpdatesSection />
        <NotBuilt />
      </div>
    </Page>
  );
}

