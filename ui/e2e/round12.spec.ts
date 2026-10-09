import { expect, test } from '@playwright/test';
import { createAgent, createProject, roomLink } from './helpers';

// The composer's typing aids, attachments and the command palette against the real Go core.
test('mentions autocomplete, slash commands, attachments and the Ctrl+K palette', async ({ page }) => {
  await createProject(page, 'gocalc-composer');
  await createAgent(page, 'Backend One', 'backend');
  await roomLink(page, 'general').click();
  const box = page.getByLabel('Message to #general');
  await expect(box).toBeVisible();

  // @ suggests the room's members; Enter inserts the handle the room resolves
  await box.click();
  await box.pressSequentially('hello @back');
  await expect(page.getByRole('listbox', { name: 'Suggestions' })).toContainText('Backend One');
  await box.press('Enter');
  await expect(box).toHaveValue('hello @backend-one ');
  await box.pressSequentially('please look');
  await box.press('Control+Enter');
  const mine = page.locator('[data-testid=message][data-author-kind=human]').filter({ hasText: 'please look' });
  await expect(mine).toBeVisible();

  // a slash command posts a marker
  await box.fill('/decision Keep the parser in Go');
  await box.press('Escape');
  await page.getByRole('button', { name: 'Send' }).click();
  await expect(page.locator('[data-testid=message]').filter({ hasText: 'Keep the parser in Go' }).getByText('DECISION').first()).toBeVisible();

  // attach a file
  await page.getByTestId('composer-file').setInputFiles({ name: 'notes.txt', mimeType: 'text/plain', buffer: Buffer.from('remember the edge cases\n') });
  await expect(page.getByRole('list', { name: 'Files to attach' })).toContainText('notes.txt');
  await box.fill('the notes');
  await page.getByRole('button', { name: 'Send' }).click();
  const withFile = page.locator('[data-testid=message]').filter({ hasText: 'the notes' });
  await expect(withFile.getByTestId('attachment')).toContainText('notes.txt');
  await withFile.getByRole('button', { name: 'Save' }).click();
  const download = page.waitForEvent('download');
  await withFile.getByRole('link', { name: 'Download notes.txt' }).click();
  const d = await download;
  expect(d.suggestedFilename()).toBe('notes.txt');

  // Ctrl+K: the palette goes to a screen
  await page.locator('body').click({ position: { x: 5, y: 5 } });
  await page.keyboard.press('Control+k');
  const cmd = page.getByPlaceholder(/^Go to, open or do/);
  await expect(cmd).toBeVisible();
  await cmd.fill('team');
  await page.keyboard.press('Enter');
  await expect(page).toHaveURL(/\/team$/);
  await expect(cmd).toBeHidden();
});
