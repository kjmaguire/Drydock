import type { Topic } from '../model.ts';

export const SAFETY: readonly Topic[] = [
  {
    id: 'privacy',
    section: 'safety',
    title: 'What stays on this computer',
    summary: 'Where your data lives, and the few things that leave.',
    keywords: ['privacy', 'upload', 'telemetry', 'secrets', 'credential', 'osv.dev', 'network', 'license'],
    body: [
      { p: 'Drydock keeps its database, your projects’ worktrees and its logs on this computer. **It never uploads your source code.** Secrets are kept out of logs, events and the database.' },
      { h: 'What leaves' },
      {
        ul: [
          '**Agent CLIs and model providers** receive what an agent sends them, the way they do outside Drydock. Providers are the ones you added; agent CLIs use their own sign-in.',
          '**osv-scanner** sends package names and versions to osv.dev during a security scan. Nothing else leaves in a scan.',
          '**Downloads** Drydock makes: the newest release of a scan tool from GitHub (checked by its SHA-256), llama.cpp and models if you ask for them.',
          '**GitHub** receives what a pull request contains, through your `gh` sign-in.',
        ],
      },
      { h: 'What does not leave' },
      {
        ul: [
          '**Telemetry** keeps usage counts in a local database, with only allowlisted identifier-shaped attributes (no prompts, code, tool output, file contents or secrets), so you can see and export them. Nothing is sent anywhere. It is off unless you turn it on.',
          '**Licenses** are checked offline on this computer; Drydock never contacts a license server. The check protects against casual sharing and gives honest expiry behaviour. It is not tamper-proof.',
          'A **support bundle** is written locally; you decide whether to send it.',
          'API keys and sign-in tokens you give Drydock are kept in the **Windows Credential Manager** and never shown again.',
        ],
      },
    ],
    related: ['untrusted-text', 'scan', 'support-bundle'],
  },
  {
    id: 'untrusted-text',
    section: 'safety',
    title: 'Why outside text is shown inert',
    summary: 'Text from agents, GitHub, services and packs is data, never an instruction.',
    keywords: ['untrusted', 'injection', 'prompt injection', 'quarantine', 'outside text', 'redacted', 'inert'],
    body: [
      { p: 'Anything that comes from outside your own hands (agent output, GitHub issues and comments, items from a service, the text of a requirements file or an expert pack, a planner’s proposal) is **outside text**.' },
      {
        ul: [
          'It is shown as inert text. Raw HTML is not rendered, remote images are not loaded and links show where they lead.',
          'Secrets in it are redacted before it is stored.',
          'Passages that look like instructions to an agent are quarantined, and items from services are scored for injection risk. A rule never creates a task from an item with a risk of 3 or more.',
          'Nothing in outside text can grant a permission, change a setting or make a decision. Only you can.',
          'A tool description that reads like instructions to the agent is shown as data and is not granted to agents.',
        ],
      },
    ],
    related: ['privacy', 'integrations', 'skills'],
  },
  {
    id: 'troubleshooting',
    section: 'trouble',
    title: 'When something does not work',
    summary: 'The usual causes, and the page that fixes each.',
    keywords: ['error', 'not working', 'gh not installed', 'cli not found', 'no ci', 'read-only', 'budget', 'git config', 'help'],
    body: [
      {
        table: {
          head: ['You see', 'Look at'],
          rows: [
            ['The GitHub CLI is not installed', 'Install `gh` from [cli.github.com](https://cli.github.com), run `gh auth login`, then **Check again** on [GitHub](/github).'],
            ['An agent CLI is not listed, or is not usable', '[Connections](/connections): install and sign in to the CLI, accept every notice on it, then **Test connection…**. See [Connect an agent CLI](guide:connect-cli).'],
            ['A task waits and nothing seems wrong', 'Open **Needs you** (the bell). A limit, an approval or a question may be waiting for you.'],
            ['Agents stopped answering', 'A budget reached 100%. Raise it in [Budgets](/budgets). See [Budgets and cost](guide:budgets).'],
            ['No CI reported on a pull request', 'Your decision in Needs you: check again, merge without CI this time, or stop. A repository without CI can be declared so in [Project settings](/project).'],
            ['“The repository’s git configuration changed”', 'Drydock does not run git there until you review it: a hook, filter or command planted there would run in Drydock’s own git calls. Trust it only if you made or expect the change.'],
            ['Drydock is read-only', 'The license is not valid. Backups, verify and restore, and importing a license still work. See [License](/license).'],
            ['The Code scan is skipped', 'Opengrep has no rules. Choose a folder of rules on [Security scan](/security-scan). See [Scan a repository](guide:scan).'],
            ['Something else', 'Make a [support bundle](/support), look through its file list, and send it with your report.'],
          ],
        },
      },
    ],
    related: ['support-bundle', 'connect-cli', 'github'],
  },
];
