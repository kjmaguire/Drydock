import { useState, type FormEvent } from 'react';
import type { MergeStrategy, Project, ProjectMode } from '../api/types';
import { useClient } from '../state/context';
import { ErrorNote, Page } from './common';

export function ProjectCreate({ first, onCreated }: { first: boolean; onCreated: (p: Project) => void }) {
  const client = useClient();
  const [name, setName] = useState('');
  const [repoPath, setRepoPath] = useState('');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<Error | undefined>();
  const submit = async (e: FormEvent) => {
    e.preventDefault();
    setBusy(true);
    setError(undefined);
    try {
      onCreated(await client.projectCreate(name.trim(), repoPath.trim()));
    } catch (err) {
      setError(err as Error); // the core's own message, e.g. "... is not a usable Git repository"
    } finally {
      setBusy(false);
    }
  };
  return (
    <Page title={first ? 'Welcome to Drydock' : 'New project'}>
      {first ? (
        <p>
          Drydock runs a team of real coding agents on a local Git repository. Pick a repository that already has a commit on a branch. Drydock works in separate
          worktrees and merges only after you approve.
        </p>
      ) : null}
      <form className="form" onSubmit={submit}>
        <ErrorNote error={error} what="Project not created" />
        <label>
          Repository path
          <input value={repoPath} onChange={(e) => setRepoPath(e.target.value)} placeholder="C:\\src\\my-repo" required spellCheck={false} />
        </label>
        <label>
          Project name (optional)
          <input value={name} onChange={(e) => setName(e.target.value)} placeholder="defaults to the folder name" />
        </label>
        <div className="form-actions">
          <button type="submit" className="primary" disabled={busy || !repoPath.trim()}>
            Create project
          </button>
        </div>
      </form>
    </Page>
  );
}

export function ProjectSettings({ project, onChanged }: { project: Project; onChanged: () => void }) {
  const client = useClient();
  const [mode, setMode] = useState<string>(project.mode);
  const [strategy, setStrategy] = useState<string>(project.mergeStrategy);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<Error | undefined>();
  const [saved, setSaved] = useState(false);
  const dirty = mode !== project.mode || strategy !== project.mergeStrategy;
  const submit = async (e: FormEvent) => {
    e.preventDefault();
    setBusy(true);
    setError(undefined);
    setSaved(false);
    try {
      await client.projectUpdate(project.id, mode as ProjectMode, strategy as MergeStrategy);
      setSaved(true);
      onChanged();
    } catch (err) {
      setError(err as Error);
    } finally {
      setBusy(false);
    }
  };
  return (
    <Page title="Project">
      <dl className="kv">
        <dt>Name</dt>
        <dd>{project.name}</dd>
        <dt>Repository</dt>
        <dd>
          <code>{project.repoPath}</code>
        </dd>
        <dt>Default branch</dt>
        <dd>
          <code>{project.defaultBranch}</code>
        </dd>
      </dl>
      <h2>Delivery</h2>
      <form className="form" onSubmit={submit}>
        <ErrorNote error={error} what="Not saved" />
        <label>
          Mode
          <select value={mode} onChange={(e) => setMode(e.target.value)}>
            <option value="local_merge">Merge locally after my approval</option>
            <option value="pr_flow">Open a GitHub pull request (needs a github.com origin and gh)</option>
          </select>
        </label>
        <label>
          Merge strategy
          <select value={strategy} onChange={(e) => setStrategy(e.target.value)}>
            <option value="merge">Merge commit</option>
            <option value="squash">Squash</option>
            <option value="rebase">Rebase</option>
          </select>
        </label>
        <p className="muted">Changing this affects tasks started afterwards. The core refuses pull request mode when the repository cannot do pull requests.</p>
        <div className="form-actions">
          <button type="submit" className="primary" disabled={busy || !dirty}>
            Save
          </button>
          {saved ? <span role="status">Saved.</span> : null}
        </div>
      </form>
    </Page>
  );
}
