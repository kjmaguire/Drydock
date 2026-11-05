// A project's Settings tab (decision P141): its sections, in the order its left list shows them. A project's own settings and
// integrations, and every page that left the project's tabs (Overview's Activity, Budgets, Evals report and Security; Tasks'
// Plan and Milestones; Knowledge with its pages), keep their addresses and test ids and open here, with the Settings tab marked.
// The shape is the workspace Settings' (shell/settings-sections.ts): the same rail, search and pills draw both.
//
// To add a section: add an entry here, give its route the hub 'project-settings' (lib/route.ts HUB_OF) and an action in
// actions/controller.tsx.

import type { Route } from '../lib/route.ts';
import { routes } from '../routes.ts';
import { sectionOf, type SettingsPage, type SettingsSection } from './settings-sections.ts';

const one = (label: string, href: string, testId: string, on: Route['name'][]): SettingsPage[] => [{ label, href, testId, on }];

export const PROJECT_SECTIONS: readonly SettingsSection[] = [
  {
    id: 'general',
    title: 'General',
    href: routes.project.href(),
    testId: 'nav-project',
    pages: one('General', routes.project.href(), 'nav-project-general', ['project']),
    rows: ['Name', 'Folder', 'Repository', 'Branch', 'Scheduler', 'Sandbox', 'Templates', 'Checks', 'Permissions', 'Archive'],
  },
  {
    id: 'integrations',
    title: 'Integrations',
    href: routes.projectIntegrations.href(),
    testId: 'nav-project-integrations',
    pages: one('Integrations', routes.projectIntegrations.href(), 'nav-project-integrations-page', ['project-integrations']),
    rows: ['MCP servers', 'Grants', 'Approvals', 'Issue sync'],
  },
  {
    id: 'knowledge',
    title: 'Knowledge',
    href: routes.knowledge.href(),
    testId: 'nav-knowledge',
    pages: [
      { label: 'Overview', href: routes.knowledge.href(), testId: 'nav-knowledge-home', on: ['knowledge'] },
      { label: 'Requirements', href: routes.requirements.href(), testId: 'nav-requirements', on: ['requirements'] },
      { label: 'Expert packs', href: routes.packs.href(), testId: 'nav-packs', on: ['packs'] },
      { label: 'Skills', href: routes.skills.href(), testId: 'nav-skills', on: ['skills'] },
      { label: 'Memory', href: routes.memory.href(), testId: 'nav-memory', on: ['memory'] },
      { label: 'Repo map', href: routes.repoMap.href(), testId: 'nav-repo-map', on: ['repo-map'] },
    ],
    rows: ['Ask', 'Sources', 'Requirements', 'Expert packs', 'Skills', 'Memory', 'Repo map'],
  },
  {
    id: 'plan',
    title: 'Plan & milestones',
    href: routes.plan.href(),
    testId: 'nav-planning',
    pages: [
      { label: 'Plan', href: routes.plan.href(), testId: 'nav-plan', on: ['plan'] },
      { label: 'Milestones', href: routes.milestones.href(), testId: 'nav-milestones', on: ['milestones'] },
    ],
    rows: ['Plan with PlanTeam', 'Milestones'],
  },
  {
    id: 'activity',
    title: 'Activity',
    href: routes.activity.href(),
    testId: 'nav-activity',
    pages: one('Activity', routes.activity.href(), 'nav-activity-page', ['activity']),
    rows: ['Events', 'Audit'],
  },
  {
    id: 'budgets',
    title: 'Budgets',
    href: routes.budgets.href(),
    testId: 'nav-budgets',
    pages: one('Budgets', routes.budgets.href(), 'nav-budgets-page', ['budgets']),
    rows: ['Spend', 'Limits', 'Usage'],
  },
  {
    id: 'evals',
    title: 'Evals report',
    href: routes.evals.href(),
    testId: 'nav-evals',
    pages: one('Evals report', routes.evals.href(), 'nav-evals-page', ['evals']),
    rows: ['First check', 'Quality', 'Report'],
  },
  {
    id: 'security',
    title: 'Security',
    href: routes.security.href(),
    testId: 'nav-security',
    pages: one('Security', routes.security.href(), 'nav-security-page', ['security']),
    rows: ['Scan', 'Findings', 'Secrets'],
  },
];

/** The project section a route belongs to, if any. */
export function projectSectionOf(route: Pick<Route, 'name'>): SettingsSection | undefined {
  return sectionOf(route, PROJECT_SECTIONS);
}
