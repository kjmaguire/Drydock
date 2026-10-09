import { expect } from 'remix/assert';
import { describe, it } from 'remix/test';
import { routes } from '../routes.ts';
import { hashToPath, parseRoute } from './route.ts';

describe('routes', () => {
  it('builds the paths the screens link to', () => {
    expect(routes.task.href({ id: 'a b' })).toBe('/task/a%20b');
    expect(routes.run.href({ id: 't1' })).toBe('/task/t1/run');
    expect(routes.run.href({ id: 't1', runId: 'r1' })).toBe('/task/t1/run/r1');
    expect(routes.room.href({ id: 'r' })).toBe('/room/r');
    expect(routes.settings.href()).toBe('/settings');
  });

  it('parses every path back into the screen it shows', () => {
    expect(parseRoute('/')).toEqual({ name: 'home' });
    expect(parseRoute('/room/r%201')).toEqual({ name: 'room', id: 'r 1' });
    expect(parseRoute('/tasks/new')).toEqual({ name: 'task-new' });
    expect(parseRoute('/task/t1')).toEqual({ name: 'task', id: 't1' });
    expect(parseRoute('/task/t1/run')).toEqual({ name: 'run', taskId: 't1' });
    expect(parseRoute('/task/t1/run/r2')).toEqual({ name: 'run', taskId: 't1', runId: 'r2' });
    expect(parseRoute('/project/new')).toEqual({ name: 'project-new' });
    expect(parseRoute('/settings')).toEqual({ name: 'settings' });
    expect(parseRoute('/nope')).toEqual({ name: 'not-found' });
    expect(parseRoute('/team/extra')).toEqual({ name: 'not-found' });
  });

  it('turns old hash links into paths', () => {
    expect(hashToPath('#/task/x')).toBe('/task/x');
    expect(hashToPath('#/')).toBe('/');
    expect(hashToPath('#main')).toBeUndefined();
    expect(hashToPath('')).toBeUndefined();
  });
});
