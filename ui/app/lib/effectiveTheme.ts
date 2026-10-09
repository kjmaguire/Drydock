/** Whether the page is showing the dark theme right now (the Settings override, else the system preference). */
export function isDarkTheme(): boolean {
  const t = document.documentElement.getAttribute('data-theme');
  if (t === 'dark') return true;
  if (t === 'light') return false;
  return typeof window.matchMedia === 'function' && window.matchMedia('(prefers-color-scheme: dark)').matches;
}

/** Calls fn whenever the effective theme changes; returns the unsubscribe. */
export function onThemeChange(fn: (dark: boolean) => void): () => void {
  const mo = new MutationObserver(() => fn(isDarkTheme()));
  mo.observe(document.documentElement, { attributes: true, attributeFilter: ['data-theme'] });
  const mq = typeof window.matchMedia === 'function' ? window.matchMedia('(prefers-color-scheme: dark)') : undefined;
  const onMq = () => fn(isDarkTheme());
  mq?.addEventListener?.('change', onMq);
  return () => {
    mo.disconnect();
    mq?.removeEventListener?.('change', onMq);
  };
}
