// Library > Skills (/library/skills): the workspace's skill library. A small tile per registry ("20 skills · 3 blocked ·
// 2 new"); the chosen registry's skills as compact rows to pick from (a name opens what the skill does); "Add to <project>"
// takes the picks to that project's Skills page, where they are added at once or given to an agent of the team to judge
// against the project's plan (decision P52). The Library itself holds no project.

import { css, navigate, on, type Handle } from 'remix/component';
import { ArrowRight, BookOpen, Cpu, GitBranch, RefreshCw } from 'lucide';
import type { LibrarySkill } from '../../api/extTypes.ts';
import { routes } from '../../routes.ts';
import { newProjectHref } from '../../shell/new.ts';
import { action, query, services, watch } from '../../state/services.tsx';
import { Button } from '../../ui/button.tsx';
import { Badge } from '../../ui/data.tsx';
import { Select } from '../../ui/select.tsx';
import { ErrorNote, Loading, Notice } from '../../ui/feedback.tsx';
import { Icon } from '../../ui/icon.tsx';
import { Page, Section } from '../../ui/layout.tsx';
import { emptyLine, Tile, tileGrid } from '../../ui/tiles.tsx';
import { t } from '../../ui/tokens.ts';
import { pickKey, SkillRows } from './skill-rows.tsx';
import { newInLibrary, registryStatusText, registryTileText, skillBrand, syncChanges } from './skills.ts';

const barCss = css({ display: 'flex', alignItems: 'center', gap: t.s2, flexWrap: 'wrap', '& [role="combobox"]': { maxWidth: '240px' } });
const panelCss = css({
  display: 'grid',
  gap: t.s3,
  padding: t.s4,
  border: `1px solid ${t.border}`,
  borderRadius: t.rLg,
  background: t.bgSurface,
  '& header': { display: 'flex', alignItems: 'center', gap: t.s2, flexWrap: 'wrap' },
  '& h3': { fontSize: t.textMd, fontWeight: '600', fontFamily: t.mono },
  '& .note': { margin: '0', fontSize: t.textSm, color: t.textMuted },
});

