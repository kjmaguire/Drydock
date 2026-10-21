package api_test

// Hidden acceptance test of the trial task tally-idempotent-payments (evals/tasks). The agents never see it.
// Requests that run in goroutines are built with httptest and sent to e.Server.ServeHTTP (e.Do may call t.Fatal). Charges
// are held with e.Gateway.OnCharge; waits for something that should take milliseconds get seconds before they fail.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"example.com/tally/internal/apitest"
	"example.com/tally/internal/payments"
)

const hiddenIdemWait = 10 * time.Second

const hiddenIdemVisa = `{"payment_method":"pm_card_visa"}`

// hiddenIdemPay sends POST /v1/invoices/{id}/pay straight to the server, with an Idempotency-Key when idemKey is not "-".
// It never calls t.Fatal, so goroutines may use it.
func hiddenIdemPay(e *apitest.Env, org apitest.Org, invoiceID, idemKey, body string) *apitest.Response {
	var rd io.Reader
	if body != "" {
		rd = strings.NewReader(body)
	}
	req := httptest.NewRequest("POST", "/v1/invoices/"+invoiceID+"/pay", rd)
	req.Header.Set("Authorization", "Bearer "+org.Key)
	if idemKey != "-" {
		req.Header.Set("Idempotency-Key", idemKey)
	}
	rec := httptest.NewRecorder()
	e.Server.ServeHTTP(rec, req)
	return &apitest.Response{Code: rec.Code, Header: rec.Header(), Body: rec.Body.Bytes()}
}

// hiddenIdemInvoice creates an open invoice of amount (minor units, no tax: the organizations' tax rate is 0) for a new
// customer, with defaultMethod as the customer's default payment method ("" for none). Each test uses its own amounts, so
// a charge's amount tells which invoice it was for.
func hiddenIdemInvoice(t *testing.T, e *apitest.Env, org apitest.Org, amount int64, defaultMethod string) (invoiceID, customerID string) {
	t.Helper()
	c := e.CreateCustomer(org, "Hidden Payer", fmt.Sprintf("payer-%d@hidden.example", amount))
	if defaultMethod != "" {
		hiddenIdemSetMethod(t, e, org, c.ID, defaultMethod)
	}
	var inv struct {
		ID string `json:"id"`
	}
	e.MustDo(http.StatusCreated, "POST", "/v1/invoices", org.Key, map[string]any{"customer_id": c.ID, "currency": "USD",
		"lines": []map[string]any{{"description": "Hidden work", "quantity": 1, "unit_amount": amount}}}, &inv)
	return inv.ID, c.ID
}

func hiddenIdemSetMethod(t *testing.T, e *apitest.Env, org apitest.Org, customerID, method string) {
	t.Helper()
	e.MustDo(http.StatusOK, "PATCH", "/v1/customers/"+customerID, org.Key, map[string]any{"default_payment_method": method}, nil)
}

func hiddenIdemCharges(e *apitest.Env, amount int64) int {
	n := 0
	for _, c := range e.Gateway.Charges() {
		if c.Amount == amount {
			n++
		}
	}
	return n
}

func hiddenIdemCalls(e *apitest.Env, amount int64) int {
	n := 0
	for _, c := range e.Gateway.Calls() {
		if c.Amount == amount {
			n++
		}
	}
	return n
}

func hiddenIdemWantCharges(t *testing.T, e *apitest.Env, amount int64, want int, what string) {
	t.Helper()
	if got := hiddenIdemCharges(e, amount); got != want {
		t.Fatalf("%s: the gateway took %d charge(s) of %d, want %d", what, got, amount, want)
	}
}

func hiddenIdemCode(r *apitest.Response) string {
	var body struct {
		Error string `json:"error"`
	}
	_ = json.Unmarshal(r.Body, &body)
	return body.Error
}

func hiddenIdemWant(t *testing.T, r *apitest.Response, status int, code, what string) {
	t.Helper()
	if r.Code != status || (code != "" && hiddenIdemCode(r) != code) {
		want := fmt.Sprint(status)
		if code != "" {
			want += fmt.Sprintf(` {"error":%q}`, code)
		}
		t.Fatalf("%s: want %s, got %s", what, want, r)
	}
}

func hiddenIdemWantFresh(t *testing.T, r *apitest.Response, what string) {
	t.Helper()
	if got := r.Header.Get("Idempotent-Replayed"); got == "true" {
		t.Fatalf("%s: the response says Idempotent-Replayed: true, but it is not a replay (%s)", what, r)
	}
}

