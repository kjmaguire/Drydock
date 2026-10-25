import { expect } from 'remix/assert';
import { afterEach, describe, it } from 'remix/test';
import { Server } from 'lucide';
import type { Handle } from 'remix/component';
import { cleanup, renderPlain } from '../test/render.tsx';
import { AddRow, DetailHeader, FactGrid, MasterDetail, SettingRow, SettingsGroup, StatusWord, visibleGroups, type ListGroup } from './master-detail.tsx';

afterEach(cleanup);

const groups = (): ListGroup[] => [
  {
    key: 'services',
    title: 'Services',
    note: 'app sync',
    items: [
      { key: 'linear', title: 'Linear', logo: 'linear', sub: 'Issues and projects', status: { text: 'On', tone: 'ok' }, testId: 'row' },
      { key: 'github', title: 'GitHub', logo: 'github', sub: 'Issues and runners', href: '/github', testId: 'row' },
      { key: 'notion', title: 'Notion', logo: 'notion', sub: 'Pages as requirements', status: { text: 'Set up', tone: 'none' }, testId: 'row' },
    ],
  },
  { key: 'mine', title: 'Your servers', items: [{ key: 'pg', title: 'postgres-local', icon: Server, sub: 'Program on this PC', testId: 'row' }] },
];

// a page keeps the choice and draws the layout again with it, as the screens do
function Host(handle: Handle<{ start: string; picks: string[]; filterPicks: string[]; filters?: boolean; address?: string }>) {
  let selected = handle.props.start;
  return () => (
    <MasterDetail
      label="All integrations"
      groups={groups()}
      selected={selected}
      onSelect={(k) => (handle.props.picks.push(k), (selected = k), void handle.update())}
      search="Filter"
      filters={handle.props.filters ? [{ key: 'all', label: 'All', count: 4 }, { key: 'on', label: 'On', count: 1 }] : undefined}
      filter="all"
      onFilter={(k) => handle.props.filterPicks.push(k)}
      detail={
        <p data-testid="the-detail">
          detail of {selected}
          <span role="menu" tabIndex={-1} data-testid="a-menu">
            menu
          </span>
        </p>
      }
      testId="md"
      address={handle.props.address}
    />
  );
}

function render(selected: { key: string }, extra: { filters?: boolean; address?: string } = {}) {
  const picks: string[] = [];
  const filterPicks: string[] = [];
  const r = renderPlain(<Host start={selected.key} picks={picks} filterPicks={filterPicks} filters={extra.filters} address={extra.address} />);
  return { ...r, picks, filterPicks };
}

