// The project switcher in the top bar (Linear's workspace menu): a button with the current project's monogram
// and name (or "Home" on a workspace page) that opens a menu: Home, the projects (radio items, the open one checked), then
// "New project..." and "App settings". Choosing a project opens it; Home opens the workspace. The kit's Menu (@remix-run/ui) gives it the keyboard: Enter, Space or the arrow keys open it, the arrows
// move, Enter selects, Escape closes and focus returns to the button.

import { css, navigate, type Handle } from 'remix/component';
import { ChevronsUpDown, FolderClosed, House, Plus, Settings } from 'lucide';
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
  flex: 'none',
  minWidth: '0',
  maxWidth: '260px',
  height: '38px',
  padding: `0 ${t.s2} 0 8px`,
  border: `1px solid ${t.border}`,
  borderRadius: t.rMd,
  background: t.bgSurface,
  color: t.text,
  fontSize: t.textMd,
  fontWeight: '600',
  textAlign: 'left',
  justifyContent: 'flex-start',
  transition: `background-color ${t.fast} ${t.ease}, border-color ${t.fast} ${t.ease}`,
  '&:hover, &[aria-expanded="true"]': { background: t.bgHover, borderColor: t.borderStrong },
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
      <span mix={monoCss} aria-hidden="true" style={{ width: `${size}px`, height: `${size}px`, fontSize: `${Math.max(10, Math.round(size * 0.45))}px` }}>
        {name ? initials(name) : <Icon icon={FolderClosed} size={Math.round(size * 0.65)} />}
      </span>
    );
  };
}

/** The workspace's mark in the switcher: a house in the monogram's square. */
function HomeMark(handle: Handle<{ size?: number }>) {
  return () => {
    const { size = 20 } = handle.props;
    return (
      <span mix={monoCss} aria-hidden="true" style={{ width: `${size}px`, height: `${size}px` }}>
        <Icon icon={House} size={Math.round(size * 0.65)} />
      </span>
    );
  };
}

export function ProjectSwitcher(handle: Handle<{ workspace?: boolean; onNavigate?: (href: string) => void }>) {
  const s = services(handle);
  watch(handle, s.workspace);

  const go = (href: string) => (handle.props.onNavigate ? handle.props.onNavigate(href) : void navigate(href));
  const select = (name: string) => {
    if (name.startsWith('project:')) {
      const id = name.slice('project:'.length);
      if (id === s.workspace.projectId && !handle.props.workspace) return;
      // open the project. A page of the previous project (its room, a task) never stays open under the new one: a message
      // typed there would go to the other project's agents.
      s.workspace.selectProject(id);
      go(routes.home.href());
    } else if (name === 'home') go(routes.workspace.href());
    else if (name === 'new') go(routes.projectNew.href());
    else if (name === 'settings') go(routes.settings.href());
  };

  return () => {
    const { workspace } = handle.props;
    const current = s.workspace.project;
    const items: MenuItem[] = [
      { name: 'home', label: 'Home', type: 'radio', checked: !!workspace, value: 'home', lead: <HomeMark size={18} /> },
      ...s.workspace.projectList.map(
        (p): MenuItem => ({
          name: `project:${p.id}`,
          label: p.name,
          type: 'radio',
          checked: !workspace && p.id === current?.id,
          value: p.id,
          lead: <Monogram name={p.name} size={18} />,
          separatorBefore: s.workspace.projectList[0] === p,
        }),
      ),
      { name: 'new', label: 'New project…', icon: Plus, separatorBefore: true },
      { name: 'settings', label: 'App settings', icon: Settings },
    ];
    return (
      <Menu
        label="Projects"
        items={items}
        onSelect={select}
        surface={menuSurface}
        trigger={(mix) => (
          <button
            type="button"
            mix={[triggerCss, mix]}
            aria-label="Switch project"
            title={workspace ? 'Home (open a project)' : current ? `${current.name} (switch project)` : 'No project'}
            data-testid="project-switcher"
          >
            {workspace ? <HomeMark /> : <Monogram name={current?.name} />}
            <span class="name" data-empty={workspace || current ? undefined : 'true'}>
              {workspace ? 'Home' : (current?.name ?? 'No project')}
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
