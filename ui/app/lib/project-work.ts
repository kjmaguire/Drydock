// Where a flow lands in a project (decision P142): the project's home ("/"), its Work view, with the item the flow made selected
// (?item=task:<id> or ?item=chat:<id>, decision P141). Every flow that opens a project on a piece of its work goes through this
// one function, so a change of that address is a change here alone. The caller selects the project first.

import { routes } from '../routes.ts';

/** A piece of a project's work: a task or a chat. */
export interface WorkItem {
  kind: 'task' | 'chat';
  id: string;
}

/** The project's Work view, on the item when one is given. */
export function projectWorkHref(item?: WorkItem): string {
  const home = routes.home.href();
  return item ? `${home}?item=${item.kind}:${encodeURIComponent(item.id)}` : home;
}
