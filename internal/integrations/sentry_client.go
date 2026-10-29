package integrations

import (
	"context"
	"errors"
	"net/url"
)

// Typed Sentry web API calls beyond the listing (decision P114), from getsentry/sentry-api-schema openapi-derefed.json: one issue
// (GET /api/0/organizations/{org}/issues/{id}/, a webhook names it), the latest event of an issue, and that event's attachments
// (GET /api/0/projects/{org}/{project}/events/{event_id}/attachments/, and .../{attachment_id}/?download=1; Sentry's documentation
// says both need the organization's event-attachments feature). Same client, credential, rate limit and failure handling as sync.

// Fetch reads one issue and says whether it is in the assignment's scope (its project). A resolved or ignored issue is read as
// closed, as sync reads it.
func (sentryAdapter) Fetch(ctx context.Context, a *API, cfg map[string]string, id string) (RawItem, bool, error) {
	org := cfg["organization"]
	if org == "" {
		return RawItem{}, false, nil
	}
	var m map[string]any
	if _, err := a.HTTP.json(ctx, "GET", "/api/0/organizations/"+url.PathEscape(org)+"/issues/"+url.PathEscape(id)+"/", nil, nil, &m); err != nil {
		var f *Failure
		if asFailure(err, &f) && f.Code == CodeNotFound {
			return RawItem{}, false, nil
		}
		return RawItem{}, false, err
	}
	if str(m["id"]) == "" {
		return RawItem{}, false, schemaDrift(KindSentry, errors.New("the issue has no id"))
	}
	raw := sentryRaw(m)
	if p := cfg["project"]; p != "" {
		if pid, _ := raw.Meta["projectId"].(string); pid != p {
			return raw, false, nil
		}
	}
	return raw, true, nil
}

// sentryLatest reads the id and project of an issue's latest event.
func sentryLatest(ctx context.Context, a *API, org, issueID string) (eventID, projectID string, err error) {
	var ev map[string]any
	if _, err := a.HTTP.json(ctx, "GET", "/api/0/organizations/"+url.PathEscape(org)+"/issues/"+url.PathEscape(issueID)+"/events/latest/", nil, nil, &ev); err != nil {
		return "", "", err
	}
	return firstNonEmpty(str(ev["eventID"]), str(ev["id"])), str(ev["projectID"]), nil
}

// Attachments lists the attachments of the issue's latest event (minidumps, logs, screenshots the SDK sent with it).
func (sentryAdapter) Attachments(ctx context.Context, a *API, cfg map[string]string, it Item) ([]RawAttachment, error) {
	org := cfg["organization"]
	if org == "" {
		return nil, &Failure{Code: CodeNotFound, Message: "Choose the Sentry organization in the project's Sentry settings."}
	}
	ev, proj, err := sentryLatest(ctx, a, org, it.ExternalID)
	if err != nil {
		return nil, err
	}
	if ev == "" || proj == "" {
		return []RawAttachment{}, nil
	}
	var list []any
	if _, err := a.HTTP.json(ctx, "GET", "/api/0/projects/"+url.PathEscape(org)+"/"+url.PathEscape(proj)+"/events/"+url.PathEscape(ev)+"/attachments/", nil, nil, &list); err != nil {
		return nil, err
	}
	out := []RawAttachment{}
	for _, e := range list {
		m := asMap(e)
		id := str(m["id"])
		if id == "" {
			continue
		}
		size, _ := num(m["size"])
		out = append(out, RawAttachment{ExternalID: proj + "/" + ev + "/" + id, Name: firstNonEmpty(str(m["name"]), id), Mime: str(m["mimetype"]), Size: size, Downloadable: true, Source: "event"})
	}
	return out, nil
}

// FetchAttachment downloads one event attachment through the API (?download=1).
func (sentryAdapter) FetchAttachment(ctx context.Context, a *API, cfg map[string]string, _ Item, att RawAttachment, max int64) ([]byte, string, error) {
	org := cfg["organization"]
	parts := splitN3(att.ExternalID)
	if org == "" || parts == nil {
		return nil, "", &Failure{Code: CodeNotFound, Message: "Sentry no longer lists this attachment."}
	}
	r, err := a.HTTP.doMax(ctx, "GET", "/api/0/projects/"+url.PathEscape(org)+"/"+url.PathEscape(parts[0])+"/events/"+url.PathEscape(parts[1])+"/attachments/"+url.PathEscape(parts[2])+"/",
		url.Values{"download": {"1"}}, nil, max)
	if err != nil {
		return nil, "", err
	}
	return r.Body, r.Header.Get("Content-Type"), nil
}

// splitN3 splits "a/b/c" into its three non-empty parts (nil otherwise).
func splitN3(s string) []string {
	var out []string
	start := 0
	for i := 0; i <= len(s); i++ {
		if i == len(s) || s[i] == '/' {
			if i == start {
				return nil
			}
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	if len(out) != 3 {
		return nil
	}
	return out
}
