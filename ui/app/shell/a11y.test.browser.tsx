// Accessibility and keyboard behaviour of the shell and the kit (audit F7, F20, F33).
import { expect } from 'remix/assert';
import { afterEach, describe, it } from 'remix/test';
import { lic } from '../test/fixtures.ts';
import { cleanup, mount, renderPlain, testServices, waitFor } from '../test/render.tsx';
import { adapterInfo, CATALOG, prebuilt } from '../screens/project/wizard-fixtures.ts';
import { ownsKeys } from '../lib/keys.ts';
import { Dialog } from '../ui/dialog.tsx';
import { Loading } from '../ui/feedback.tsx';
import { Tabs } from '../ui/tabs.tsx';
import { Details } from './details.tsx';
import { Dock } from './dock.tsx';
import { Shell } from './shell.tsx';

afterEach(cleanup);

const press = (target: EventTarget, key: string, mods: KeyboardEventInit = {}) => {
  const e = new KeyboardEvent('keydown', { key, bubbles: true, cancelable: true, ...mods });
  target.dispatchEvent(e);
  return e;
};

function shellServices() {
  const s = testServices({
    projectList: async () => [],
    licenseStatus: async () => lic(),
    appInfo: async () => ({}) as never,
    githubStatus: async () => ({ installed: false, loggedIn: false, login: '', error: 'gh is not installed' }),
    agentCatalogList: async () => CATALOG.map((e) => prebuilt(e.id, { codex: false })),
    adapterList: async () => [adapterInfo('claude', true, ['opus'])],
    agentTemplateList: async () => [],
    mcpLibList: async () => [],
    mcpCatalog: async () => [],
    chatList: async () => ({ chats: [], hasMore: false }) as never,
  });
  s.workspace.start();
  return s;
}

describe('global shortcuts (F20)', () => {
  it('Ctrl+J and Ctrl+K work on the page but not inside the terminal or the diff editor', async () => {
    const s = shellServices();
    const r = mount(<Shell url={new URL('http://localhost/settings')}>{null}</Shell>, s);
    // a widget host that owns its keys (the terminal and the diff editor carry data-own-keys), with xterm's own textarea
    const host = document.createElement('div');
    host.setAttribute('data-own-keys', '');
    const inner = document.createElement('textarea');
    host.append(inner);
    r.container.append(host);

    const j = press(inner, 'j', { ctrlKey: true });
    const k = press(inner, 'k', { ctrlKey: true });
    const d = press(inner, 'D', { ctrlKey: true, shiftKey: true });
    r.root.flush();
    expect([j.defaultPrevented, k.defaultPrevented, d.defaultPrevented]).toEqual([false, false, false]);
    expect(s.ui.dock.open).toBe(false);
    expect(s.ui.paletteOpen).toBe(false);

    // elsewhere they act
    const j2 = press(document.body, 'j', { ctrlKey: true });
    expect(j2.defaultPrevented).toBe(true);
    expect(s.ui.dock.open).toBe(true);
    const k2 = press(document.body, 'k', { ctrlKey: true });
    expect(k2.defaultPrevented).toBe(true);
    expect(s.ui.paletteOpen).toBe(true);
    s.ui.setPalette(false);
    host.remove();
  });

  it('recognises the widgets by their own roots too', () => {
    const term = document.createElement('div');
    term.className = 'xterm';
    const ta = document.createElement('textarea');
    term.append(ta);
    const monaco = document.createElement('div');
    monaco.className = 'monaco-editor';
    const span = document.createElement('span');
    monaco.append(span);
    expect(ownsKeys(ta)).toBe(true);
    expect(ownsKeys(span)).toBe(true);
    expect(ownsKeys(document.body)).toBe(false);
    expect(ownsKeys(window)).toBe(false);
    expect(ownsKeys(null)).toBe(false);
  });
});

describe('the skip link', () => {
  it('sits in a landmark of its own and targets main', async () => {
    const r = mount(<Shell url={new URL('http://localhost/settings')}>{null}</Shell>, shellServices());
    const link = await waitFor(() => r.$('a[href="#main"]')!, r);
    expect(link.closest('nav')!.getAttribute('aria-label')).toBe('Skip links');
    expect(r.$('main#main')).not.toBeNull();
  });
});