// hiddenIdemWantReplay checks that r replays first: the same status and body, with Idempotent-Replayed: true.
func hiddenIdemWantReplay(t *testing.T, r, first *apitest.Response, what string) {
	t.Helper()
	if r.Code != first.Code || !bytes.Equal(r.Body, first.Body) {
		t.Fatalf("%s: want the first response again (%s), got %s", what, first, r)
	}
	if got := r.Header.Get("Idempotent-Replayed"); got != "true" {
		t.Fatalf("%s: want the header Idempotent-Replayed: true on a replay, got %q", what, got)
	}
}

func TestHiddenIdempotentPayReplaysTheFirstResponse(t *testing.T) {
	e := apitest.New(t)
	const amount = 1111
	inv, _ := hiddenIdemInvoice(t, e, e.Acme, amount, "")
	first := hiddenIdemPay(e, e.Acme, inv, "order-1111", hiddenIdemVisa)
	hiddenIdemWant(t, first, http.StatusOK, "", "the first pay request with a key")
	hiddenIdemWantFresh(t, first, "the first pay request with a key")
	for i, advance := range []time.Duration{0, time.Second, 23 * time.Hour} {
		e.Clock.Advance(advance)
		r := hiddenIdemPay(e, e.Acme, inv, "order-1111", hiddenIdemVisa)
		hiddenIdemWantReplay(t, r, first, fmt.Sprintf("repeat %d of the request, %s after the one before", i+1, advance))
	}
	hiddenIdemWantCharges(t, e, amount, 1, "after three replays")
	if n := hiddenIdemCalls(e, amount); n != 1 {
		t.Fatalf("a replay asked the gateway to charge again: %d charge requests, want 1", n)
	}
}

func TestHiddenIdempotencyKeyReusedForAnotherRequest(t *testing.T) {
	e := apitest.New(t)
	const amountA, amountB = 2221, 2222
	invA, _ := hiddenIdemInvoice(t, e, e.Acme, amountA, "")
	invB, _ := hiddenIdemInvoice(t, e, e.Acme, amountB, "")
	first := hiddenIdemPay(e, e.Acme, invA, "order-2221", hiddenIdemVisa)
	hiddenIdemWant(t, first, http.StatusOK, "", "the first pay request")

	r := hiddenIdemPay(e, e.Acme, invA, "order-2221", `{"payment_method":"pm_card_mastercard"}`)
	hiddenIdemWant(t, r, http.StatusUnprocessableEntity, "idempotency_key_reused", "the same key and invoice with another body")
	r = hiddenIdemPay(e, e.Acme, invB, "order-2221", hiddenIdemVisa)
	hiddenIdemWant(t, r, http.StatusUnprocessableEntity, "idempotency_key_reused", "the same key and body for another invoice")

	hiddenIdemWantCharges(t, e, amountB, 0, "the other invoice")
	var got struct {
		Status string `json:"status"`
	}
	e.MustDo(http.StatusOK, "GET", "/v1/invoices/"+invB, e.Acme.Key, nil, &got)
	if got.Status != "open" {
		t.Fatalf("the invoice of a refused request is %q, want open", got.Status)
	}
	hiddenIdemWantReplay(t, hiddenIdemPay(e, e.Acme, invA, "order-2221", hiddenIdemVisa), first, "the original request after the refused ones")
	hiddenIdemWantCharges(t, e, amountA, 1, "the first invoice")
}

func TestHiddenIdempotencyKeysBelongToAnOrganization(t *testing.T) {
	e := apitest.New(t)
	const acmeAmount, globexAmount = 3331, 3332
	acmeInv, _ := hiddenIdemInvoice(t, e, e.Acme, acmeAmount, "")
	globexInv, _ := hiddenIdemInvoice(t, e, e.Globex, globexAmount, "")
	acmeFirst := hiddenIdemPay(e, e.Acme, acmeInv, "shared-key", hiddenIdemVisa)
	hiddenIdemWant(t, acmeFirst, http.StatusOK, "", "Acme pays with the key")

	globexFirst := hiddenIdemPay(e, e.Globex, globexInv, "shared-key", hiddenIdemVisa)
	hiddenIdemWant(t, globexFirst, http.StatusOK, "", "Globex pays its own invoice with the same key")
	hiddenIdemWantFresh(t, globexFirst, "Globex's first use of the key")
	if !bytes.Contains(globexFirst.Body, []byte(globexInv)) || bytes.Contains(globexFirst.Body, []byte(acmeInv)) {
		t.Fatalf("Globex's response is not about its own invoice %s: %s", globexInv, globexFirst)
	}
	hiddenIdemWantCharges(t, e, globexAmount, 1, "Globex's invoice")

	hiddenIdemWantReplay(t, hiddenIdemPay(e, e.Globex, globexInv, "shared-key", hiddenIdemVisa), globexFirst, "Globex repeats its request")
	hiddenIdemWantReplay(t, hiddenIdemPay(e, e.Acme, acmeInv, "shared-key", hiddenIdemVisa), acmeFirst, "Acme repeats its request")
	hiddenIdemWantCharges(t, e, acmeAmount, 1, "Acme's invoice")
	hiddenIdemWantCharges(t, e, globexAmount, 1, "Globex's invoice")
}

