// Models on this PC end to end against the real Go core (decision P65). The harness (cmd/drydock-e2e) gives the core PROTOCOL
// FIXTURES: GitHub's releases API for llama.cpp's repository (builds b11600 and b11669, the newer with a Vulkan and a CPU
// archive holding a placeholder llama-server) and the Hugging Face Hub with small GGUF repositories; the "server" started is
// the harness's own stand-in (internal/localllm/llamatest), which loads its model for a second. Nothing reaches GitHub or
// Hugging Face; no llama.cpp runs. The core reports one CUDA device with 14 GB free and 20 GB of RAM available.
import { expect, test } from '@playwright/test';
import { appUrl, workspaceNav } from './helpers';

test('Models on this PC: install llama.cpp, find and download a model, serve it as a provider, stop', async ({ page }) => {
  test.setTimeout(240_000);
  await page.goto(appUrl());
  await workspaceNav(page, 'nav-providers');
  const section = page.getByTestId('local-models');
  await expect(section).toBeVisible();
  await expect(section).toContainText('Run open models with llama.cpp on this computer.');
  await expect(section.getByTestId('local-runtime')).toContainText('Drydock downloads and runs llama.cpp itself');

  // not installed: the backend and the source to choose, then Install
  const runtime = section.getByTestId('local-runtime');
  await expect(runtime).toHaveAttribute('data-installed', 'false');
  await expect(section.getByTestId('local-source')).toHaveValue('official');
  await section.getByTestId('local-backend').selectOption('vulkan');
  await section.getByTestId('local-install').click();
  // the install runs in the background; the page follows it through local.changed until the newest build is in
  await expect(runtime).toHaveAttribute('data-installed', 'true', { timeout: 60_000 });
  await expect(runtime).toContainText('llama.cpp b11669 · Vulkan · llama.cpp (official weekly build)');
  await expect(runtime.locator('[data-testid=local-device][data-id=CUDA0]')).toBeVisible({ timeout: 60_000 });
  await expect(runtime.getByTestId('local-job')).toHaveAttribute('data-state', 'done');
  await expect(section.getByTestId('local-memory')).toContainText('RAM: 20.0 GB free of 32.0 GB');

  // suggestions come from the Hub fixture; Close suggestions shrinks the page back and Show suggestions returns
  await section.getByTestId('local-suggested').click();
  await expect(section.locator('[data-testid=local-repo][data-id="ggml-org/gpt-oss-20b-GGUF"]')).toBeVisible();
  await expect(section.locator('[data-testid=local-repo][data-id="ggml-org/Qwen3-Coder-30B-A3B-Instruct-GGUF"]')).toBeVisible();
  await expect(section.getByTestId('local-suggested')).toHaveCount(0);
  await section.getByTestId('local-repos-close').click();
  await expect(section.getByTestId('local-repo')).toHaveCount(0);
  await expect(section.getByTestId('local-repos-close')).toHaveCount(0);
  await expect(section.getByTestId('local-suggested')).toBeVisible();

  // a search finds the tiny repository; its one variant downloads and the model row appears once the file is checked
  await section.getByTestId('local-search-input').fill('tiny');
  await section.getByTestId('local-search').getByRole('button', { name: 'Search' }).click();
  const tiny = section.locator('[data-testid=local-repo][data-id="acme/tiny-GGUF"]');
  await expect(tiny).toBeVisible();
  await expect(section.getByTestId('local-repo')).toHaveCount(1);
  await tiny.click();
  const variant = section.locator('[data-testid=local-variant][data-name="Q4_0"]');
  await expect(variant).toBeVisible();
  await expect(variant).toContainText('2 MB');
  await expect(variant).toContainText('Fits the GPU');
  await variant.getByTestId('local-download').click();
  const model = section.locator('[data-testid=local-model][data-id="acme/tiny-GGUF:Q4_0"]');
  await expect(model).toBeVisible({ timeout: 60_000 });
  await expect(model).toContainText('Fits the GPU');

  // Serve: the stand-in loads for a second, then the server is running at its address and is a provider tile below
  const server = section.getByTestId('local-server');
  await expect(server).toHaveAttribute('data-state', 'stopped');
  await model.getByTestId('local-serve').click();
  await expect(server).toHaveAttribute('data-state', 'running', { timeout: 60_000 });
  await expect(server.getByTestId('local-server-state')).toHaveText('running');
  await expect(server.getByTestId('local-server-url')).toHaveText('http://127.0.0.1:9933/v1');
  await expect(server).toContainText('acme/tiny-GGUF:Q4_0');
  await expect(server).toContainText("Registered as the provider 'Models on this PC (llama.cpp)' below.");
  await expect(model).toContainText('serving');
  const tile = page.locator('[data-testid=provider-tile][data-name="Models on this PC (llama.cpp)"]');
  await expect(tile).toBeVisible();
  await expect(tile).toContainText('127.0.0.1:9933');
  // the log shows the stand-in's lines
  await server.getByTestId('local-log').click();
  await expect(server.getByTestId('local-log-lines')).toBeVisible();

  // a setting changed while the server runs asks for a restart
  await section.getByTestId('local-cache').selectOption('q4_0');
  await expect(section.getByTestId('local-restart')).toContainText('Restart the server to apply');

  // Stop: the server is stopped, the provider row stays (and is now down)
  await server.getByTestId('local-server-stop').click();
  await expect(server).toHaveAttribute('data-state', 'stopped', { timeout: 60_000 });
  await expect(server.getByTestId('local-server-stop')).toHaveCount(0);
  await expect(tile).toBeVisible();

  // the choices survive a reload
  await page.reload();
  await expect(page.getByTestId('local-runtime')).toHaveAttribute('data-installed', 'true');
  await expect(page.getByTestId('local-cache')).toHaveValue('q4_0');
  await expect(page.locator('[data-testid=local-model][data-id="acme/tiny-GGUF:Q4_0"]')).toBeVisible();
});
