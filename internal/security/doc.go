// Package security implements the Drydock security design (docs/security.md): action descriptors and single-use
// approvals bound to an action hash, the path guard, the command analyzer and its decision table, secret scanning and
// exact-value redaction, the hash-chained audit log, and the agent environment scrub.
//
// Enforcement strength is stated honestly in the design: everything here is hard only for what Drydock itself
// executes or mediates. Nothing in this package is a sandbox.
package security
