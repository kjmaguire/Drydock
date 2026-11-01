// Models on this PC end to end against the real Go core (decisions P65, P95, P97, P98): the this-pc row of the Models page (/models),
// reached from the sidebar and the command menu; its server is a provider the core registers, which is that row, not a second one. The harness (cmd/drydock-e2e) gives the core PROTOCOL FIXTURES:
// GitHub's releases API for the TurboQuant build's repository (b10269-1.7.1 and b10298-2.0.0, the newer with a Vulkan and a CPU
// archive holding a placeholder llama-server) and the Hugging Face Hub with small GGUF repositories; the "server" started is the
// harness's own stand-in (internal/localllm/llamatest, a TEST DOUBLE), which loads its model for a second. Nothing reaches GitHub
// or Hugging Face; no llama.cpp runs. The stand-in reports one CUDA GPU of 16 GB with 14 GB free; the core 32 GB of RAM with 20 GB
// available. The fixture's GGUF files are random bytes, so no KV cache can be estimated from their headers.
import { expect, test, type Locator, type Page } from '@playwright/test';
import { appUrl, choose, sidebar } from './helpers';

/** Pick an item of a menu. A menu still closing after its last choice ignores a click on its opener, as it does for a person. */
async function fromMenu(trigger: Locator, item: Locator): Promise<void> {
  await expect(async () => {
    if (!(await item.isVisible())) await trigger.click();
    await item.click({ timeout: 2_000 });
  }).toPass({ timeout: 20_000 });
}

/** A path of the app with the harness's token kept (the page reads it from the address). */
const at = (path: string): string => {
  const u = new URL(appUrl());
  u.pathname = path;
  return u.toString();
};

/** Open a repository of the Hub list and download one of its files; the model row appears once the file is checked. */
async function download(page: Page, repo: string, variant: string): Promise<Locator> {
  const row = page.locator(`[data-testid=local-repo][data-id="${repo}"]`);
  await row.click();
  const file = page.locator(`[data-testid=local-variant][data-name="${variant}"]`);
  await expect(file).toBeVisible();
  await file.getByTestId('local-download').click();
  const model = page.locator(`[data-testid=local-model][data-id="${repo}:${variant}"]`);
  await expect(model).toBeVisible({ timeout: 60_000 });
  return model;
}

