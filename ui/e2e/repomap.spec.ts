// The Repo map page end to end against the real Go core (decision P62). The harness is built with Go's default CGO setting,
// so on a machine with a C compiler (Linux CI, the Windows runner with MinGW) the real tree-sitter parser makes the map.
import { expect, test } from '@playwright/test';
import { mkdirSync, writeFileSync } from 'node:fs';
import { join } from 'node:path';
import { createProject, freshRepo, projectNav } from './helpers';

function write(repo: string, rel: string, body: string): void {
  const p = join(repo, ...rel.split('/'));
  mkdirSync(join(p, '..'), { recursive: true });
  writeFileSync(p, body);
}

test('the repo map ranks the most used file first, leaves ignored files out, and remakes it with a focus and a budget', async ({ page }) => {
  const repo = freshRepo();
  await createProject(page, 'gocalc-repomap', repo);
  // a store every handler uses; untracked files are mapped too (git ls-files --others), ignored ones are not
  write(repo, 'store/store.go', 'package store\n\n// Store keeps values.\ntype Store struct{}\n\nfunc NewStore() *Store { return &Store{} }\n\nfunc (s *Store) LookupValue(k string) string { return k }\n');
  for (const h of ['alpha', 'beta', 'gamma']) {
    write(repo, `handlers/${h}/handler.go`, `package ${h}\n\nimport "gocalc/store"\n\nfunc Handle() string {\n\ts := store.NewStore()\n\treturn s.LookupValue("${h}")\n}\n`);
  }
  write(repo, '.gitignore', 'private/\n');
  write(repo, 'private/hidden.go', 'package private\n\nfunc HiddenHelper() {}\n');

  await projectNav(page, 'nav-repo-map');
  await expect(page.getByTestId('repo-map')).toBeVisible();
  const map = page.getByTestId('repomap-text');
  await expect(map).toContainText('│func NewStore() *Store {', { timeout: 60_000 });
  expect((await map.textContent())!.startsWith('store/store.go:')).toBe(true);
  await expect(map).toContainText('calc.go');
  await expect(map).not.toContainText('HiddenHelper');
  await expect(page.getByTestId('repomap-ranking').locator('tbody tr').first()).toContainText('store/store.go');
  await expect(page.getByTestId('repomap-stats')).toContainText('of 2048 tokens');
  await expect(page.getByTestId('repomap-stats')).toContainText('Languages: go 6.');

  await page.getByLabel('Focus paths').fill('handlers/beta');
  await page.getByLabel('Token budget').fill('100');
  await page.getByTestId('repomap-make').click();
  await expect(page.getByTestId('repomap-stats')).toContainText('Focus: handlers/beta.');
  await expect(page.getByTestId('repomap-stats')).toContainText('of 256 tokens');
  await expect(page.getByLabel('Token budget')).toHaveValue('256');
  await expect(page.getByTestId('repomap-ranking').locator('tr', { hasText: 'handlers/beta/handler.go' })).toContainText('focus');
  await expect(page.locator('body')).not.toContainText('No repo map in this build');
});
