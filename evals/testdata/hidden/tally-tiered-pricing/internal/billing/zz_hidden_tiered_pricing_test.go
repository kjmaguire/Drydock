package billing_test

// Hidden acceptance test of the trial task tally-tiered-pricing (evals/tasks). The agents never see it.
//
// It uses the Go API the prompt names: store.TiersMode, store.Graduated, store.Volume, store.Tier{UpTo *int64; UnitAmount
// int64; FlatAmount int64}, the Plan fields TiersMode and Tiers, and billing.TieredAmount(tiers, mode, quantity).

import (
	"encoding/json"
	"fmt"
	"testing"

	"example.com/tally/internal/billing"
	"example.com/tally/internal/store"
)

func hiddenTierUpTo(n int64) *int64 { return &n }

func hiddenTierString(tiers []store.Tier) string {
	out := "["
	for i, t := range tiers {
		if i > 0 {
			out += ", "
		}
		up := "null"
		if t.UpTo != nil {
			up = fmt.Sprint(*t.UpTo)
		}
		out += fmt.Sprintf("{up_to %s, unit %d, flat %d}", up, t.UnitAmount, t.FlatAmount)
	}
	return out + "]"
}

type hiddenTierCase struct {
	quantity          int64
	graduated, volume int64
}

func hiddenTierCheck(t *testing.T, tiers []store.Tier, cases []hiddenTierCase) {
	t.Helper()
	for _, c := range cases {
		if got := billing.TieredAmount(tiers, store.Graduated, c.quantity); got != c.graduated {
			t.Errorf("TieredAmount(%s, graduated, %d) = %d, want %d", hiddenTierString(tiers), c.quantity, got, c.graduated)
		}
		if got := billing.TieredAmount(tiers, store.Volume, c.quantity); got != c.volume {
			t.Errorf("TieredAmount(%s, volume, %d) = %d, want %d", hiddenTierString(tiers), c.quantity, got, c.volume)
		}
	}
}

func TestHiddenTieredAmountBoundaries(t *testing.T) {
	// units 1-100 at 10; 101-1000 at 8 plus 500 once; above 1000 at 5 plus 1000 once
	tiers := []store.Tier{
		{UpTo: hiddenTierUpTo(100), UnitAmount: 10},
		{UpTo: hiddenTierUpTo(1000), UnitAmount: 8, FlatAmount: 500},
		{UpTo: nil, UnitAmount: 5, FlatAmount: 1000},
	}
	hiddenTierCheck(t, tiers, []hiddenTierCase{
		{0, 0, 0},
		{1, 10, 10},
		{99, 990, 990},
		{100, 1000, 1000},    // up_to is inclusive: 100 is still the first tier
		{101, 1508, 1308},    // graduated 100*10 + 1*8 + 500; volume 101*8 + 500
		{500, 4700, 4500},    // graduated 1000 + 400*8 + 500; volume 500*8 + 500
		{1000, 8700, 8500},   // graduated 1000 + 900*8 + 500; volume 1000*8 + 500
		{1001, 9705, 6005},   // graduated 8700 + 1*5 + 1000; volume 1001*5 + 1000
		{5000, 29700, 26000}, // graduated 8700 + 4000*5 + 1000; volume 5000*5 + 1000
	})
}

func TestHiddenTieredAmountFlatAmounts(t *testing.T) {
	t.Run("a flat fee for the first ten units, then 100 each", func(t *testing.T) {
		hiddenTierCheck(t, []store.Tier{{UpTo: hiddenTierUpTo(10), FlatAmount: 2000}, {UnitAmount: 100}}, []hiddenTierCase{
			{0, 0, 0}, // nothing used costs nothing, even with a flat amount on the first tier
			{1, 2000, 2000},
			{10, 2000, 2000},
			{11, 2100, 1100},
			{25, 3500, 2500},
		})
	})
	t.Run("a single tier without an upper limit", func(t *testing.T) {
		hiddenTierCheck(t, []store.Tier{{UnitAmount: 25, FlatAmount: 300}}, []hiddenTierCase{{0, 0, 0}, {4, 400, 400}})
	})
	t.Run("a first tier of one unit", func(t *testing.T) {
		hiddenTierCheck(t, []store.Tier{{UpTo: hiddenTierUpTo(1), UnitAmount: 1000}, {UnitAmount: 1, FlatAmount: 50}}, []hiddenTierCase{
			{1, 1000, 1000},
			{2, 1051, 52},
			{10, 1059, 60},
		})
	})
	t.Run("four tiers, each with a flat amount", func(t *testing.T) {
		tiers := []store.Tier{
			{UpTo: hiddenTierUpTo(5), UnitAmount: 100, FlatAmount: 10},
			{UpTo: hiddenTierUpTo(10), UnitAmount: 90, FlatAmount: 20},
			{UpTo: hiddenTierUpTo(20), UnitAmount: 80, FlatAmount: 30},
			{UnitAmount: 70, FlatAmount: 40},
		}
		hiddenTierCheck(t, tiers, []hiddenTierCase{
			{0, 0, 0},
			{5, 510, 510},
			{6, 620, 560},
			{10, 980, 920},
			{11, 1090, 910},
			{20, 1810, 1630},
			{21, 1920, 1510},
		})
	})
}

func TestHiddenTieredStoreTypes(t *testing.T) {
	var mode store.TiersMode = store.Graduated
	if string(mode) != "graduated" || string(store.Volume) != "volume" {
		t.Fatalf("store.Graduated = %q and store.Volume = %q, want \"graduated\" and \"volume\"", store.Graduated, store.Volume)
	}
	var tier store.Tier
	tier.UpTo, tier.UnitAmount, tier.FlatAmount = hiddenTierUpTo(42), 7, 300
	b, err := json.Marshal(tier)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	if m["up_to"] != float64(42) || m["unit_amount"] != float64(7) || m["flat_amount"] != float64(300) {
		t.Fatalf("store.Tier as JSON = %s, want the fields up_to 42, unit_amount 7 and flat_amount 300", b)
	}
	var back store.Tier
	if err := json.Unmarshal([]byte(`{"up_to": 9, "unit_amount": 4, "flat_amount": 1}`), &back); err != nil || back.UpTo == nil ||
		*back.UpTo != 9 || back.UnitAmount != 4 || back.FlatAmount != 1 {
		t.Fatalf(`{"up_to": 9, "unit_amount": 4, "flat_amount": 1} decodes to %s, %v`, hiddenTierString([]store.Tier{back}), err)
	}

	var plan store.Plan
	plan.TiersMode = store.Volume
	plan.Tiers = []store.Tier{tier, {UnitAmount: 3}}
	b, err = json.Marshal(plan)
	if err != nil {
		t.Fatal(err)
	}
	var pm struct {
		TiersMode string `json:"tiers_mode"`
		Tiers     []struct {
			UpTo       *int64 `json:"up_to"`
			UnitAmount int64  `json:"unit_amount"`
			FlatAmount int64  `json:"flat_amount"`
		} `json:"tiers"`
	}
	if err := json.Unmarshal(b, &pm); err != nil {
		t.Fatal(err)
	}
	if pm.TiersMode != "volume" || len(pm.Tiers) != 2 || pm.Tiers[0].UpTo == nil || *pm.Tiers[0].UpTo != 42 || pm.Tiers[1].UpTo != nil ||
		pm.Tiers[1].UnitAmount != 3 {
		t.Fatalf("store.Plan with tiers as JSON = %s, want \"tiers_mode\": \"volume\" and \"tiers\" with both tiers", b)
	}
}
