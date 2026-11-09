# Where this skill comes from

`SKILL.md` is the `remix` skill of the Remix repository, copied unchanged from
https://github.com/remix-run/remix/tree/092c4d60d978b9be751ea3a3138dd26ad99b1e5e/.agents/skills/remix
(commit 092c4d60d978b9be751ea3a3138dd26ad99b1e5e, 2026-11-09). `LICENSE` is that repository's licence (MIT, Copyright (c) 2025 Shopify Inc.).

It is for building Remix 3 apps, which Drydock's UI is (`ui/`, the `remix` package). It points agents at the installed
documentation: in this repository that is `ui/node_modules/remix/INDEX.md` and `ui/node_modules/remix/guides`, at the
version `ui/package.json` pins.

The repository's other 18 skills (make-pr, review-pr, write-tests, typescript-expert and the rest) are written for people
working on the Remix framework itself and assume its monorepo, so they are not installed here. To update this one, copy
`.agents/skills/remix/SKILL.md` from the newest commit and change the commit above.