describe('MasterDetail', () => {
  it('lists the rows under their groups, with the state in words at the end, and marks the chosen one', () => {
    const { container } = render({ key: 'linear' });
    const heads = [...container.querySelectorAll('nav h2')].map((h) => h.textContent);
    expect(heads).toEqual(['Servicesapp sync', 'Your servers']);
    const rows = [...container.querySelectorAll<HTMLElement>('[data-testid="row"]')];
    expect(rows.map((r) => r.getAttribute('data-key'))).toEqual(['linear', 'github', 'notion', 'pg']);
    expect(rows[0]!.getAttribute('aria-current')).toBe('true');
    expect(rows[2]!.getAttribute('aria-current')).toBeNull();
    expect(rows[0]!.textContent).toBe('LinearIssues and projectsOn');
    // a state never rests on colour alone: the dot comes with words; "none" is the words only
    expect(rows[0]!.querySelector('[data-tone="ok"] span[aria-hidden="true"]')).not.toBeNull();
    expect(rows[2]!.querySelector('[data-tone="none"] span[aria-hidden="true"]')).toBeNull();
    expect(rows[0]!.querySelector('[data-brand="linear"]')).not.toBeNull();
    expect(rows[3]!.querySelector('svg')).not.toBeNull();
    expect(container.querySelector('[data-testid="the-detail"]')!.textContent).toBe('detail of linearmenu');
  });

  it('chooses a row on click; a row with a page of its own is a link', () => {
    const { container, picks, root } = render({ key: 'linear' });
    const notion = container.querySelector<HTMLButtonElement>('[data-key="notion"]')!;
    notion.click();
    root.flush();
    expect(picks).toEqual(['notion']);
    const gh = container.querySelector<HTMLAnchorElement>('[data-key="github"]')!;
    expect(gh.tagName).toBe('A');
    expect(gh.getAttribute('href')).toBe('/github');
  });

  it('filters by name and by the line under it, hides a group with nothing left, and says when nothing matches', () => {
    const { container, root } = render({ key: 'linear' });
    const box = container.querySelector<HTMLInputElement>('[data-testid="list-filter"]')!;
    box.value = 'requirements';
    box.dispatchEvent(new Event('input', { bubbles: true }));
    root.flush();
    expect([...container.querySelectorAll('[data-testid="row"]')].map((r) => r.getAttribute('data-key'))).toEqual(['notion']);
    expect(container.querySelector('[data-testid="list-group-mine"]')).toBeNull();
    box.value = 'zzz';
    box.dispatchEvent(new Event('input', { bubbles: true }));
    root.flush();
    expect(container.querySelectorAll('[data-testid="row"]').length).toBe(0);
    expect(container.querySelector('nav')!.textContent).toContain('Nothing matches "zzz".');
    expect(visibleGroups(groups(), 'POSTGRES').map((g) => g.key)).toEqual(['mine']);
  });

  it('moves through the rows with the arrow keys, skipping the rows that are links, and / focuses the filter', () => {
    const selected = { key: 'linear' };
    const { container, picks, root } = render(selected);
    const nav = container.querySelector('nav')!;
    const down = () => (nav.querySelector<HTMLElement>('[aria-current="true"]') ?? nav).dispatchEvent(new KeyboardEvent('keydown', { key: 'ArrowDown', bubbles: true }));
    down();
    root.flush();
    down();
    root.flush();
    expect(picks).toEqual(['notion', 'pg']);
    nav.querySelector<HTMLElement>('[data-key="pg"]')!.dispatchEvent(new KeyboardEvent('keydown', { key: 'ArrowUp', bubbles: true }));
    root.flush();
    expect(picks.at(-1)).toBe('notion');
    const md = container.querySelector('[data-testid="md"]')!;
    md.dispatchEvent(new KeyboardEvent('keydown', { key: '/', bubbles: true }));
    expect(document.activeElement).toBe(container.querySelector('[data-testid="list-filter"]'));
  });

  it('ArrowDown in the filter box chooses the first row and stops there', () => {
    const { container, picks, root } = render({ key: 'notion' });
    const box = container.querySelector<HTMLInputElement>('[data-testid="list-filter"]')!;
    box.focus();
    box.dispatchEvent(new KeyboardEvent('keydown', { key: 'ArrowDown', bubbles: true, cancelable: true }));
    root.flush();
    expect(picks).toEqual(['linear']);
  });

  it('leaves / alone with Ctrl, Alt or Meta held and inside a menu, a listbox or a dialog', () => {
    const { container } = render({ key: 'linear' }, { address: 'linear' }); // the detail showing, on a narrow window too
    const md = container.querySelector('[data-testid="md"]')!;
    const box = container.querySelector('[data-testid="list-filter"]');
    for (const mod of [{ ctrlKey: true }, { altKey: true }, { metaKey: true }]) {
      const e = new KeyboardEvent('keydown', { key: '/', bubbles: true, cancelable: true, ...mod });
      md.dispatchEvent(e);
      expect(e.defaultPrevented).toBe(false);
      expect(document.activeElement).not.toBe(box);
    }
    const menu = container.querySelector<HTMLElement>('[data-testid="a-menu"]')!;
    menu.focus();
    const e = new KeyboardEvent('keydown', { key: '/', bubbles: true, cancelable: true });
    menu.dispatchEvent(e);
    expect(e.defaultPrevented).toBe(false);
    expect(document.activeElement).toBe(menu);
  });

  it('shows the detail of the row the address named at once on narrow windows', () => {
    const { container } = render({ key: 'notion' }, { address: 'notion' });
    expect(container.querySelector('[data-testid="md"]')!.getAttribute('data-open')).toBe('true');
    cleanup();
    const plain = render({ key: 'notion' });
    expect(plain.container.querySelector('[data-testid="md"]')!.getAttribute('data-open')).toBe('false');
  });

  it('keeps the list when the address names a row that is gone and the page chose its default instead', () => {
    // a bookmark of an uninstalled agent: the page replaced the key with its default row, which is not the one asked for
    const { container } = render({ key: 'linear' }, { address: 'codex' });
    expect(container.querySelector('[data-testid="md"]')!.getAttribute('data-open')).toBe('false');
  });

  it('follows a new address while the page stays mounted, once the page has chosen its row', () => {
    const ctl: { set?: (address: string | undefined, selected: string) => void } = {};
    // the controller draws a page with one key for every address, so a new address arrives as a new prop, not a new page
    function LiveHost(handle: Handle<{ address?: string; selected: string }>) {
      let address = handle.props.address;
      let selected = handle.props.selected;
      ctl.set = (a, k) => ((address = a), (selected = k), void handle.update());
      return () => (
        <MasterDetail label="All integrations" groups={groups()} selected={selected} onSelect={(k) => ((selected = k), void handle.update())} detail={<p>detail of {selected}</p>} testId="md" address={address} />
      );
    }
    // the list was still loading when the page opened (nothing drawn as chosen); then a link names Linear and the page chooses it
    const r = renderPlain(<LiveHost selected="" />);
    const md = r.container.querySelector<HTMLElement>('[data-testid="md"]')!;
    expect(md.getAttribute('data-open')).toBe('false');
    ctl.set!('linear', 'linear');
    r.root.flush();
    expect(md.getAttribute('data-open')).toBe('true');
    cleanup();
    // a stale address the page replaced opens nothing; a real one after it does
    const r2 = renderPlain(<LiveHost address="codex" selected="linear" />);
    const md2 = r2.container.querySelector<HTMLElement>('[data-testid="md"]')!;
    expect(md2.getAttribute('data-open')).toBe('false');
    ctl.set!('notion', 'notion');
    r2.root.flush();
    expect(md2.getAttribute('data-open')).toBe('true');
    cleanup();
    // Back, then a re-read drops the chosen row and the page draws its default in its place: nothing was chosen, the list stays
    const r3 = renderPlain(<LiveHost address="notion" selected="notion" />);
    const md3 = r3.container.querySelector<HTMLElement>('[data-testid="md"]')!;
    expect(md3.getAttribute('data-open')).toBe('true');
    r3.container.querySelector<HTMLButtonElement>('[data-testid="detail-back"]')!.click();
    r3.root.flush();
    expect(md3.getAttribute('data-open')).toBe('false');
    ctl.set!('notion', 'linear');
    r3.root.flush();
    expect(md3.getAttribute('data-open')).toBe('false');
    cleanup();
    // the plain address (the page's own link in the sidebar, no key) shows the list again
    const r4 = renderPlain(<LiveHost address="linear" selected="linear" />);
    const md4 = r4.container.querySelector<HTMLElement>('[data-testid="md"]')!;
    expect(md4.getAttribute('data-open')).toBe('true');
    ctl.set!(undefined, 'linear');
    r4.root.flush();
    expect(md4.getAttribute('data-open')).toBe('false');
  });

  it('shows the filter choices the page owns and reports a choice', () => {
    const { container, filterPicks } = render({ key: 'linear' }, { filters: true });
    const all = container.querySelector<HTMLButtonElement>('[data-testid="list-filter-all"]')!;
    const onlyOn = container.querySelector<HTMLButtonElement>('[data-testid="list-filter-on"]')!;
    expect(all.getAttribute('aria-pressed')).toBe('true');
    expect(onlyOn.textContent).toBe('On 1');
    onlyOn.click();
    expect(filterPicks).toEqual(['on']);
  });

  it('opens the detail in place of the list after a choice, and Back returns to the list (narrow windows)', () => {
    const { container, root } = render({ key: 'linear' });
    const md = container.querySelector<HTMLElement>('[data-testid="md"]')!;
    expect(md.getAttribute('data-open')).toBe('false');
    container.querySelector<HTMLButtonElement>('[data-key="notion"]')!.click();
    root.flush();
    expect(md.getAttribute('data-open')).toBe('true');
    container.querySelector<HTMLButtonElement>('[data-testid="detail-back"]')!.click();
    root.flush();
    expect(md.getAttribute('data-open')).toBe('false');
  });

  it('opens a row the page chose itself (an Add row) once the page has put it in the address, as the pages do', () => {
    function AddHost(handle: Handle<{ start: string }>) {
      let selected = handle.props.start;
      let address: string | undefined;
      // as the pages do: the choice replaces the address (navigate), and the address comes back to the page as a prop
      const pick = (k: string) => ((selected = k), (address = k), void handle.update());
      return () => (
        <MasterDetail
          label="Providers"
          groups={[{ key: 'p', title: 'Providers', items: [{ key: 'a', title: 'A', testId: 'row' }], footer: <AddRow label="Add one" current={selected === 'new'} onClick={() => pick('new')} testId="add" /> }]}
          selected={selected}
          onSelect={pick}
          detail={<p data-testid="the-detail">detail of {selected}</p>}
          testId="md"
          address={address}
        />
      );
    }
    const { container, root } = renderPlain(<AddHost start="a" />);
    const md = container.querySelector<HTMLElement>('[data-testid="md"]')!;
    const add = container.querySelector<HTMLButtonElement>('[data-testid="add"]')!;
    expect(md.getAttribute('data-open')).toBe('false');
    expect(add.getAttribute('aria-current')).toBeNull();
    add.click();
    root.flush();
    expect(md.getAttribute('data-open')).toBe('true');
    expect(add.getAttribute('aria-current')).toBe('true');
    expect(container.querySelector('[data-testid="the-detail"]')!.textContent).toBe('detail of new');
  });
});