describe('the details panel', () => {
  it('shows why the task could not be read instead of loading for ever (F20)', async () => {
    const s = testServices({
      taskGet: async () => {
        throw new Error('task store is locked');
      },
    });
    s.ui.setDetails(true);
    s.ui.setTab('task');
    const r = mount(<Details taskId="t1" />, s);
    await waitFor(() => expect(r.$('[role="alert"]')!.textContent).toContain('task store is locked'), r);
    expect(r.container.textContent).not.toContain('Loading...');
  });

  it('its tabs name no panel that is not in the page', async () => {
    const s = testServices({});
    s.ui.setDetails(true);
    const r = mount(<Details taskId="t1" />, s);
    const tabs = await waitFor(() => {
      const t = Array.from(r.$$('[role="tab"]'));
      if (t.length < 2) throw new Error('tabs not rendered');
      return t;
    }, r);
    for (const tab of tabs) {
      const id = tab.getAttribute('aria-controls');
      if (id) expect(document.getElementById(id)).not.toBeNull();
    }
  });
});

describe('the kit', () => {
  it('Tabs with panels point each tab at a rendered (hidden when inactive) panel; without panels they name none', () => {
    const items = [
      { name: 'a', label: 'A' },
      { name: 'b', label: 'B' },
    ];
    const withPanels = renderPlain(
      <Tabs label="With" items={items} active="a" onChange={() => {}}>
        {(n) => <p>panel {n}</p>}
      </Tabs>,
    );
    const tabs = Array.from(withPanels.container.querySelectorAll('[role="tab"]'));
    for (const tab of tabs) {
      const panel = document.getElementById(tab.getAttribute('aria-controls')!);
      expect(panel).not.toBeNull();
      expect(panel!.getAttribute('role')).toBe('tabpanel');
    }
    expect(document.getElementById(tabs[1]!.getAttribute('aria-controls')!)!.hidden).toBe(true);

    const bare = renderPlain(<Tabs label="Bare" items={items} active="a" onChange={() => {}} />);
    for (const tab of Array.from(bare.container.querySelectorAll('[role="tab"]'))) expect(tab.hasAttribute('aria-controls')).toBe(false);
  });

  it('a Dialog is named by its heading, or by its label when one is given', async () => {
    const r = renderPlain(
      <div>
        <Dialog open title="Rename chat" onClose={() => {}}>
          <p>body</p>
        </Dialog>
      </div>,
    );
    const dlg = await waitFor(() => {
      const d = r.container.querySelector('dialog')!;
      if (!d.open) throw new Error('not open yet');
      return d;
    }, r);
    const heading = document.getElementById(dlg.getAttribute('aria-labelledby')!)!;
    expect(heading.tagName).toBe('H2');
    expect(heading.textContent).toBe('Rename chat');
    dlg.close();
    const named = renderPlain(
      <Dialog open title="New chat" label="Start a chat" onClose={() => {}}>
        <p>body</p>
      </Dialog>,
    );
    const d2 = named.container.querySelector('dialog')!;
    expect(d2.getAttribute('aria-label')).toBe('Start a chat');
    expect(d2.hasAttribute('aria-labelledby')).toBe(false);
    d2.close();
  });

  it('Loading is a status message', () => {
    const r = renderPlain(<Loading label="Loading messages..." />);
    expect(r.container.querySelector('[role="status"]')!.textContent).toContain('Loading messages...');
  });
});

describe('the dock', () => {
  it('its resize handle reports its range and value, and its tabs name the panel only while it is shown', async () => {
    const s = testServices({});
    const r = mount(<Dock projectId="p1" taskId={undefined} />, s);
    await waitFor(() => r.$('[role="tablist"][aria-label="Dock"]')!, r);
    for (const tab of Array.from(r.$$('[role="tab"]'))) expect(tab.hasAttribute('aria-controls')).toBe(false);
    await r.act(() => s.ui.setDock({ open: true, tab: 'logs', height: 300 }));
    const sep = r.$('[role="separator"]')!;
    expect(sep.getAttribute('aria-valuemin')).toBe('120');
    expect(Number(sep.getAttribute('aria-valuemax'))).toBeGreaterThan(119);
    expect(sep.getAttribute('aria-valuenow')).toBe('300');
    expect(r.$('#dock-panel')).not.toBeNull();
    for (const tab of Array.from(r.$$('[role="tab"]'))) expect(tab.getAttribute('aria-controls')).toBe('dock-panel');
    await r.act(() => press(sep, 'ArrowUp'));
    expect(s.ui.dock.height).toBe(Math.min(340, Number(sep.getAttribute('aria-valuemax'))));
  });
});