export function SkillLibraryScreen(handle: Handle<{ onNavigate?: (href: string) => void }>) {
  const s = services(handle);
  watch(handle, s.workspace);
  const regs = query(handle, { load: () => s.client.skillRegistries() });
  const st = query(handle, { load: () => s.client.skillRegistryStatus(), when: (e) => e.type === 'skill.library_synced' || e.type === 'skill.registry_updates' });
  const lib = query(handle, { load: () => s.client.skillLibraryList(), when: (e) => e.type === 'skill.library_synced' || e.type === 'skill.registry_updates' });
  let project = '';
  const target = () => (s.workspace.projectList.some((p) => p.id === project) ? project : (s.workspace.projectId ?? s.workspace.projectList[0]?.id ?? ''));
  const mine = query(handle, {
    load: () => s.client.skillList(target()),
    key: target,
    enabled: () => target() !== '',
    when: (e) => e.type.startsWith('skill.'),
  });
  const act = action(handle);
  let registry = '';
  let picked = new Set<string>();
  const set = (f: () => void) => {
    f();
    void handle.update();
  };
  // the picks go to the project's Skills page: added there at once, or judged by an agent of the team first
  const toProject = (ls: readonly LibrarySkill[]) => {
    const pid = target();
    if (!pid || ls.length === 0) return;
    s.ui.handSkillPicks(
      pid,
      ls.map((l) => ({ registryId: l.registryId, name: l.name })),
    );
    s.workspace.selectProject(pid);
    const href = routes.skills.href();
    if (handle.props.onNavigate) handle.props.onNavigate(href);
    else void navigate(href);
  };
  const sync = async (registryId?: string) => {
    await act.run('sync', () => s.client.skillRegistryCheck(registryId));
    st.reload();
    lib.reload();
  };

  return () => {
    const projects = s.workspace.projectList;
    const pid = target();
    const pname = projects.find((p) => p.id === pid)?.name ?? '';
    const registries = regs.data ?? [];
    const statuses = st.data ?? [];
    const library = lib.data ?? [];
    const have = new Set((mine.data ?? []).map((k) => k.name));
    const openReg = registries.find((r) => r.id === registry);
    const openStatus = openReg ? statuses.find((x) => x.registryId === openReg.id) : undefined;
    const skills = openReg ? library.filter((l) => l.registryId === openReg.id) : [];
    const pickedSkills = library.filter((l) => picked.has(pickKey(l)));
    const addable = skills.filter((l) => l.status !== 'blocked' && !have.has(l.name));
    const fresh = newInLibrary(statuses);
    const changes = syncChanges(openStatus);
    return (
      <Page title="Skills" icon={Cpu} wide testId="skill-library" subtitle="From the registries you enabled; add them to a project there" guide="skills">
        {s.workspace.projects.data && projects.length === 0 ? (
          <Notice tone="info" testId="skill-library-no-project">
            Skills are added to a project. <a href={newProjectHref()}>Create a project</a> first; you can browse the registries meanwhile.
          </Notice>
        ) : null}
        <Section title="Registries" testId="library-skills">
          <ErrorNote error={regs.error ?? st.error ?? lib.error ?? act.error} what="Skills" />
          {regs.loading && !regs.data ? <Loading /> : null}
          {regs.data && registries.length === 0 ? (
            <p mix={emptyLine} data-testid="lib-skills-empty">
              <Icon icon={BookOpen} size={14} />
              <strong>No skill registry is allowed yet.</strong> Allow one on a project's Skills page.
            </p>
          ) : null}
          {registries.length > 0 ? (
            <ul mix={tileGrid} aria-label="Skill registries">
              {registries.map((r) => {
                const state = registryTileText(
                  statuses.find((x) => x.registryId === r.id),
                  r.enabled,
                );
                return (
                  <Tile
                    key={r.id}
                    title={r.repo}
                    logo={skillBrand(r.id, 'registry') || undefined}
                    icon={GitBranch}
                    state={state.text}
                    tone={state.tone}
                    selected={registry === r.id}
                    onSelect={() =>
                      set(() => {
                        registry = registry === r.id ? '' : r.id;
                      })
                    }
                    testId="lib-skill-registry-tile"
                    data={{ registry: r.id }}
                  />
                );
              })}
            </ul>
          ) : null}
          {lib.data && registries.length > 0 && library.length === 0 ? (
            <p mix={emptyLine} data-testid="lib-skills-filling">
              <Icon icon={RefreshCw} size={14} />
              <strong>The Library is filling from your registries...</strong> It syncs at start and twice a day.
              <Button size="sm" icon={RefreshCw} disabled={act.busy !== ''} mix={[on('click', () => void sync())]}>
                {act.busy === 'sync' ? 'Syncing...' : 'Sync now'}
              </Button>
            </p>
          ) : null}
        </Section>
        {openReg ? (
          <section mix={panelCss} data-testid="lib-skill-registry" data-registry={openReg.id} aria-label={openReg.repo}>
            <header>
              <h3>{openReg.repo}</h3>
              <Badge tone={openReg.enabled ? 'ok' : 'muted'}>{openReg.enabled ? 'enabled' : 'disabled'}</Badge>
              {openReg.curated ? <Badge>curated</Badge> : <Badge tone="warn">added by you</Badge>}
              {openReg.license ? <Badge title="License">{openReg.license}</Badge> : null}
            </header>
            {openReg.notes ? <p class="note">{openReg.notes}</p> : null}
            <p class="note" data-testid="lib-skill-registry-status">
              {registryStatusText(openStatus, openReg.enabled).text}
            </p>
            {openStatus?.lastError ? <ErrorNote error={`Last sync failed: ${openStatus.lastError}`} /> : null}
            {changes.added.length > 0 ? (
              <p class="note" data-testid="lib-skill-registry-new">
                New in the last sync: {changes.added.map((c) => c.name).join(', ')}
              </p>
            ) : null}
            <div mix={barCss}>
              {projects.length > 0 ? (
                <>
                  <label>
                    Project{' '}
                    <Select
                      label="Project to add skills to"
                      size="sm"
                      value={pid}
                      options={projects.map((p) => ({ value: p.id, label: p.name }))}
                      onChange={(v) => set(() => (project = v))}
                    />
                  </label>
                  <Button
                    variant="primary"
                    size="sm"
                    icon={ArrowRight}
                    disabled={pickedSkills.length === 0}
                    data-testid="lib-skill-add-picked"
                    mix={on('click', () => toProject(pickedSkills))}
                  >
                    {pickedSkills.length === 0 ? 'Pick skills to add' : `Add ${pickedSkills.length === 1 ? pickedSkills[0]!.name : `${pickedSkills.length} skills`} to ${pname}`}
                  </Button>
                  {addable.length > 0 ? (
                    <Button size="sm" data-testid="lib-skill-pick-all" mix={on('click', () => set(() => (picked = new Set(addable.map(pickKey)))))}>
                      Pick all {addable.length}
                    </Button>
                  ) : null}
                  {pickedSkills.length > 0 ? (
                    <Button size="sm" variant="ghost" mix={on('click', () => set(() => (picked = new Set())))}>
                      Clear
                    </Button>
                  ) : null}
                </>
              ) : (
                <span class="note">Create a project to add skills to it.</span>
              )}
              <Button size="sm" variant="ghost" icon={RefreshCw} disabled={!openReg.enabled || act.busy !== ''} mix={[on('click', () => void sync(openReg.id))]}>
                {act.busy === 'sync' ? 'Syncing...' : 'Sync now'}
              </Button>
            </div>
            {skills.length === 0 ? (
              <p class="note">No skill from this registry is in the Library yet.</p>
            ) : (
              <SkillRows
                skills={skills}
                picked={picked}
                onPick={(k, yes) =>
                  set(() => {
                    picked = new Set(picked);
                    if (yes) picked.add(k);
                    else picked.delete(k);
                  })
                }
                have={have}
                fresh={fresh}
                projectName={pname}
                label={`Skills from ${openReg.repo}`}
                testId="lib-skill-tile"
                actions={(l) =>
                  pid && !have.has(l.name) ? (
                    <Button variant="primary" size="sm" icon={ArrowRight} data-testid="lib-skill-add" mix={on('click', () => toProject([l]))}>
                      Add to {pname}
                    </Button>
                  ) : null
                }
              />
            )}
          </section>
        ) : null}
      </Page>
    );
  };
}
