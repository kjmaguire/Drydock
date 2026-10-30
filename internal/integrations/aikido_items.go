package integrations

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
)

// Attachments of an Aikido finding (decision P122). Aikido's public API, as far as its documentation could be read from search
// extracts (apidocs.aikido.dev was not reachable from the build machine), has no files on a finding: the export gives each
// finding its page in Aikido and, depending on the finding, reference links. Those are what Drydock lists, as links that are
// opened in the browser and never fetched; nothing of a finding is downloaded. The export is read again (filtered to the
// assignment's repository, as sync reads it) and the finding found by its id. Unverified against the live API.

// aikidoFindingLinks are the links of one exported finding: its Aikido page and its references, each once.
func aikidoFindingLinks(m map[string]any) []RawAttachment {
	out := []RawAttachment{}
	seen := map[string]bool{}
	add := func(name, raw string) {
		u := validHTTPURL(raw)
		if u == "" || seen[u] || len(out) >= 50 {
			return
		}
		seen[u] = true
		host := ""
		if pu, err := url.Parse(u); err == nil {
			host = pu.Hostname()
		}
		out = append(out, RawAttachment{ExternalID: "link:" + hashHex(u)[:24], Name: firstNonEmpty(name, host, u), URL: u, Source: "finding"})
	}
	add("The finding in Aikido", str(pick(m, "url", "link")))
	for _, k := range []string{"references", "reference_urls", "links"} {
		for _, r := range asList(m[k]) {
			if rm := asMap(r); rm != nil {
				add(firstNonEmpty(str(rm["title"]), str(rm["name"])), firstNonEmpty(str(rm["url"]), str(rm["href"])))
			} else {
				add("", str(r))
			}
		}
	}
	return out
}

// Attachments lists the links of a finding.
func (aikidoAdapter) Attachments(ctx context.Context, a *API, cfg map[string]string, it Item) ([]RawAttachment, error) {
	v := url.Values{"format": {"json"}}
	if r := cfg["repo"]; r != "" {
		if _, err := strconv.Atoi(r); err == nil {
			v.Set("filter_code_repo_id", r)
		} else {
			v.Set("filter_code_repo_name", r)
		}
	}
	var raw any
	if _, err := a.HTTP.json(ctx, "GET", "/api/public/v1/issues/export", v, nil, &raw); err != nil {
		return nil, err
	}
	list, ok := raw.([]any)
	if !ok {
		if m := asMap(raw); m != nil {
			list, ok = pick(m, "issues", "data").([]any)
		}
	}
	if !ok {
		return nil, schemaDrift(KindAikido, fmt.Errorf("the export is not a list of issues"))
	}
	for _, e := range list {
		m := asMap(e)
		if str(m["id"]) == it.ExternalID {
			return aikidoFindingLinks(m), nil
		}
	}
	return nil, &Failure{Code: CodeNotFound, Message: "Aikido no longer lists this finding."}
}

// FetchAttachment refuses: a finding has links only.
func (aikidoAdapter) FetchAttachment(context.Context, *API, map[string]string, Item, RawAttachment, int64) ([]byte, string, error) {
	return nil, "", &Failure{Code: CodeOther, Message: "Aikido keeps no files on a finding; this is a link to open in the browser."}
}
