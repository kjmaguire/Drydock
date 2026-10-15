import { Registry, slugifyTitle } from './util.js';

function TitleBadge({ title }) {
  return <b>{slugifyTitle(title)}</b>;
}

export function startApp() {
  const r = new Registry();
  r.register('home');
  return <TitleBadge title="Home" />;
}
