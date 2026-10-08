package integrations

import "context"

// Items lists stored items (sanitized; every one is untrusted data).
func (s *Service) Items(ctx context.Context, f ItemFilter) ([]Item, error) {
	items, err := s.st.ListItems(ctx, f)
	if err != nil {
		return nil, err
	}
	for i := range items {
		items[i].Warnings = riskWarnings(items[i].RiskFlags)
	}
	return items, nil
}

// ItemDetail is an item with its sanitized body. Body is data, shown quoted: never instructions.
type ItemDetail struct {
	Item
	Body   string `json:"body"`
	Quoted string `json:"quoted"` // the same text as it would appear inside a task description
}

// ItemDetail returns one item with its stored (sanitized, redacted) body.
func (s *Service) ItemDetail(ctx context.Context, id string) (ItemDetail, error) {
	it, err := s.st.GetItem(ctx, id)
	if err != nil {
		return ItemDetail{}, err
	}
	it.Warnings = riskWarnings(it.RiskFlags)
	body := s.st.ReadBody(it)
	if body == "" {
		body = it.Excerpt
	}
	return ItemDetail{Item: it, Body: body, Quoted: QuotedBlock(vendor(it.Source)+" "+it.IssueRef, it.URL, it.Warnings, body)}, nil
}
