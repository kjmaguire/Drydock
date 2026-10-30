package integrations

import (
	"context"
	"fmt"
	"net/url"
	"path"
	"strings"
)

// Files inside a Notion page's body (decision P122): the page's file, image, pdf, video and audio blocks, read from GET
// /v1/blocks/{id}/children (start_cursor pages of up to 100, the children of a block that has_children read too, bounded) and,
// for a download, GET /v1/blocks/{id} again for a fresh signed address (a Notion-hosted file's address expires after an hour).
// Written from the official SDK's endpoint definitions (@notionhq/client 5.27.0 src/api-endpoints: blocks.children.list,
// blocks.retrieve) and its block types (a file object of type "file" {url, expiry_time} kept by Notion, or "external" {url}, a
// link; "caption" rich text and, on file blocks, "name"). A Notion-hosted file is downloaded without the credential (the address
// is Notion's file storage, not its API); an external one is a link and never fetched.

// notionFileBlocks are the block types that carry a file.
var notionFileBlocks = map[string]bool{"file": true, "image": true, "pdf": true, "video": true, "audio": true}

const (
	notionBlockDepth = 3  // a file inside a toggle inside a column is still found
	notionBlockCalls = 25 // children listings per page body, at most
)

// notionBlockFile is the attachment a file block carries (ok false for any other block).
func notionBlockFile(b map[string]any) (RawAttachment, bool) {
	typ := str(b["type"])
	id := str(b["id"])
	if !notionFileBlocks[typ] || id == "" {
		return RawAttachment{}, false
	}
	fo := asMap(b[typ])
	att := RawAttachment{ExternalID: "block:" + id, Source: "page body"}
	switch str(fo["type"]) {
	case "file":
		att.URL, att.Downloadable = str(asMap(fo["file"])["url"]), true
	case "external":
		att.URL = str(asMap(fo["external"])["url"])
	default: // a file_upload still being attached, or a type this build does not know: listed, never fetched
	}
	name := str(fo["name"])
	if name == "" {
		name = strings.TrimSpace(richText(fo["caption"]))
	}
	if name == "" && att.URL != "" {
		if u, err := url.Parse(att.URL); err == nil {
			name = path.Base(u.Path)
		}
	}
	if name == "" || name == "." || name == "/" {
		name = typ
	}
	att.Name = name
	return att, true
}

// pageFileBlocks walks a page's blocks and returns its file blocks in reading order.
func (notionAdapter) pageFileBlocks(ctx context.Context, a *API, pageID string) ([]RawAttachment, error) {
	out := []RawAttachment{}
	calls := 0
	var walk func(id string, depth int) error
	walk = func(id string, depth int) error {
		cursor := ""
		for {
			if calls >= notionBlockCalls || len(out) >= 100 {
				return nil
			}
			calls++
			q := url.Values{"page_size": {"100"}}
			if cursor != "" {
				q.Set("start_cursor", cursor)
			}
			var res map[string]any
			if _, err := a.HTTP.json(ctx, "GET", "/v1/blocks/"+url.PathEscape(id)+"/children", q, nil, &res); err != nil {
				return err
			}
			if str(res["object"]) != "list" {
				return schemaDrift(KindNotion, fmt.Errorf("blocks/{id}/children is not a list"))
			}
			for _, e := range asList(res["results"]) {
				b := asMap(e)
				if att, ok := notionBlockFile(b); ok {
					out = append(out, att)
					continue
				}
				// a child page is a page of its own, not this one's body
				if hc, _ := b["has_children"].(bool); hc && depth+1 < notionBlockDepth && str(b["type"]) != "child_page" && str(b["type"]) != "child_database" {
					if err := walk(str(b["id"]), depth+1); err != nil {
						return err
					}
				}
			}
			if hm, _ := res["has_more"].(bool); !hm {
				return nil
			}
			if cursor = str(res["next_cursor"]); cursor == "" {
				return nil
			}
		}
	}
	return out, walk(pageID, 0)
}

// fetchFileBlock reads one file block again for its current address and downloads a Notion-hosted file.
func (notionAdapter) fetchFileBlock(ctx context.Context, a *API, blockID string, max int64) ([]byte, string, error) {
	var b map[string]any
	if _, err := a.HTTP.json(ctx, "GET", "/v1/blocks/"+url.PathEscape(blockID), nil, nil, &b); err != nil {
		var f *Failure
		if asFailure(err, &f) && f.Code == CodeNotFound {
			return nil, "", &Failure{Code: CodeNotFound, Message: "Notion no longer has this file in the page."}
		}
		return nil, "", err
	}
	att, ok := notionBlockFile(b)
	if !ok {
		return nil, "", &Failure{Code: CodeNotFound, Message: "Notion no longer has this file in the page."}
	}
	if !att.Downloadable || att.URL == "" {
		return nil, "", &Failure{Code: CodeOther, Message: "This file is a link to another site, not a file Notion keeps."}
	}
	return a.fetchFile(ctx, att.URL, "", max)
}