func TestHiddenIdempotencyServerErrorsAreNotKept(t *testing.T) {
	e := apitest.New(t)
	const amount = 4441
	inv, customer := hiddenIdemInvoice(t, e, e.Acme, amount, "pm_gateway_down")
	r := hiddenIdemPay(e, e.Acme, inv, "retry-4441", "{}")
	hiddenIdemWant(t, r, http.StatusServiceUnavailable, "gateway_unavailable", "paying with the gateway down")
	r = hiddenIdemPay(e, e.Acme, inv, "retry-4441", "{}")
	hiddenIdemWant(t, r, http.StatusServiceUnavailable, "gateway_unavailable", "a retry with the same key, gateway still down")
	hiddenIdemWantFresh(t, r, "a retry after a 503")
	if n := hiddenIdemCalls(e, amount); n != 2 {
		t.Fatalf("a retry after a 503 must try the payment again: %d charge requests, want 2", n)
	}

	// The gateway works again for this customer: the retry with the same key pays.
	hiddenIdemSetMethod(t, e, e.Acme, customer, "pm_card_visa")
	paid := hiddenIdemPay(e, e.Acme, inv, "retry-4441", "{}")
	hiddenIdemWant(t, paid, http.StatusOK, "", "a retry with the same key once the gateway works")
	hiddenIdemWantFresh(t, paid, "the retry that paid")
	hiddenIdemWantCharges(t, e, amount, 1, "after the retry")
	hiddenIdemWantReplay(t, hiddenIdemPay(e, e.Acme, inv, "retry-4441", "{}"), paid, "a repeat after the payment")
	hiddenIdemWantCharges(t, e, amount, 1, "after the repeat")
}

func TestHiddenIdempotencyKeepsOtherResponses(t *testing.T) {
	e := apitest.New(t)

	// 402: the decline is replayed, the gateway is not asked again.
	const declined = 5551
	inv, _ := hiddenIdemInvoice(t, e, e.Acme, declined, "")
	first := hiddenIdemPay(e, e.Acme, inv, "decline-5551", `{"payment_method":"pm_card_declined"}`)
	hiddenIdemWant(t, first, http.StatusPaymentRequired, "payment_declined", "a declined card")
	hiddenIdemWantReplay(t, hiddenIdemPay(e, e.Acme, inv, "decline-5551", `{"payment_method":"pm_card_declined"}`), first, "repeating a declined payment")
	if n := hiddenIdemCalls(e, declined); n != 1 {
		t.Fatalf("a replayed decline asked the gateway again: %d charge requests, want 1", n)
	}
	hiddenIdemWant(t, hiddenIdemPay(e, e.Acme, inv, "decline-5551", hiddenIdemVisa), http.StatusUnprocessableEntity, "idempotency_key_reused", "the declined request's key with another card")
	hiddenIdemWant(t, hiddenIdemPay(e, e.Acme, inv, "decline-5551-retry", hiddenIdemVisa), http.StatusOK, "", "a new key with another card")
	hiddenIdemWantCharges(t, e, declined, 1, "the declined invoice, paid with another card")

	// 409 already_paid.
	const paidAmount = 5552
	paidInv, _ := hiddenIdemInvoice(t, e, e.Acme, paidAmount, "")
	hiddenIdemWant(t, hiddenIdemPay(e, e.Acme, paidInv, "-", hiddenIdemVisa), http.StatusOK, "", "paying without a key")
	first = hiddenIdemPay(e, e.Acme, paidInv, "late-5552", hiddenIdemVisa)
	hiddenIdemWant(t, first, http.StatusConflict, "already_paid", "paying a paid invoice with a key")
	hiddenIdemWantReplay(t, hiddenIdemPay(e, e.Acme, paidInv, "late-5552", hiddenIdemVisa), first, "repeating already_paid")
	hiddenIdemWantCharges(t, e, paidAmount, 1, "the paid invoice")

	// 404.
	first = hiddenIdemPay(e, e.Acme, "in_hidden_missing", "missing-1", hiddenIdemVisa)
	hiddenIdemWant(t, first, http.StatusNotFound, "", "paying an invoice that does not exist")
	hiddenIdemWantReplay(t, hiddenIdemPay(e, e.Acme, "in_hidden_missing", "missing-1", hiddenIdemVisa), first, "repeating a 404")

	// 422 no_payment_method stays the answer for the key even once the customer has a payment method.
	const noMethod = 5553
	nmInv, nmCustomer := hiddenIdemInvoice(t, e, e.Acme, noMethod, "")
	first = hiddenIdemPay(e, e.Acme, nmInv, "nomethod-5553", "")
	hiddenIdemWant(t, first, http.StatusUnprocessableEntity, "no_payment_method", "paying without a payment method")
	hiddenIdemSetMethod(t, e, e.Acme, nmCustomer, "pm_card_visa")
	hiddenIdemWantReplay(t, hiddenIdemPay(e, e.Acme, nmInv, "nomethod-5553", ""), first, "repeating no_payment_method after a method was added")
	hiddenIdemWantCharges(t, e, noMethod, 0, "the invoice whose kept answer is no_payment_method")
}

