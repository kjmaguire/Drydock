# The Code engine's built-in rules (decision P77)

Drydock ships these Opengrep rule sets inside the app and runs them on every scan; a person may add a folder of their own
rules on top. Each set is here with its licence. Nothing is downloaded at run time.

| Set | Folder | Rules | Languages | Source | Licence |
| --- | --- | --- | --- | --- | --- |
| Drydock's own | `drydock/` | 34 | JavaScript, TypeScript, Python, Go, Java, C# | written for Drydock; fixtures in `../testdata/rules/drydock` | the repository's `LICENSE` |
| dgryski/semgrep-go | `third_party/dgryski-semgrep-go/` | 66 (in 43 files) | Go (bug patterns) | https://github.com/dgryski/semgrep-go at `db0227c03f4b3c4e71d900188d51db4c81d66932` (2024-11-17) | MIT (`LICENSE` beside it) |
| 0xdea/semgrep-rules, C | `third_party/0xdea-c/` | 49 | C, C++ | https://github.com/0xdea/semgrep-rules `rules/c` at `e0121ac19d9fa259a44f3a24d3e42c95719b17c7` (2026-10-18); its `noisy` rules are left out | MIT (`LICENSE` beside it) |

Not shipped, because their terms do not allow it in a product that is sold, or bind it to terms the owner has not chosen:

- `opengrep/opengrep-rules`: LGPL-2.1 with the Commons Clause, which forbids selling a product whose value comes from the rules.
- `semgrep/semgrep-rules`: the Semgrep Rules License v1.0, which restricts use in a product offered to others.
- `trailofbits/semgrep-rules`: AGPL-3.0; it could ship (unchanged, with its source), but the owner decides whether to take on AGPL
  terms for a part of the product.

To update a third-party set: copy the new rule files over the old, record the commit here and in `docs/software-notes.json`, and
run `go test ./internal/scan -run Builtin` (and `semgrep --test` or `opengrep --test` on the fixtures where one is installed).