describe('detail parts', () => {
  it('draws the header with its state pill and actions, settings rows, facts and an add row', () => {
    let added = 0;
    const { container } = renderPlain(
      <div>
        <DetailHeader title="Linear" logo="linear" status={{ text: 'Connected', tone: 'ok' }} sub="Synced 4 min ago" actions={<button type="button">Sync now</button>} testId="head" />
        <SettingsGroup title="Connection" actions={<a href="#x">Replace key</a>} testId="grp">
          <SettingRow label="Workspace" control={<button type="button">Change</button>} testId="set">
            Acme
          </SettingRow>
        </SettingsGroup>
        <FactGrid label="Facts" facts={[{ label: 'Version', value: '2.1.309', mono: true }, { label: 'Signed in', value: 'yes' }]} testId="facts" />
        <AddRow label="Add a server" onClick={() => added++} testId="add" />
        <StatusWord text="Key expired" tone="warn" testId="sw" />
      </div>,
    );
    const head = container.querySelector('[data-testid="head"]')!;
    expect(head.querySelector('h2')!.textContent).toBe('Linear');
    expect(head.querySelector('[data-testid="detail-status"]')!.getAttribute('data-tone')).toBe('ok');
    expect(head.textContent).toBe('LinearConnectedSynced 4 min agoSync now');
    const grp = container.querySelector('[data-testid="grp"]')!;
    expect(grp.getAttribute('aria-label')).toBe('Connection');
    expect(grp.querySelector('h3')!.textContent).toBe('Connection');
    const set = container.querySelector('[data-testid="set"]')!;
    expect([...set.children].map((c) => c.textContent)).toEqual(['Workspace', 'Acme', 'Change']);
    const facts = container.querySelector('[data-testid="facts"]')!;
    expect([...facts.querySelectorAll('dt')].map((d) => d.textContent)).toEqual(['Version', 'Signed in']);
    expect(facts.querySelector('dd')!.getAttribute('data-mono')).toBe('true');
    container.querySelector<HTMLButtonElement>('[data-testid="add"]')!.click();
    expect(added).toBe(1);
    expect(container.querySelector('[data-testid="sw"]')!.textContent).toBe('Key expired');
  });
});
