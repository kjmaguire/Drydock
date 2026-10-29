package integrations

import (
	"context"
	"fmt"
	"net/url"
	"sort"
	"strings"
)

// Typed Notion REST calls beyond the listing (decision P114), written from the official SDK's endpoint definitions
// (@notionhq/client 5.27.0 src/api-endpoints: pages.retrieve GET /v1/pages/{id}, pages.update PATCH /v1/pages/{id},
// dataSources.retrieve GET /v1/data_sources/{id}, comments.list GET /v1/comments?block_id=) and its property value types. Same
// client, Notion-Version, credential, rate limit and failure handling as sync.

func (notionAdapter) getPage(ctx context.Context, a *API, id string) (map[string]any, error) {
	var m map[string]any
	if _, err := a.HTTP.json(ctx, "GET", "/v1/pages/"+url.PathEscape(id), nil, nil, &m); err != nil {
		return nil, err
	}
	if str(m["object"]) != "page" || str(m["id"]) == "" {
		return nil, schemaDrift(KindNotion, fmt.Errorf("pages/{id} is not a page"))
	}
	return m, nil
}

// Fetch reads one page and says whether it is in the assignment's scope: a row of its data source, or (an assignment without
// one) any page shared with the integration.
func (n notionAdapter) Fetch(ctx context.Context, a *API, cfg map[string]string, id string) (RawItem, bool, error) {
	m, err := n.getPage(ctx, a, id)
	if err != nil {
		var f *Failure
		if asFailure(err, &f) && f.Code == CodeNotFound {
			return RawItem{}, false, nil
		}
		return RawItem{}, false, err
	}
	raw := notionRaw(m)
	if ds := cfg["data_source"]; ds != "" && !sameNotionID(raw.Parent, ds) {
		return raw, false, nil
	}
	return raw, true, nil
}

// sameNotionID compares two Notion ids with or without their dashes.
func sameNotionID(a, b string) bool {
	return strings.EqualFold(strings.ReplaceAll(a, "-", ""), strings.ReplaceAll(b, "-", ""))
}

// notionFiles lists the files of a row's files properties, in a stable order: (property, index, file object).
func notionFiles(page map[string]any) []RawAttachment {
	props := asMap(page["properties"])
	names := make([]string, 0, len(props))
	for k := range props {
		names = append(names, k)
	}
	sort.Strings(names)
	out := []RawAttachment{}
	for _, name := range names {
		p := asMap(props[name])
		if str(p["type"]) != "files" {
			continue
		}
		for i, f := range asList(p["files"]) {
			fm := asMap(f)
			att := RawAttachment{ExternalID: fmt.Sprintf("%s#%d", name, i), Name: firstNonEmpty(str(fm["name"]), name), Source: "property"}
			switch str(fm["type"]) {
			case "file": // hosted by Notion: a signed address that expires after an hour, so it is read again before a download
				att.URL, att.Downloadable = str(asMap(fm["file"])["url"]), true
			case "external": // a link to another site: never fetched
				att.URL = str(asMap(fm["external"])["url"])
			}
			out = append(out, att)
		}
	}
	return out
}

// Attachments lists the files of a typed row's files properties and the file blocks of the page's body (decision P122).
func (n notionAdapter) Attachments(ctx context.Context, a *API, _ map[string]string, it Item) ([]RawAttachment, error) {
	m, err := n.getPage(ctx, a, it.ExternalID)
	if err != nil {
		return nil, err
	}
	out := notionFiles(m)
	blocks, err := n.pageFileBlocks(ctx, a, it.ExternalID)
	if err != nil {
		return nil, err
	}
	out = append(out, blocks...)
	for i := range out {
		if out[i].Downloadable {
			out[i].URL = "" // the signed address expires; it is not kept
		}
	}
	return out, nil
}

// FetchAttachment reads the page again for a fresh signed address of the file and downloads it, without the credential (the
// address is Notion's file storage, not its API).
func (n notionAdapter) FetchAttachment(ctx context.Context, a *API, _ map[string]string, it Item, att RawAttachment, max int64) ([]byte, string, error) {
	if id, ok := strings.CutPrefix(att.ExternalID, "block:"); ok {
		return n.fetchFileBlock(ctx, a, id, max)
	}
	m, err := n.getPage(ctx, a, it.ExternalID)
	if err != nil {
		return nil, "", err
	}
	for _, f := range notionFiles(m) {
		if f.ExternalID == att.ExternalID {
			if !f.Downloadable || f.URL == "" {
				return nil, "", &Failure{Code: CodeOther, Message: "This file is a link to another site, not a file Notion keeps."}
			}
			return a.fetchFile(ctx, f.URL, "", max)
		}
	}
	return nil, "", &Failure{Code: CodeNotFound, Message: "Notion no longer lists this file on the page."}
}

