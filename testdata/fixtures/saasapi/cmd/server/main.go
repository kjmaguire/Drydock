// Command server runs the API with demo data.
package main

import (
	"log"
	"net/http"
	"os"
	"time"

	"example.com/saasapi/internal/api"
	"example.com/saasapi/internal/store"
)

func main() {
	st := store.New()
	st.AddAccount(store.Account{ID: "acc_acme", Name: "Acme", Plan: store.PlanFree, TaxRateBP: 825})
	st.AddAccount(store.Account{ID: "acc_globex", Name: "Globex", Plan: store.PlanPro, TaxRateBP: 2000})
	st.AddKey("demo-acme", "acc_acme")
	st.AddKey("demo-globex", "acc_globex")
	if _, err := st.CreateCustomer("acc_acme", "Jane Doe", "jane@example.com", store.PlanPro, time.Now()); err != nil {
		log.Fatal(err)
	}
	addr := ":8080"
	if p := os.Getenv("PORT"); p != "" {
		addr = ":" + p
	}
	srv := &http.Server{Addr: addr, Handler: api.New(st, api.Options{}), ReadHeaderTimeout: 10 * time.Second}
	log.Printf("listening on %s", addr)
	log.Fatal(srv.ListenAndServe())
}
