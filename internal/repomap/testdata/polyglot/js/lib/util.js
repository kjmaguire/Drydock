export function slugifyTitle(title) {
  return title.toLowerCase().replace(/\s+/g, '-');
}

export class Registry {
  register(name) {
    this.names = [...(this.names ?? []), slugifyTitle(name)];
  }
}
