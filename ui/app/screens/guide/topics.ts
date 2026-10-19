import type { Topic } from './model.ts';
import { START } from './content/start.ts';
import { CONCEPTS } from './content/concepts.ts';
import { HOWTO } from './content/howto.ts';
import { REFERENCE } from './content/reference.ts';
import { SAFETY } from './content/safety.ts';

/** Every topic id. A page asks for one by name (Page `guide`), so a typo does not compile. topics.test.ts keeps this list and the content in step. */
export const GUIDE_IDS = [
  'start',
  'overview',
  'crew',
  'tasks',
  'rooms',
  'workflows',
  'approvals',
  'delivery',
  'budgets',
  'skills',
  'knowledge',
  'connect-cli',
  'github',
  'integrations',
  'scan',
  'providers',
  'local-models',
  'backups',
  'support-bundle',
  'runners',
  'project-settings',
  'status-words',
  'routing-rules',
  'events',
  'agent-environment',
  'capabilities',
  'rule-licences',
  'evals',
  'privacy',
  'untrusted-text',
  'troubleshooting',
] as const;

export type GuideId = (typeof GUIDE_IDS)[number];

export const GUIDE_TOPICS: readonly Topic[] = [...START, ...CONCEPTS, ...HOWTO, ...REFERENCE, ...SAFETY];

export function guideTopic(id: string | undefined): Topic | undefined {
  return id === undefined ? undefined : GUIDE_TOPICS.find((t) => t.id === id);
}
