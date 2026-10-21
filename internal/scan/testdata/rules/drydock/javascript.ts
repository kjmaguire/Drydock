// TypeScript fixtures of internal/scan/rules/drydock/javascript.yaml (the rules cover both languages).
import { exec } from 'child_process';

export function run(input: string, el: HTMLElement): void {
  // ruleid: drydock.js.eval-of-non-literal
  eval(input);
  // ruleid: drydock.js.shell-command-built-from-data
  exec(`convert ${input} out.png`);
  // ruleid: drydock.js.inner-html-from-data
  el.innerHTML = input;
  // ok: drydock.js.inner-html-from-data
  el.textContent = input;
}
