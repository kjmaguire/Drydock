// Typed client for room attachments (internal/core/ext_attachments.go).

import type { Transport } from './transport.ts';
import type { Posted } from './types.ts';
import { decodeBase64 } from './dockClient.ts';

export interface AttachmentInfo {
  id: string;
  name: string;
  mime: string;
  size: number;
  /** the text an agent was shown (short text files only, after the secret scan) */
  text?: string;
}

export interface AttachmentData {
  id: string;
  name: string;
  mime: string;
  size: number;
  data: Uint8Array;
}

export interface AttachClient {
  roomAttach(roomId: string, body: string, files: { name: string; data: Uint8Array }[], threadRoot?: string): Promise<Posted>;
  roomAttachment(id: string): Promise<AttachmentData>;
}

export function encodeBase64(b: Uint8Array): string {
  let s = '';
  const CH = 0x8000;
  for (let i = 0; i < b.length; i += CH) s += String.fromCharCode(...b.subarray(i, i + CH));
  return btoa(s);
}

export function createAttachClient(t: Transport): AttachClient {
  return {
    roomAttach: (roomId, body, files, threadRoot = '') =>
      t.call('room.attach', { roomId, body, threadRoot, files: files.map((f) => ({ name: f.name, data: encodeBase64(f.data) })) }),
    roomAttachment: async (id) => {
      const r = await t.call<Omit<AttachmentData, 'data'> & { data: string | null }>('room.attachment', { id });
      return { ...r, data: decodeBase64(r.data) };
    },
  };
}
