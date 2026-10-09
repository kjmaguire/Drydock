# One-off helper used during the Remix migration: add .ts/.tsx extensions to relative imports (Node ESM resolution).
import os, re, sys
root = sys.argv[1]
pat = re.compile(r"""((?:from|import)\s*\(?\s*['"])(\.{1,2}/[^'"]+?)(['"])""")
for d, _, fs in os.walk(root):
    for f in fs:
        if not f.endswith(('.ts', '.tsx')): continue
        p = os.path.join(d, f); s = open(p).read()
        def fix(m):
            spec = m.group(2)
            if re.search(r'\.(tsx?|css|js|json|svg|woff2?)(\?.*)?$', spec): return m.group(0)
            base = os.path.normpath(os.path.join(d, spec))
            for ext in ('.ts', '.tsx', '/index.ts', '/index.tsx'):
                if os.path.exists(base + ext): return m.group(1) + spec + ext + m.group(3)
            return m.group(0)
        n = pat.sub(fix, s)
        if n != s: open(p, 'w').write(n)
