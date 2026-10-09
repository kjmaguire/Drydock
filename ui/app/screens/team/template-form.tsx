import type { Handle } from 'remix/component';
import type { AgentTemplate } from '../../api/libraryClient.ts';
import type { McpLibraryServer } from '../../api/mcpClient.ts';
import type { AdapterInfo } from '../../api/types.ts';

// PLACEHOLDER during the Remix rebuild: the team slice replaces it (the Library imports it with these props).
export interface TemplateFormProps {
  initial?: AgentTemplate;
  adapters: readonly AdapterInfo[];
  servers: readonly McpLibraryServer[];
  onDone: (saved: AgentTemplate) => void;
  onCancel: () => void;
}

export function TemplateForm(_handle: Handle<TemplateFormProps>) {
  return () => <p>The template form is being rebuilt.</p>;
}