func TestHiddenIdempotencyExpiresAfter24Hours(t *testing.T) {
	e := apitest.New(t)
	const amountA, amountB, amountC, amountD = 6661, 6662, 6663, 6664
	invA, _ := hiddenIdemInvoice(t, e, e.Acme, amountA, "")
	invB, _ := hiddenIdemInvoice(t, e, e.Acme, amountB, "")
	invC, _ := hiddenIdemInvoice(t, e, e.Acme, amountC, "")
	invD, _ := hiddenIdemInvoice(t, e, e.Acme, amountD, "")
	t0 := e.Clock.Now()
	for _, p := range []struct{ inv, key string }{{invA, "daily-a"}, {invC, "daily-c"}} {
		hiddenIdemWant(t, hiddenIdemPay(e, e.Acme, p.inv, p.key, hiddenIdemVisa), http.StatusOK, "", "paying with the key "+p.key)
	}
	firstD := hiddenIdemPay(e, e.Acme, invD, "daily-d", hiddenIdemVisa)
	hiddenIdemWant(t, firstD, http.StatusOK, "", "paying with the key daily-d")

	// Only daily-d is used again before the 24 hours are over, so daily-a and daily-c expire 24 hours after they were kept
	// however the expiry is counted.
	e.Clock.Set(t0.Add(23*time.Hour + 59*time.Minute))
	hiddenIdemWantReplay(t, hiddenIdemPay(e, e.Acme, invD, "daily-d", hiddenIdemVisa), firstD, "23h59m later")
	hiddenIdemWant(t, hiddenIdemPay(e, e.Acme, invB, "daily-d", hiddenIdemVisa), http.StatusUnprocessableEntity, "idempotency_key_reused", "23h59m later, D's key for invoice B")

	e.Clock.Set(t0.Add(24*time.Hour + time.Minute))
	r := hiddenIdemPay(e, e.Acme, invB, "daily-a", hiddenIdemVisa)
	hiddenIdemWant(t, r, http.StatusOK, "", "24h01m later, A's key counts as new: it pays invoice B")
	hiddenIdemWantFresh(t, r, "the expired key's new request")
	r = hiddenIdemPay(e, e.Acme, invC, "daily-c", hiddenIdemVisa)
	hiddenIdemWant(t, r, http.StatusConflict, "already_paid", "24h01m later, the same request as C's first one is handled anew")
	hiddenIdemWantFresh(t, r, "the expired key's repeated request")
	hiddenIdemWantCharges(t, e, amountA, 1, "invoice A")
	hiddenIdemWantCharges(t, e, amountB, 1, "invoice B")
	hiddenIdemWantCharges(t, e, amountC, 1, "invoice C")
	hiddenIdemWantCharges(t, e, amountD, 1, "invoice D")
}

