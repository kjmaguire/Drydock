// Command tally runs the Tally API with the demo payment gateway, keeping its data in a snapshot file.
//
//	tally -addr :8080 -data tally.json
package main

import (
	"context"
	"errors"
	"flag"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"example.com/tally/internal/api"
	"example.com/tally/internal/clock"
	"example.com/tally/internal/jobs"
	"example.com/tally/internal/mail"
	"example.com/tally/internal/payments"
	"example.com/tally/internal/snapshot"
	"example.com/tally/internal/store"
	"example.com/tally/internal/subscriptions"
	"example.com/tally/internal/webhooks"
)

func main() {
	addr := flag.String("addr", ":8080", "address to listen on")
	data := flag.String("data", "tally.json", "snapshot file (created when missing)")
	flag.Parse()
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	clk := clock.Real{}

	st, err := snapshot.Load(*data)
	switch {
	case errors.Is(err, os.ErrNotExist):
		st = store.New()
		if err := seed(st, clk.Now(), log); err != nil {
			log.Error("seed", "err", err)
			os.Exit(1)
		}
	case err != nil:
		log.Error("load snapshot", "err", err)
		os.Exit(1)
	}

	hooks := webhooks.New(webhooks.Options{Store: st, Clock: clk})
	pay := &payments.Service{Store: st, Gateway: &payments.Fake{}, Clock: clk, Publisher: hooks}
	subs := &subscriptions.Service{Store: st, Payments: pay, Clock: clk, Publisher: hooks}
	srv := api.New(st, api.Options{Clock: clk, Payments: pay, Subscriptions: subs, Webhooks: hooks, Mail: &mail.Outbox{Log: log}, Logger: log})

	runner := jobs.NewRunner(4, 100, log)
	runner.Start()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		t := time.NewTicker(30 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				_ = runner.Enqueue(jobs.Job{Name: "renewals", Run: func(ctx context.Context) error { _, err := subs.RenewDue(ctx); return err }})
				_ = runner.Enqueue(jobs.Job{Name: "webhooks", Run: func(ctx context.Context) error { _, err := hooks.RunDue(ctx); return err }})
			}
		}
	}()

	hs := &http.Server{Addr: *addr, Handler: srv, ReadHeaderTimeout: 10 * time.Second}
	go func() {
		log.Info("listening", "addr", *addr)
		if err := hs.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("serve", "err", err)
			stop()
		}
	}()
	<-ctx.Done()
	shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = hs.Shutdown(shutdown)
	runner.Stop()
	if err := snapshot.Save(st, *data, clk.Now()); err != nil {
		log.Error("save snapshot", "err", err)
		os.Exit(1)
	}
}

// seed adds a demo organization with an owner and prints the owner's API key.
func seed(st *store.Store, now time.Time, log *slog.Logger) error {
	o, err := st.CreateOrg("Demo Co", store.OrgTeam, "UTC", 0, now)
	if err != nil {
		return err
	}
	u, err := st.CreateUser("owner@demo.example", "Demo Owner", now)
	if err != nil {
		return err
	}
	if _, err := st.AddMember(o.ID, u.ID, store.RoleOwner, now); err != nil {
		return err
	}
	k, err := st.CreateAPIKey(o.ID, u.ID, "Demo key", now)
	if err != nil {
		return err
	}
	log.Info("demo organization created", "org", o.ID, "api_key", k.Token)
	return nil
}
