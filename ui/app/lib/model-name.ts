// A model id in the words people use for it, for the chat chips and the routing diagram (decisions P107, P108).

/**
 * A model id as a person reads it: "claude-opus-5-5" and "opus-5-5-20261001" are "Opus 5.5", the alias "sonnet" is "Sonnet";
 * an id of another vendor stays as it is.
 */
export function modelName(id: string): string {
  const m = id.trim().replace(/\[[^\]]*\]$/, '');
  const x = /^(?:claude-)?(opus|sonnet|haiku|fable)(?:-(\d+)(?:-(\d{1,2}))?)?(?:-\d{8})?$/i.exec(m);
  if (!x) return m;
  const family = x[1]!.charAt(0).toUpperCase() + x[1]!.slice(1).toLowerCase();
  const version = x[2] ? (x[3] ? `${x[2]}.${x[3]}` : x[2]) : '';
  return version ? `${family} ${version}` : family;
}