// FindComment looks for a comment with the marker's text on a page.
func (notionAdapter) FindComment(ctx context.Context, a *API, pageID, marker string, since int64) (string, bool, error) {
	cursor := ""
	for i := 0; i < 5; i++ {
		q := url.Values{"block_id": {pageID}, "page_size": {"100"}}
		if cursor != "" {
			q.Set("start_cursor", cursor)
		}
		var res map[string]any
		if _, err := a.HTTP.json(ctx, "GET", "/v1/comments", q, nil, &res); err != nil {
			return "", false, err
		}
		for _, c := range asList(res["results"]) {
			cm := asMap(c)
			if t := millis(cm["created_time"]); since > 0 && t > 0 && t < since {
				continue
			}
			if hashHex(normComment(richText(cm["rich_text"])))[:16] == marker {
				return str(cm["id"]), true, nil
			}
		}
		if hm, _ := res["has_more"].(bool); !hm {
			break
		}
		cursor = str(res["next_cursor"])
	}
	return "", false, nil
}

// MappingFields reads the data source's properties: their names, types and, for a status or select property, its options.
func (notionAdapter) MappingFields(ctx context.Context, a *API, cfg map[string]string, scope string) ([]MappingField, error) {
	ds := cfg["data_source"]
	if ds == "" {
		return nil, &Failure{Code: CodeNotFound, Message: "Choose a Notion data source in this project's settings first: typed rows and their statuses belong to one."}
	}
	var m map[string]any
	if _, err := a.HTTP.json(ctx, "GET", "/v1/data_sources/"+url.PathEscape(ds), nil, nil, &m); err != nil {
		return nil, err
	}
	props := asMap(m["properties"])
	if props == nil {
		return nil, schemaDrift(KindNotion, fmt.Errorf("data source without properties"))
	}
	names := make([]string, 0, len(props))
	for k := range props {
		names = append(names, k)
	}
	sort.Strings(names)
	out := []MappingField{}
	for _, name := range names {
		p := asMap(props[name])
		typ := str(p["type"])
		f := MappingField{Name: firstNonEmpty(str(p["name"]), name), Type: typ}
		if typ == "status" || typ == "select" {
			for _, o := range asList(asMap(p[typ])["options"]) {
				if n := str(asMap(o)["name"]); n != "" {
					f.Options = append(f.Options, n)
				}
			}
		}
		if scope == MapStatus {
			if typ != "status" && typ != "select" {
				continue
			}
			// the options are what a status mapping names
			for _, o := range f.Options {
				out = append(out, MappingField{Name: o, Type: typ, Property: name})
			}
			continue
		}
		out = append(out, f)
	}
	return out, nil
}

// buildSetStatus is the Notion write that sets a row's status (or select) property: PATCH /v1/pages/{id}.
func buildSetStatus(target string, p map[string]string) (OutCall, error) {
	st, prop := strings.TrimSpace(p["state"]), strings.TrimSpace(p["property"])
	typ := strings.TrimSpace(p["type"])
	if typ == "" {
		typ = "status"
	}
	if target == "" || st == "" || prop == "" {
		return OutCall{}, errf(ErrBadRequest, "set_status needs the page, the status property and the new status")
	}
	if typ != "status" && typ != "select" {
		return OutCall{}, errf(ErrBadRequest, "set_status sets a status or select property")
	}
	return OutCall{Endpoint: "PATCH /v1/pages/{id}", Args: map[string]any{"pageId": target, "properties": map[string]any{prop: map[string]any{typ: map[string]any{"name": st}}}},
		Preview: fmt.Sprintf("Set the %q property of Notion page %s to %q", prop, target, st)}, nil
}

func asFailure(err error, f **Failure) bool {
	ff, ok := err.(*Failure)
	if ok {
		*f = ff
	}
	return ok
}
