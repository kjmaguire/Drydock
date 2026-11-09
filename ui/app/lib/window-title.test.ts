import { expect } from 'remix/assert';
import { describe, it } from 'remix/test';
import { windowTitle } from './window-title.ts';

describe('the window title', () => {
  it("names the page and, on a project's page, the project, then Drydock; what waits on the person comes first", () => {
    expect(windowTitle({ name: 'tasks' }, 'gocalc', 0)).toBe('Tasks · gocalc · Drydock');
    expect(windowTitle({ name: 'home' }, 'gocalc', 2)).toBe('(2) Work · gocalc · Drydock');
    // a workspace page belongs to no project
    expect(windowTitle({ name: 'needs-you' }, 'gocalc', 3)).toBe('(3) Inbox · Drydock');
    expect(windowTitle({ name: 'tasks' }, undefined, 0)).toBe('Tasks · Drydock');
  });

  it("never carries a task's, a chat's or an agent's own title: only the page's fixed name", () => {
    expect(windowTitle({ name: 'task', id: 't1' }, 'gocalc', 0)).toBe('Task · gocalc · Drydock');
    expect(windowTitle({ name: 'chat', id: 'c1' }, 'gocalc', 0)).toBe('Chat · Drydock');
    expect(windowTitle({ name: 'agent', id: 'a1' }, 'gocalc', 0)).toBe('Agent · gocalc · Drydock');
    expect(windowTitle({ name: 'room', id: 'r1' }, 'gocalc', 0)).toBe('Room · gocalc · Drydock');
  });
});
