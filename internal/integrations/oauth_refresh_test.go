package integrations_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kjmaguire/drydock/internal/integrations"
	"github.com/kjmaguire/drydock/internal/integrations/itfixture"
	"github.com/kjmaguire/drydock/internal/providers"
)

// linearSignIn signs in to Linear through the OAuth fixture and connects it, assigned to project prj1 (team ENG), against a
// Linear fixture that takes any token (which one arrived is read from AuthSeen).
func linearSignIn(t *testing.T, ourl string, mod func(*integrations.Options)) (*harness, *itfixture.Linear, integrations.ConnectionView) {
	t.Helper()
	lfx := itfixture.NewLinear("")
	lts, lurl := itfixture.Serve(lfx.Server())
	t.Cleanup(lts.Close)
	h := newHarness(t, func(o *integrations.Options) {
		o.OAuth = map[string]integrations.OAuthApp{integrations.KindLinear: {ClientID: "lin_client_fixture", AuthURL: ourl + "/authorize", TokenURL: ourl + "/token"}}
		if mod != nil {
			mod(o)
		}
	})
	ctx := context.Background()
	s, err := h.svc.OAuthStart(ctx, human, integrations.KindLinear, "")
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.Get(s.URL)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if done := waitSignedIn(t, h, s.ID); done.State != "signed_in" {
		t.Fatalf("%+v", done)
	}
	c, err := h.svc.Connect(ctx, human, integrations.ConnectReq{Kind: integrations.KindLinear, OAuthSession: s.ID, BaseURL: lurl, ProjectID: "prj1", Config: map[string]string{"team": "ENG"}})
	if err != nil {
		t.Fatal(err)
	}
	return h, lfx, c
}

func failureCode(err error) string {
	var f *integrations.Failure
	if errors.As(err, &f) {
		return f.Code
	}
	return ""
}

func storedRefreshToken(t *testing.T, h *harness, ref string) string {
	t.Helper()
	v, err := h.vault.Get(context.Background(), ref)
	if err != nil {
		t.Fatal(err)
	}
	var sc struct {
		RefreshToken string `json:"refresh_token"`
	}
	if err := json.Unmarshal([]byte(v), &sc); err != nil {
		t.Fatalf("the stored credential is not an OAuth sign-in: %v", err)
	}
	return sc.RefreshToken
}