test('Models on this PC: the Models page opens it; set up the engine, find and download models, serve one, switch, stop; its server is that one row', async ({ page }) => {
  test.setTimeout(300_000);
  await page.setViewportSize({ width: 1366, height: 900 });
  await page.goto(appUrl());
  // the sidebar's Workspace link opens the page; it is the page's link there, and no project is open in the sidebar
  const link = sidebar(page).getByTestId('nav-side-models');
  await expect(link).toHaveAttribute('href', '/models');
  await link.click();
  await expect(page).toHaveURL(/\/models(\?|$)/);
  await expect(link).toHaveAttribute('aria-current', 'page');
  await expect(sidebar(page).getByTestId('nav-overview')).toHaveCount(0);
  await expect(page.getByTestId('models-page').getByRole('heading', { level: 1 })).toHaveText('Models');
  const thisPc = page.locator('[data-testid=models-row][data-key=this-pc]');
  await expect(thisPc).toHaveAttribute('aria-current', 'true');
  const pageEl = page.getByTestId('local-models');
  await expect(pageEl.getByRole('heading', { level: 2 })).toHaveText('Models on this PC');
  await expect(pageEl).toContainText('Private: prompts and code never leave this computer');

  // "/" goes to the Hub search
  const hub = page.getByTestId('local-search-input');
  await expect(hub).toHaveAttribute('placeholder', 'Find a model on Hugging Face');
  await page.evaluate(() => (document.activeElement as HTMLElement | null)?.blur());
  await page.keyboard.press('/');
  await expect(hub).toBeFocused();
  await expect(hub).toHaveValue('');
  await hub.blur();

  // not set up (the harness has no AutoSetup and no tools folder): Set up, with nothing to choose and no llama.cpp named
  const engine = page.getByTestId('local-runtime');
  await expect(engine).toHaveAttribute('data-installed', 'false');
  await expect(engine.getByRole('heading', { name: 'Engine' })).toBeVisible();
  await expect(page.getByTestId('local-source')).toHaveCount(0);
  await expect(page.getByTestId('local-backend')).toHaveCount(0);
  await expect(pageEl).not.toContainText('llama');
  await expect(thisPc).toContainText('Set up');
  await engine.getByTestId('local-install').click();
  // the setup runs in the background; the page follows it through local.changed until the newest TurboQuant build is in
  await expect(engine).toHaveAttribute('data-installed', 'true', { timeout: 60_000 });
  await expect(engine.getByTestId('local-build')).toHaveText('TurboQuant build b10298-2.0.0 · Vulkan');
  await expect(engine.getByTestId('local-runtime-note')).toHaveText('Drydock set it up and keeps it current. An update restarts the server on the same model.');
  await expect(engine.getByTestId('local-job')).toHaveAttribute('data-state', 'done');
  await expect(engine.getByTestId('local-check-updates')).toHaveText('Check for updates');

  // Memory: the stand-in's GPU (16 GB, 14 GB free) and the RAM the core read (32 GB, 20 GB available)
  const memory = page.getByTestId('local-memory-card');
  const gpu = memory.locator('[data-testid=local-device][data-id=CUDA0]');
  await expect(gpu).toBeVisible({ timeout: 60_000 });
  await expect(gpu).toContainText('GPU · CUDA');
  await expect(gpu.getByTestId('local-bar-used')).toHaveText('2 of 16 GB');
  await expect(memory.getByTestId('local-ram').getByTestId('local-bar-used')).toHaveText('12 of 32 GB');

  // nothing served yet
  const server = page.getByTestId('local-server');
  await expect(server).toHaveAttribute('data-state', 'stopped');
  await expect(server.getByTestId('local-server-title')).toHaveText('Nothing served');
  await expect(server.getByTestId('local-switch')).toBeDisabled();

  // suggestions come from the Hub fixture; Close suggestions shrinks the table back and Show suggestions returns
  const table = page.getByRole('region', { name: 'Local models', exact: true });
  await table.getByTestId('local-suggested').click();
  await expect(table.locator('[data-testid=local-repo][data-id="ggml-org/gpt-oss-20b-GGUF"]')).toBeVisible();
  await expect(table.locator('[data-testid=local-repo][data-id="ggml-org/Qwen3-Coder-30B-A3B-Instruct-GGUF"]')).toBeVisible();
  await expect(table.getByTestId('local-suggested')).toHaveCount(0);
  await table.getByTestId('local-repos-close').click();
  await expect(table.getByTestId('local-repo')).toHaveCount(0);
  await expect(table.getByTestId('local-repos-close')).toHaveCount(0);
  await expect(table.getByTestId('local-suggested')).toBeVisible();

  // a search (Enter in the head's box) finds the tiny repository; its one file downloads, with the mockup's row
  await hub.fill('tiny');
  await hub.press('Enter');
  await expect(table.getByTestId('local-repos-title')).toHaveText('Hugging Face, "tiny": 1 repository');
  await expect(table.getByTestId('local-repo')).toHaveCount(1);
  await table.locator('[data-testid=local-repo][data-id="acme/tiny-GGUF"]').click();
  const variant = table.locator('[data-testid=local-variant][data-name="Q4_0"]');
  await expect(variant).toContainText('2 MB');
  await expect(variant.getByTestId('local-fit')).toHaveText('Fits · GPU');
  await variant.getByTestId('local-download').click();
  const tiny = table.locator('[data-testid=local-model][data-id="acme/tiny-GGUF:Q4_0"]');
  await expect(tiny).toBeVisible({ timeout: 60_000 });
  await expect(tiny.locator('.name')).toHaveText('tiny · Q4_0');
  await expect(tiny.locator('.repo')).toHaveText('acme/tiny-GGUF');
  await expect(tiny.getByTestId('local-fit')).toHaveText('Fits · GPU');
  // the downloaded file is not offered again
  await expect(variant.locator('.act')).toHaveText('downloaded');

  // Serve: the stand-in loads for a second, then the Serving card has the model, its address, its KV cache and when it started
  await tiny.getByTestId('local-serve').click();
  await expect(server).toHaveAttribute('data-state', 'running', { timeout: 60_000 });
  await expect(server.getByTestId('local-server-state')).toHaveAttribute('aria-label', 'running');
  await expect(server.getByTestId('local-server-title')).toHaveText('Serving tiny');
  await expect(server.getByTestId('local-server-url')).toHaveText('127.0.0.1:9933');
  await expect(server.getByTestId('local-server-url')).toHaveAttribute('title', 'http://127.0.0.1:9933/v1');
  await expect(server.getByTestId('local-used-by')).toHaveText('No agent yet');
  await expect(server.getByTestId('local-kv')).toContainText(' · ');
  await expect(server.getByTestId('local-started')).toHaveText(/just now|min ago/);
  await expect(server.getByTestId('local-provider-note')).toContainText('Agents reach it as a provider');
  await expect(thisPc).toContainText('Serving');
  await expect(thisPc).toContainText('tiny · Q4_0');
  await expect(tiny.getByTestId('local-fit')).toHaveText('Serving · GPU');
  await expect(tiny.locator('.act')).toHaveText('downloaded');
  // the served model is in the GPU bar, beside its KV cache (which the fixture's random file does not let the core estimate)
  await expect(gpu.locator('[data-part=model]')).toHaveCount(1);
  await expect(gpu.locator('.legend')).toContainText('model');
  await expect(gpu.locator('.legend')).toContainText('KV cache not estimated');
  // the log shows the stand-in's lines
  await server.getByTestId('local-log').click();
  await expect(server.getByTestId('local-log-lines')).toBeVisible();

  // a setting changed while the server runs asks for a restart
  await choose(page.getByTestId('local-cache'), 'q4_0');
  await expect(page.getByTestId('local-restart')).toContainText('Restart the server to apply');

  // a second model, then Switch model serves it
  await table.getByTestId('local-suggested').click();
  const qwen = await download(page, 'ggml-org/Qwen3-Coder-30B-A3B-Instruct-GGUF', 'Q4_K_M');
  await expect(qwen.getByTestId('local-fit')).toHaveText('Fits · GPU');
  const sw = server.getByTestId('local-switch');
  await expect(sw).toHaveText('Switch model');
  await expect(sw).toBeEnabled();
  await fromMenu(sw, page.getByRole('menu', { name: 'Switch model' }).getByRole('menuitemradio', { name: 'Qwen3-Coder-30B-A3B-Instruct · Q4_K_M' }));
  await expect(server.getByTestId('local-server-title')).toHaveText('Serving Qwen3-Coder-30B-A3B-Instruct', { timeout: 60_000 });
  await expect(server).toHaveAttribute('data-state', 'running', { timeout: 60_000 });
  await expect(qwen.getByTestId('local-fit')).toHaveText('Serving · GPU');
  await expect(tiny.getByTestId('local-serve')).toBeEnabled();

  // Load too keeps a second model loaded beside it (decision P111): the server, of one model, starts again as the router with
  // both; then Unload takes one off on the running router, and Load too loads it beside the other again, which keeps running
  // (decision P120: the router's own load and unload, which the log shows)
  await expect(tiny.getByTestId('local-load-too')).toHaveAttribute('title', /the server starts again with both/);
  await tiny.getByTestId('local-load-too').click();
  await expect(server.getByTestId('local-server-title')).toHaveText('Serving 2 models', { timeout: 60_000 });
  await expect(server).toHaveAttribute('data-state', 'running', { timeout: 60_000 });
  await expect(server.getByTestId('local-load')).toContainText('2 of 4 models loaded');
  await expect(tiny.getByTestId('local-fit')).toHaveText('Serving · GPU');
  await expect(tiny.getByTestId('local-unload')).toHaveAttribute('title', 'Unload it: the other models keep running');
  await tiny.getByTestId('local-unload').click();
  await expect(server.getByTestId('local-server-title')).toHaveText('Serving Qwen3-Coder-30B-A3B-Instruct', { timeout: 60_000 });
  await expect(server).toHaveAttribute('data-state', 'running', { timeout: 60_000 });
  await expect(server.getByTestId('local-log-lines')).toContainText('drydock: unloaded acme/tiny-GGUF:Q4_0 (the others stay loaded)');
  await expect(tiny.getByTestId('local-load-too')).toBeEnabled();
  await expect(tiny.getByTestId('local-load-too')).toHaveAttribute('title', 'Load it beside the models served: they keep running while it loads');
  await tiny.getByTestId('local-load-too').click();
  await expect(server.getByTestId('local-server-title')).toHaveText('Serving 2 models', { timeout: 60_000 });
  await expect(server.getByTestId('local-log-lines')).toContainText('drydock: loading acme/tiny-GGUF:Q4_0 beside the models loaded');
  await expect(server.getByTestId('local-server-note')).toHaveCount(0);

  // Stop: the server is stopped, nothing is served
  await server.getByTestId('local-server-stop').click();
  await expect(server).toHaveAttribute('data-state', 'stopped', { timeout: 60_000 });
  await expect(server.getByTestId('local-server-stop')).toHaveCount(0);
  await expect(server.getByTestId('local-server-title')).toHaveText('Nothing served');

  await expect(thisPc).toContainText('Stopped');

  // the server is registered as a provider; it is the this-pc row, never a row of its own under Providers
  const list = page.getByRole('navigation', { name: 'Models and providers' });
  await expect(list.getByText('Models on this PC', { exact: true })).toHaveCount(1);
  await expect(list.locator('[data-testid=models-row][data-name="Models on this PC"]')).toHaveCount(0);
  // the old address of Providers leads here
  await page.goto(at('/providers'));
  await expect(page).toHaveURL(/\/models(\?|$)/);
  await expect(sidebar(page).getByTestId('nav-side-models')).toHaveAttribute('aria-current', 'page');
  await expect(page.getByTestId('local-runtime')).toBeVisible();

  // the choices survive a reload
  await page.reload();
  await expect(page.getByTestId('local-runtime')).toHaveAttribute('data-installed', 'true');
  await expect(page.getByTestId('local-cache')).toHaveAttribute('data-value', 'q4_0');
  await expect(page.locator('[data-testid=local-model][data-id="acme/tiny-GGUF:Q4_0"]')).toBeVisible();

  // the command menu finds the page by its name
  await page.goto(at('/home'));
  await sidebar(page).getByRole('button', { name: 'Search and jump (Ctrl+K)' }).click();
  const menu = page.getByRole('dialog', { name: 'Command menu' });
  await expect(menu).toBeVisible();
  await menu.getByRole('combobox', { name: 'Command' }).fill('models on this pc');
  await page.keyboard.press('Enter');
  await expect(page).toHaveURL(/\/models(\?|$)/);
  await expect(page.getByRole('heading', { level: 1, name: 'Models' })).toBeVisible();
  await expect(page.getByRole('heading', { level: 2, name: 'Models on this PC' })).toBeVisible();
});
