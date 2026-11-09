package requirements

import "testing"

// A criterion is one link whether it is linked with the id a spec or tasks.NumberCriteria gives it ("AC1: ...") or without it.
func TestACriterionsIDIsNotPartOfItsLink(t *testing.T) {
	base := criterionHash("Given a paid order, When it is refunded, Then the customer is credited")
	for _, c := range []string{
		"AC1: Given a paid order, When it is refunded, Then the customer is credited",
		"  ac12 - Given a paid order,  When it is refunded, Then the customer is credited",
		"AC3) given a paid order, when it is refunded, then the customer is credited",
	} {
		if criterionHash(c) != base {
			t.Errorf("%q links apart from the same criterion without its id", c)
		}
	}
	if criterionHash("Given a paid order, When it is refunded, Then the customer is debited") == base {
		t.Error("a different criterion shares the link")
	}
}