func TestHiddenIdempotencyKeyFormat(t *testing.T) {
	e := apitest.New(t)
	const amount = 7771
	inv, _ := hiddenIdemInvoice(t, e, e.Acme, amount, "")
	for _, bad := range []struct{ name, key string }{
		{"256 characters", strings.Repeat("k", 256)},
		{"a tab", "order\t1"},
		{"a newline", "order\n1"},
		{"a NUL byte", "order\x001"},
		{"DEL", "order\x7f"},
		{"non-ASCII", "commande-été"},
	} {
		r := hiddenIdemPay(e, e.Acme, inv, bad.key, hiddenIdemVisa)
		hiddenIdemWant(t, r, http.StatusBadRequest, "invalid_idempotency_key", "an Idempotency-Key with "+bad.name)
	}
	hiddenIdemWantCharges(t, e, amount, 0, "after invalid keys")

	long := strings.Repeat("a", 247) + "~!{}|#$%"
	first := hiddenIdemPay(e, e.Acme, inv, long, hiddenIdemVisa)
	hiddenIdemWant(t, first, http.StatusOK, "", "an Idempotency-Key of 255 printable characters")
	hiddenIdemWantReplay(t, hiddenIdemPay(e, e.Acme, inv, long, hiddenIdemVisa), first, "repeating the 255-character key")

	const other = 7772
	inv2, _ := hiddenIdemInvoice(t, e, e.Acme, other, "")
	hiddenIdemWant(t, hiddenIdemPay(e, e.Acme, inv2, "x", hiddenIdemVisa), http.StatusOK, "", "a one-character Idempotency-Key")
	hiddenIdemWantCharges(t, e, amount, 1, "the first invoice")
	hiddenIdemWantCharges(t, e, other, 1, "the second invoice")
}

