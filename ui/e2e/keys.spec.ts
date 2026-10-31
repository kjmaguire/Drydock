// Keyboard shortcuts against the real Go core (decision P132): the region keys move the keyboard, a person gives the Inbox other
// keys in Settings and they work at once and after a reload (the core keeps them for the workspace), keys another shortcut has
// are refused, and Reset all puts the defaults back (the core is shared by the specs: nothing is left bound).
import { expect, test } from '@playwright/test';
import { appUrl } from './helpers';

test('the region keys, and a shortcut given other keys in Settings, kept by the core and reset', async ({ page }) => {
  await page.setViewportSize({ width: 1366, height: 900 });
  const settings = new URL(appUrl());
  settings.pathname = '/settings';
  await page.goto(settings.toString());
  const section = page.getByRole('region', { name: 'Keyboard shortcuts' });
  await expect(section).toBeVisible({ timeout: 60_000 });

  // Ctrl+2 puts the keyboard on the page, Ctrl+1 in the sidebar
  await page.keyboard.press('Control+Digit2');
  await expect(page.locator('#main')).toBeFocused();
  await page.keyboard.press('Control+Digit1');
  await expect(page.getByTestId('sidebar').locator(':focus')).toHaveCount(1);

  const inbox = section.locator('[data-testid="keys-row"][data-id="inbox"]');
  await inbox.getByRole('button', { name: 'Change the keys of: Open the Inbox' }).click();
  const capture = inbox.getByTestId('keys-capture');
  await expect(capture).toBeFocused();
  await page.keyboard.press('Control+KeyB');
  await expect(inbox.getByTestId('keys-problem')).toContainText('Ctrl+B already does this');
  await expect(inbox.getByTestId('keys-save')).toBeDisabled();
  await page.keyboard.press('Control+Alt+KeyI');
  await expect(inbox.getByTestId('keys-problem')).toHaveCount(0);
  await inbox.getByTestId('keys-save').click();
  await expect(inbox.locator('kbd')).toHaveText(['Ctrl', 'Alt', 'I']);

  // after a reload the core still has it: the About map shows it and the keys open the Inbox
  await page.reload();
  await expect(section).toBeVisible({ timeout: 60_000 });
  const line = page.getByTestId('about-keymap').locator('tr', { hasText: 'Open the Inbox' });
  await expect(line.locator('kbd')).toHaveText(['Ctrl', 'Alt', 'I']);
  await page.locator('#main').focus();
  await page.keyboard.press('Control+Alt+KeyI');
  await expect(page).toHaveURL(/\/needs-you/);

  await page.goto(settings.toString());
  await expect(section).toBeVisible({ timeout: 60_000 });
  await section.getByTestId('keys-reset-all').click();
  await expect(section.getByTestId('keys-reset-all')).toHaveCount(0);
  await expect(inbox.locator('kbd')).toHaveText(['Ctrl', 'Shift', 'K']);
});
