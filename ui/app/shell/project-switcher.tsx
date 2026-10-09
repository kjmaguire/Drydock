// The project switcher at the top of the sidebar (Linear's workspace menu): a button with the current project's monogram
// and name that opens a menu of the projects (radio items, the current one checked), then "New project..." and "App
// settings". The kit's Menu (@remix-run/ui) gives it the keyboard: Enter, Space or the arrow keys open it, the arrows
// move, Enter selects, Escape closes and focus returns to the button.

import { css, navigate, type Handle } from 'remix/component';
import { ChevronsUpDown, FolderClosed, Plus, Settings } from 'lucide';
import { routes } from '../routes.ts';
import { services, watch } from '../state/services.tsx';
import { initials } from '../ui/data.tsx';
import { Icon } from '../ui/icon.tsx';
import { Menu, type MenuItem } from '../ui/menu.tsx';
import { t } from '../ui/tokens.ts';

const triggerCss = css({
  display: 'flex',
  alignItems: 'center',
  gap: t.s2,
  flex: '1',
  minWidth: '0',
  height: '32px',
  padding: `0 ${t.s2} 0 6px`,
  border: '0',
  borderRadius: t.rMd,
  background: 'transparent',
  color: t.text,
  fontSize: t.textMd,
  fontWeight: '600',
  textAlign: 'left',
  justifyContent: 'flex-start',
  transition: `background-color ${t.fast} ${t.ease}`,
  '&:hover, &[aria-expanded="true"]': { background: t.bgHover },
  '& > .name': { flex: '1', minWidth: '0', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' },
  '& > .name[data-empty="true"]': { color: t.textMuted, fontWeight: '500' },
  '& > .chev': { display: 'inline-flex', color: t.textFaint },
});

const monoCss = css({
  display: 'inline-grid',
  placeItems: 'center',
  flex: 'none',
  borderRadius: t.rSm,
  background: t.bgActive,
  boxShadow: `inset 0 0 0 1px ${t.border}`,
  color: t.textMuted,
  fontWeight: '600',
  lineHeight: '1',
  letterSpacing: '0.01em',
});

const menuSurface = css({ minWidth: '232px', maxWidth: '300px' });

/** A small neutral square with the project's initials (or a folder when there is no project). */
export function Monogram(handle: Handle<{ name?: string; size?: number }>) {
  return () => {
    const { name, size = 20 } = handle.props;
    return (
      <span mix={monoCss} aria-hidden="true" style={{ width: `${size}px`, height: `${size}px`, fontSize: `${Math.round(size * 0.45)}px` }}>
        {name ? initials(name) : <Icon icon={FolderClosed} size={Math.round(size * 0.65)} />}
      </span>
    );
  };
}

export function ProjectSwitcher(handle: Handle<{ onNavigate?: (href: string) => void }>) {
  const s = services(handle);
  watch(handle, s.workspace);

  const go = (href: string) => (handle.props.onNavigate ? handle.props.onNavigate(href) : void navigate(href));
  const select = (name: string) => {
    if (name.startsWith('project:')) s.workspace.selectProject(name.slice('project:'.length));
    else if (name === 'new') go(routes.projectNew.href());
    else if (name === 'settings') go(routes.settings.href());
  };

  return () => {
    const current = s.workspace.project;
    const items: MenuItem[] = [
      ...s.workspace.projectList.map(
        (p): MenuItem => ({ name: `project:${p.id}`, label: p.name, type: 'radio', checked: p.id === current?.id, value: p.id, lead: <Monogram name={p.name} size={18} /> }),
      ),
      { name: 'new', label: 'New project…', icon: Plus, separatorBefore: s.workspace.projectList.length > 0 },
      { name: 'settings', label: 'App settings', icon: Settings },
    ];
    return (
      <Menu
        label="Projects"
        items={items}
        onSelect={select}
        surface={menuSurface}
        trigger={(mix) => (
          <button type="button" mix={[triggerCss, mix]} aria-label="Switch project" title={current ? `${current.name} (switch project)` : 'No project'} data-testid="project-switcher">
            <Monogram name={current?.name} />
            <span class="name" data-empty={current ? undefined : 'true'}>
              {current?.name ?? 'No project'}
            </span>
            <span class="chev">
              <Icon icon={ChevronsUpDown} size={14} />
            </span>
          </button>
        )}
      />
    );
  };
}