// hiddenIdemHold makes the first charge of one amount wait in OnCharge until released.
type hiddenIdemHold struct {
	amount  int64
	mu      sync.Mutex
	seen    int
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func hiddenIdemNewHold(e *apitest.Env, amount int64) *hiddenIdemHold {
	h := &hiddenIdemHold{amount: amount, entered: make(chan struct{}), release: make(chan struct{})}
	e.Gateway.OnCharge = h.onCharge
	return h
}

func (h *hiddenIdemHold) onCharge(req payments.ChargeRequest) {
	if req.Amount != h.amount {
		return
	}
	h.mu.Lock()
	h.seen++
	first := h.seen == 1
	h.mu.Unlock()
	if !first {
		return
	}
	close(h.entered)
	select {
	case <-h.release:
	case <-time.After(30 * time.Second): // never hold a charge forever, even when the test failed
	}
}

func (h *hiddenIdemHold) free() { h.once.Do(func() { close(h.release) }) }

func (h *hiddenIdemHold) waitEntered(t *testing.T) {
	t.Helper()
	select {
	case <-h.entered:
	case <-time.After(hiddenIdemWait):
		t.Fatalf("the charge of %d never reached the gateway", h.amount)
	}
}

// hiddenIdemAsync sends a pay request in a goroutine.
func hiddenIdemAsync(e *apitest.Env, org apitest.Org, invoiceID, idemKey, body string) <-chan *apitest.Response {
	ch := make(chan *apitest.Response, 1)
	go func() { ch <- hiddenIdemPay(e, org, invoiceID, idemKey, body) }()
	return ch
}

// hiddenIdemAtOnce sends a pay request while a charge is held and fails unless it is answered without waiting for it.
func hiddenIdemAtOnce(t *testing.T, e *apitest.Env, invoiceID, idemKey, body, what string) *apitest.Response {
	t.Helper()
	select {
	case r := <-hiddenIdemAsync(e, e.Acme, invoiceID, idemKey, body):
		return r
	case <-time.After(hiddenIdemWait):
		t.Fatalf("%s: no answer within %s while another payment of the invoice was under way; it must not wait", what, hiddenIdemWait)
		return nil
	}
}

func hiddenIdemResult(t *testing.T, ch <-chan *apitest.Response, what string) *apitest.Response {
	t.Helper()
	select {
	case r := <-ch:
		return r
	case <-time.After(hiddenIdemWait):
		t.Fatalf("%s: no answer within %s", what, hiddenIdemWait)
		return nil
	}
}

func TestHiddenIdempotencyRequestAndPaymentInProgress(t *testing.T) {
	e := apitest.New(t)
	const amount, otherAmount = 8881, 8882
	inv, _ := hiddenIdemInvoice(t, e, e.Acme, amount, "")
	otherInv, _ := hiddenIdemInvoice(t, e, e.Acme, otherAmount, "")
	hold := hiddenIdemNewHold(e, amount)
	defer hold.free()

	firstCh := hiddenIdemAsync(e, e.Acme, inv, "slow-8881", hiddenIdemVisa)
	var first *apitest.Response
	defer func() { // whatever happened, let the first request finish before the test ends
		hold.free()
		if first == nil {
			select {
			case <-firstCh:
			case <-time.After(hiddenIdemWait):
			}
		}
	}()
	hold.waitEntered(t)

	r := hiddenIdemAtOnce(t, e, inv, "slow-8881", hiddenIdemVisa, "the same key while the first request is handled")
	hiddenIdemWant(t, r, http.StatusConflict, "request_in_progress", "the same key while the first request is handled")
	r = hiddenIdemAtOnce(t, e, inv, "-", hiddenIdemVisa, "no key while the invoice's charge is under way")
	hiddenIdemWant(t, r, http.StatusConflict, "payment_in_progress", "no key while the invoice's charge is under way")
	r = hiddenIdemAtOnce(t, e, inv, "other-key-8881", hiddenIdemVisa, "another key while the invoice's charge is under way")
	hiddenIdemWant(t, r, http.StatusConflict, "payment_in_progress", "another key while the invoice's charge is under way")
	r = hiddenIdemAtOnce(t, e, otherInv, "-", hiddenIdemVisa, "paying another invoice meanwhile")
	hiddenIdemWant(t, r, http.StatusOK, "", "paying another invoice while the first one's charge is under way")

	hold.free()
	first = hiddenIdemResult(t, firstCh, "the held request")
	hiddenIdemWant(t, first, http.StatusOK, "", "the held request")
	hiddenIdemWantFresh(t, first, "the held request")
	hiddenIdemWantReplay(t, hiddenIdemPay(e, e.Acme, inv, "slow-8881", hiddenIdemVisa), first, "the key after the first request finished")
	hiddenIdemWantCharges(t, e, amount, 1, "the invoice")
	hiddenIdemWantCharges(t, e, otherAmount, 1, "the other invoice")
	if n := hiddenIdemCalls(e, amount); n != 1 {
		t.Fatalf("the gateway was asked %d times to charge the invoice, want once", n)
	}
}

// TestHiddenConcurrentPayRequestsChargeOnce fires many pay requests for one invoice at the same moment, while the first
// charge to reach the gateway is held until every request was answered or has waited a while.
func TestHiddenConcurrentPayRequestsChargeOnce(t *testing.T) {
	for _, withKey := range []bool{false, true} {
		e := apitest.New(t)
		amount := int64(9991)
		if withKey {
			amount = 9992
		}
		inv, _ := hiddenIdemInvoice(t, e, e.Acme, amount, "")
		hold := hiddenIdemNewHold(e, amount)
		const n = 16
		start := make(chan struct{})
		results := make(chan *apitest.Response, n)
		var wg sync.WaitGroup
		for i := 0; i < n; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				key := "-"
				if withKey {
					key = "burst-key"
				}
				results <- hiddenIdemPay(e, e.Acme, inv, key, hiddenIdemVisa)
			}()
		}
		close(start)
		// Every request but the held one is answered while the charge is held.
		var answered []*apitest.Response
		hold.waitEntered(t)
		for len(answered) < n-1 {
			select {
			case r := <-results:
				answered = append(answered, r)
			case <-time.After(hiddenIdemWait):
				hold.free()
				wg.Wait()
				t.Fatalf("with key %v: only %d of %d other requests were answered while a charge of the invoice was under way; they must not wait", withKey, len(answered), n-1)
			}
		}
		hold.free()
		wg.Wait()
		close(results)
		for r := range results {
			answered = append(answered, r)
		}
		ok := 0
		for _, r := range answered {
			switch {
			case r.Code == http.StatusOK && r.Header.Get("Idempotent-Replayed") != "true":
				ok++
			case r.Code == http.StatusConflict:
			default:
				t.Fatalf("with key %v: an unexpected answer to one of the simultaneous requests: %s", withKey, r)
			}
		}
		if ok != 1 {
			t.Fatalf("with key %v: %d of %d simultaneous requests paid, want exactly 1", withKey, ok, n)
		}
		hiddenIdemWantCharges(t, e, amount, 1, fmt.Sprintf("with key %v, %d simultaneous requests", withKey, n))
	}
}