func waitUntil(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting: " + what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// A token endpoint that is down (502) or does not answer in time has not refused the sign-in: the connection backs off and
// retries instead of being marked signed out (the scheduler never syncs an unauthenticated connection again by itself), and
// the refresh token is not given up.
func TestATransientRefreshFailureIsRetriedNotSignedOut(t *testing.T) {
	ofx, ourl := oauthFixture(t, "lin_client_fixture")
	ofx.ExpiresIn = 600
	h, lfx, c := linearSignIn(t, ourl, func(o *integrations.Options) { o.CallTimeout = time.Second })
	ctx := context.Background()
	held := storedRefreshToken(t, h, c.CredentialRef)
	h.clk.Advance(11 * time.Minute)

	ofx.SetRefreshStatus(http.StatusBadGateway)
	_, err := h.svc.Sync(ctx, c.ID, integrations.SyncOpts{})
	if code := failureCode(err); code != integrations.CodeUnreachable {
		t.Fatalf("a 502 from the token endpoint: code %q (%v), want %q", code, err, integrations.CodeUnreachable)
	}
	if v, _ := h.svc.Get(ctx, c.ID); v.Status != integrations.StatusUnreachable {
		t.Fatalf("after a 502: status %q, want %q", v.Status, integrations.StatusUnreachable)
	}

	// a manual sync (Force: the connection is backing off) while the token endpoint does not answer
	ofx.SetRefreshStatus(0)
	ofx.SetRefreshDelay(time.Minute) // longer than the call timeout
	_, err = h.svc.Sync(ctx, c.ID, integrations.SyncOpts{Force: true})
	if code := failureCode(err); code != integrations.CodeTimeout && code != integrations.CodeUnreachable {
		t.Fatalf("a token endpoint that does not answer: code %q (%v), want a timeout", code, err)
	}
	if v, _ := h.svc.Get(ctx, c.ID); v.Status == integrations.StatusUnauthenticated {
		t.Fatalf("a timeout marked the connection signed out: %q", v.StatusDetail)
	}
	if got := storedRefreshToken(t, h, c.CredentialRef); got != held {
		t.Fatal("the stored sign-in changed although no refresh succeeded")
	}

	// the endpoint is back: once the backoff has passed the scheduler syncs the connection by itself, renewing the sign-in
	ofx.SetRefreshDelay(0)
	h.clk.Advance(2 * time.Hour)
	if ran := h.svc.Tick(ctx); len(ran) != 1 || ran[0] != c.ID {
		t.Fatalf("the scheduler synced %v after the outage", ran)
	}
	if v, _ := h.svc.Get(ctx, c.ID); v.Status != integrations.StatusOK {
		t.Fatalf("after the outage: status %q (%s)", v.Status, v.StatusDetail)
	}
	if acc, rej := ofx.RefreshCounts(); acc != 1 || rej != 0 {
		t.Fatalf("refreshes accepted %d, rejected %d", acc, rej)
	}
	seen := lfx.AuthSeen()
	if seen[len(seen)-1] != "Bearer "+ofx.LastIssued() {
		t.Fatalf("the renewed token was not used: %q", seen[len(seen)-1])
	}
}

// A refusal of the refresh grant itself (invalid_grant: the person revoked Drydock's access) is a signed-out connection.
func TestARefusedRefreshGrantMarksTheConnectionSignedOut(t *testing.T) {
	ofx, ourl := oauthFixture(t, "lin_client_fixture")
	ofx.ExpiresIn = 600
	h, _, c := linearSignIn(t, ourl, nil)
	ctx := context.Background()
	h.clk.Advance(11 * time.Minute)
	ofx.RevokeRefreshTokens()
	_, err := h.svc.Sync(ctx, c.ID, integrations.SyncOpts{})
	if code := failureCode(err); code != integrations.CodeUnauthorized {
		t.Fatalf("code %q (%v), want %q", code, err, integrations.CodeUnauthorized)
	}
	if v, _ := h.svc.Get(ctx, c.ID); v.Status != integrations.StatusUnauthenticated {
		t.Fatalf("status %q, want %q", v.Status, integrations.StatusUnauthenticated)
	}
}

// startSlowRefresh expires the sign-in and starts a call whose refresh is held in flight by the fixture; it returns once the
// token endpoint has the request, with a second caller waiting behind it for the refresh lock.
func startSlowRefresh(t *testing.T, h *harness, ofx *itfixture.OAuth, id string) (first, second chan error) {
	t.Helper()
	ofx.SetRefreshDelay(400 * time.Millisecond)
	h.clk.Advance(11 * time.Minute)
	first, second = make(chan error, 1), make(chan error, 1)
	go func() {
		_, err := h.svc.Choices(context.Background(), id, "team", nil)
		first <- err
	}()
	waitUntil(t, "the refresh to reach the token endpoint", func() bool { return ofx.RefreshesReceived() == 1 })
	go func() {
		_, err := h.svc.Choices(context.Background(), id, "team", nil)
		second <- err
	}()
	time.Sleep(50 * time.Millisecond) // the second caller is now waiting for the first one's refresh
	return first, second
}

// The person reconnects with a personal API key while a refresh is in flight and another caller waits for it: the API key
// stays (the renewed OAuth token is not written over it), the waiting caller does not refresh again, and the next call goes
// out with the API key.
func TestAReconnectDuringARefreshIsNotOverwritten(t *testing.T) {
	ofx, ourl := oauthFixture(t, "lin_client_fixture")
	ofx.ExpiresIn = 600
	h, lfx, c := linearSignIn(t, ourl, nil)
	ctx := context.Background()
	first, second := startSlowRefresh(t, h, ofx, c.ID)

	const apiKey = "lin_api_fixtureReconnect42"
	if _, err := h.svc.Connect(ctx, human, integrations.ConnectReq{ID: c.ID, Token: apiKey}); err != nil {
		t.Fatal(err)
	}
	if err := <-first; err != nil {
		t.Fatalf("the caller that refreshed: %v", err)
	}
	<-second // either used the renewed token before the reconnect or found the API key: both are fine
	if v, _ := h.vault.Get(ctx, c.CredentialRef); v != apiKey {
		t.Fatal("the reconnected API key was overwritten by the renewed OAuth sign-in")
	}
	if acc, rej := ofx.RefreshCounts(); acc != 1 || rej != 0 {
		t.Fatalf("refreshes accepted %d, rejected %d; want one", acc, rej)
	}
	if _, err := h.svc.Choices(ctx, c.ID, "team", nil); err != nil {
		t.Fatal(err)
	}
	seen := lfx.AuthSeen()
	if seen[len(seen)-1] != apiKey {
		t.Fatalf("the call after the reconnect used %q, not the API key", seen[len(seen)-1])
	}
}

// The person disconnects while a refresh is in flight and another caller waits for it: the credential stays deleted.
func TestADisconnectDuringARefreshIsNotUndone(t *testing.T) {
	ofx, ourl := oauthFixture(t, "lin_client_fixture")
	ofx.ExpiresIn = 600
	h, _, c := linearSignIn(t, ourl, nil)
	ctx := context.Background()
	first, second := startSlowRefresh(t, h, ofx, c.ID)

	if err := h.svc.Disconnect(ctx, human, c.ID); err != nil {
		t.Fatal(err)
	}
	<-first
	<-second
	if _, err := h.vault.Get(ctx, c.CredentialRef); !errors.Is(err, providers.ErrSecretNotFound) {
		t.Fatalf("the disconnected credential is back in the credential store (%v)", err)
	}
	if acc, rej := ofx.RefreshCounts(); acc != 1 || rej != 0 {
		t.Fatalf("refreshes accepted %d, rejected %d; want one", acc, rej)
	}
}

// failingPutVault is a TEST DOUBLE of the credential store: it passes everything to the real (in-memory) vault, except that
// Put fails while failPut is set, and it counts Gets.
type failingPutVault struct {
	inner   providers.SecretStore
	mu      sync.Mutex
	failPut bool
	gets    int
}

func (v *failingPutVault) Put(ctx context.Context, ref, value string) error {
	v.mu.Lock()
	fail := v.failPut
	v.mu.Unlock()
	if fail {
		return errors.New("test double: the credential store refused the write")
	}
	return v.inner.Put(ctx, ref, value)
}

func (v *failingPutVault) Get(ctx context.Context, ref string) (string, error) {
	v.mu.Lock()
	v.gets++
	v.mu.Unlock()
	return v.inner.Get(ctx, ref)
}

func (v *failingPutVault) Delete(ctx context.Context, ref string) error {
	return v.inner.Delete(ctx, ref)
}

func (v *failingPutVault) set(fail bool) {
	v.mu.Lock()
	v.failPut = fail
	v.mu.Unlock()
}

func (v *failingPutVault) getCount() int {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.gets
}

func concurrentChoices(t *testing.T, h *harness, id string, n int) {
	t.Helper()
	errs := make(chan error, n)
	start := make(chan struct{})
	for i := 0; i < n; i++ {
		go func() {
			<-start
			_, err := h.svc.Choices(context.Background(), id, "team", nil)
			errs <- err
		}()
	}
	close(start)
	for i := 0; i < n; i++ {
		if err := <-errs; err != nil {
			t.Errorf("caller %d: %v", i, err)
		}
	}
}

// Linear spends the old refresh token on every refresh. When the renewed sign-in cannot be written to the credential store,
// the callers waiting for that refresh use the renewed token Drydock keeps in memory instead of spending the dead refresh
// token again (which would read as a revoked sign-in), the failure is logged without the token, and the next refresh uses the
// refresh token kept in memory. Calls between refreshes are served from memory, not the credential store.
func TestARenewedSignInThatCannotBeStoredStillServesTheWaiters(t *testing.T) {
	ofx, ourl := oauthFixture(t, "lin_client_fixture")
	ofx.ExpiresIn = 600
	var fv *failingPutVault
	h, _, c := linearSignIn(t, ourl, func(o *integrations.Options) {
		if fv == nil {
			fv = &failingPutVault{inner: o.Vault}
		}
		o.Vault = fv
	})
	ctx := context.Background()
	original := storedRefreshToken(t, h, c.CredentialRef)
	fv.set(true)
	ofx.SetRefreshDelay(50 * time.Millisecond)
	h.clk.Advance(11 * time.Minute)
	concurrentChoices(t, h, c.ID, 8)
	if acc, rej := ofx.RefreshCounts(); acc != 1 || rej != 0 {
		t.Fatalf("refreshes accepted %d, rejected %d; want exactly one", acc, rej)
	}
	if got := storedRefreshToken(t, h, c.CredentialRef); got != original {
		t.Fatal("the test double stored the credential although Put failed")
	}
	logs := h.logs.String()
	if !strings.Contains(logs, "could not be saved") {
		t.Fatalf("the failed write was not logged: %q", logs)
	}
	if strings.Contains(logs, "oat_fixture") || strings.Contains(logs, "ort_fixture") {
		t.Fatal("a token reached the log")
	}

	// between refreshes the token comes from memory
	ofx.SetRefreshDelay(0)
	gets := fv.getCount()
	for i := 0; i < 5; i++ {
		if _, err := h.svc.Choices(ctx, c.ID, "team", nil); err != nil {
			t.Fatal(err)
		}
	}
	if got := fv.getCount(); got != gets {
		t.Fatalf("%d credential store reads for five calls with a fresh token in memory", got-gets)
	}

	// the renewed token expires too: the refresh token kept in memory renews it (the stored one is spent)
	h.clk.Advance(11 * time.Minute)
	if _, err := h.svc.Choices(ctx, c.ID, "team", nil); err != nil {
		t.Fatal(err)
	}
	if acc, rej := ofx.RefreshCounts(); acc != 2 || rej != 0 {
		t.Fatalf("refreshes accepted %d, rejected %d", acc, rej)
	}

	// the credential store works again: the next renewal is stored and survives a restart
	fv.set(false)
	h.clk.Advance(11 * time.Minute)
	if _, err := h.svc.Choices(ctx, c.ID, "team", nil); err != nil {
		t.Fatal(err)
	}
	if stored, _ := h.vault.Get(ctx, c.CredentialRef); !strings.Contains(stored, ofx.LastIssued()) {
		t.Fatal("the renewed sign-in was not stored once the credential store worked again")
	}
	h.restart()
	h.clk.Advance(11 * time.Minute)
	if _, err := h.svc.Choices(ctx, c.ID, "team", nil); err != nil {
		t.Fatal(err)
	}
	if acc, rej := ofx.RefreshCounts(); acc != 4 || rej != 0 {
		t.Fatalf("refreshes accepted %d, rejected %d", acc, rej)
	}
}

// When the renewed sign-in could not be stored and the copy in memory is gone (a reconnect that kept the credential), the
// stored refresh token is known to be spent: Drydock asks for a new sign-in instead of sending it.
func TestASpentStoredRefreshTokenIsNotSentAgain(t *testing.T) {
	ofx, ourl := oauthFixture(t, "lin_client_fixture")
	ofx.ExpiresIn = 600
	var fv *failingPutVault
	h, _, c := linearSignIn(t, ourl, func(o *integrations.Options) {
		fv = &failingPutVault{inner: o.Vault}
		o.Vault = fv
	})
	ctx := context.Background()
	fv.set(true)
	h.clk.Advance(11 * time.Minute)
	if _, err := h.svc.Choices(ctx, c.ID, "team", nil); err != nil {
		t.Fatal(err)
	}
	// a reconnect that changes no credential (the same API address) still drops what Drydock holds in memory
	if _, err := h.svc.Connect(ctx, human, integrations.ConnectReq{ID: c.ID, BaseURL: c.BaseURL}); err != nil {
		t.Fatal(err)
	}
	received := ofx.RefreshesReceived()
	_, err := h.svc.Choices(ctx, c.ID, "team", nil)
	if code := failureCode(err); code != integrations.CodeUnauthorized {
		t.Fatalf("code %q (%v), want %q", code, err, integrations.CodeUnauthorized)
	}
	if ofx.RefreshesReceived() != received {
		t.Fatal("the spent refresh token was sent again")
	}
}

// A vendor may rotate the refresh token (and the expiry) but answer with the same access token. The new refresh token must be
// stored: the old one is spent, and after a restart only the stored one is left.
func TestARotatedRefreshTokenIsStoredEvenWhenTheAccessTokenIsTheSame(t *testing.T) {
	ofx, ourl := oauthFixture(t, "lin_client_fixture")
	ofx.ExpiresIn = 600
	ofx.KeepAccessToken = true
	h, _, c := linearSignIn(t, ourl, nil)
	ctx := context.Background()
	original := storedRefreshToken(t, h, c.CredentialRef)
	h.clk.Advance(11 * time.Minute)
	if rep, _ := h.svc.Test(ctx, c.ID); !rep.OK {
		t.Fatalf("%+v", rep)
	}
	if got := storedRefreshToken(t, h, c.CredentialRef); got == original {
		t.Fatal("the rotated refresh token was not stored")
	}
	h.restart()
	h.clk.Advance(11 * time.Minute)
	if rep, _ := h.svc.Test(ctx, c.ID); !rep.OK {
		t.Fatalf("after a restart: %+v", rep)
	}
	if acc, rej := ofx.RefreshCounts(); acc != 2 || rej != 0 {
		t.Fatalf("refreshes accepted %d, rejected %d", acc, rej)
	}
}

// A caller whose context is cancelled while another caller refreshes the sign-in returns at once instead of waiting for the
// other caller's network call.
func TestACancelledCallerDoesNotWaitForAnotherCallersRefresh(t *testing.T) {
	ofx, ourl := oauthFixture(t, "lin_client_fixture")
	ofx.ExpiresIn = 600
	h, _, c := linearSignIn(t, ourl, nil)
	ofx.SetRefreshDelay(2 * time.Second)
	h.clk.Advance(11 * time.Minute)
	first := make(chan error, 1)
	go func() {
		_, err := h.svc.Choices(context.Background(), c.ID, "team", nil)
		first <- err
	}()
	waitUntil(t, "the refresh to reach the token endpoint", func() bool { return ofx.RefreshesReceived() == 1 })

	cctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := h.svc.Choices(cctx, c.ID, "team", nil)
		done <- err
	}()
	time.Sleep(50 * time.Millisecond) // waiting for the refresh lock
	cancelled := time.Now()
	cancel()
	select {
	case err := <-done:
		if waited := time.Since(cancelled); waited > 500*time.Millisecond {
			t.Fatalf("returned %v after the cancel", waited)
		}
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("err %v, want the cancellation", err)
		}
	case <-time.After(1500 * time.Millisecond):
		t.Fatal("the cancelled caller is still waiting for the other caller's refresh")
	}
	if err := <-first; err != nil {
		t.Fatalf("the caller that refreshed: %v", err)
	}
	if acc, rej := ofx.RefreshCounts(); acc != 1 || rej != 0 {
		t.Fatalf("refreshes accepted %d, rejected %d", acc, rej)
	}
}
