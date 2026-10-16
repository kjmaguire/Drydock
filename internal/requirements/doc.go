// Package requirements holds a project's requirements documents (docs/expert-packs.md section 10): Markdown, plain text or
// pasted PDF text, uploaded by a human or synced from an integration. Each document is chunked along its headings into
// FTS5 and cited like an Expert Pack ("REQ:<doc>@<version>#<heading path>"); every content change is a new version; the
// spec gate links Given/When/Then acceptance criteria to tasks, and the package reports requirement -> task -> test evidence
// traceability. It also owns milestones (brief section 21, Project section).
//
// Requirements are always private: they never reach a trainable provider or a free CONSULT route. Their text is untrusted
// data like any external content: secrets are redacted before storage, chunks are scanned for injection patterns, and
// retrieved text can never change permissions.
package requirements
