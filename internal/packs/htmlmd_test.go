package packs

import (
	"strings"
	"testing"
)

func TestHTMLToMarkdown(t *testing.T) {
	src := `<html><head><title>Workers &amp; Pages</title><script>evil()</script></head><body>
<nav><a href="/">Home</a></nav>
<main><h1>Workers</h1><p>Use <code>wrangler</code> to deploy.</p><ul><li>one</li><li>two &lt;3</li></ul>
<pre><code>npx wrangler deploy
  --dry-run</code></pre>
<table><tr><th>a</th><th>b</th></tr><tr><td>1</td><td>2</td></tr></table>
<!-- hidden comment --><style>.x{}</style></main><footer>footer text</footer></body></html>`
	title, md := HTMLToMarkdown(src)
	if title != "Workers & Pages" {
		t.Fatalf("title %q", title)
	}
	for _, want := range []string{"# Workers", "Use `wrangler` to deploy.", "- one", "- two <3", "```\nnpx wrangler deploy\n  --dry-run\n```", "| a | b |", "| 1 | 2 |"} {
		if !strings.Contains(md, want) {
			t.Errorf("missing %q in:\n%s", want, md)
		}
	}
	for _, bad := range []string{"evil", "Home", "footer text", "hidden comment", ".x{}"} {
		if strings.Contains(md, bad) {
			t.Errorf("chrome leaked: %q in:\n%s", bad, md)
		}
	}
}
