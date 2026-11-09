import { expect, test } from '@playwright/test';
import { choose, createProjectWithoutRepo, workspaceNav } from './helpers';

// Decision P121 against the real Go core: the diagnostics categories (crash reports kept by default, usage counters off), the
// OTLP bodies' compression and "Also send what was recorded before". P121's content switch was removed by decision P126.

test('diagnostics: each category is its own stored switch, and sending can take what was recorded before', async ({ page }) => {
  await createProjectWithoutRepo(page, 'diag-p121'); // the shell, with its sidebar
  await workspaceNav(page, 'nav-settings');
  const diag = page.getByTestId('diagnostics');
  const crash = diag.getByLabel('Keep crash reports');
  const usage = diag.getByLabel('Count feature use');
  await expect(crash).toHaveAttribute('aria-checked', 'true');
  await expect(usage).toHaveAttribute('aria-checked', 'false');
  await expect(diag.getByTestId('diag-crash-words')).toContainText('a report stays on this computer');

  await usage.click();
  await expect(diag.getByTestId('diag-usage')).toContainText('On: how often each part of Drydock is used is counted');
  await crash.click();
  await expect(diag.getByTestId('diag-crash-words')).toContainText('Off: a crash leaves no report');
  await page.reload();
  await expect(page.getByTestId('diagnostics').getByLabel('Keep crash reports')).toHaveAttribute('aria-checked', 'false');
  await expect(page.getByTestId('diagnostics').getByLabel('Count feature use')).toHaveAttribute('aria-checked', 'true');
  await expect(page.getByTestId('diagnostics').getByTestId('diag-crash')).toContainText(/Last changed by human:\S+/);
  // the page's own calls since then were counted by method name, and the preview shows them
  const tel = page.getByTestId('telemetry');
  await tel.getByRole('button', { name: 'Preview what an export contains' }).click();
  await expect(page.getByTestId('telemetry-preview')).toContainText('drydock.feature.uses');
  await expect(page.getByTestId('telemetry-preview')).toContainText(/"drydock\.feature": "[a-z]+\.[A-Za-z.]+"/);
  // back as they were
  await page.getByTestId('diagnostics').getByLabel('Keep crash reports').click();
  await expect(page.getByTestId('diagnostics').getByLabel('Keep crash reports')).toHaveAttribute('aria-checked', 'true');
  await page.getByTestId('diagnostics').getByLabel('Count feature use').click();
  await expect(page.getByTestId('diagnostics').getByLabel('Count feature use')).toHaveAttribute('aria-checked', 'false');

  // no switch sends the LLM calls' prompts and completions (decision P126)
  await expect(tel.getByTestId('content-export')).toHaveCount(0);
  await expect(tel).not.toContainText('prompts and completions');

  // compression and what was recorded before: telemetry on, a collector on this computer that nobody runs
  if ((await tel.getByTestId('telemetry-status').textContent()) !== 'on') await tel.getByRole('button', { name: 'Turn telemetry on' }).click();
  await expect(tel.getByTestId('telemetry-status')).toHaveText('on');
  // sending to a collector is a step further until an endpoint is set (decision P144)
  const more = tel.getByTestId('otlp-more');
  if ((await more.getAttribute('open')) === null) await more.locator('summary').click();
  const otlp = tel.getByTestId('otlp');
  await otlp.getByLabel('Endpoint (OTLP/HTTP)').fill('http://127.0.0.1:9');
  await otlp.getByRole('button', { name: 'Save endpoint' }).click();
  await expect(otlp.getByTestId('otlp-compression-words')).toHaveText('Bodies are not compressed (automatic).');
  await choose(otlp.getByLabel('Compression'), 'gzip');
  await expect(otlp.getByTestId('otlp-compression-words')).toHaveText('Bodies are compressed with gzip (you chose gzip).');
  const before = otlp.getByTestId('otlp-backfill');
  await before.getByLabel(/Also send what was recorded before/).check();
  await before.getByLabel(/At most \(rows/).fill('5000');
  await otlp.getByLabel('Send to a collector').click();
  await expect(before).toContainText('Send between 1 and 1000 rows.');
  await before.getByLabel(/At most \(rows/).fill('5');
  await otlp.getByLabel('Send to a collector').click();
  await expect(otlp.getByTestId('otlp-backfill-result')).toContainText('Also sent what was recorded before:');
  await expect(otlp.getByTestId('otlp-status')).toHaveAttribute('data-active', 'true');
  // off again, and telemetry off: nothing more is sent
  await otlp.getByLabel('Send to a collector').click();
  await expect(otlp.getByTestId('otlp-status')).toContainText('Not sending: sending is off.');
  await choose(otlp.getByLabel('Compression'), '');
  // the endpoint goes too, so the page is as a fresh core shows it (round3.spec.ts starts from no endpoint)
  await otlp.getByLabel('Endpoint (OTLP/HTTP)').fill('');
  await otlp.getByRole('button', { name: 'Save endpoint' }).click();
  await expect(otlp.getByRole('button', { name: 'Send a test span' })).toBeDisabled();
  await tel.getByRole('button', { name: 'Turn telemetry off' }).click();
  await expect(tel.getByTestId('telemetry-status')).toHaveText('off');
});
