/** The person is looking at this window: it is visible and has focus. Messages are marked read only then (rooms, chats: D8). */
export function windowAttended(doc: Document = document): boolean {
  return doc.visibilityState === 'visible' && doc.hasFocus();
}
