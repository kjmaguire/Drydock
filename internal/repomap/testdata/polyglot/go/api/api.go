package api

import "example.com/polyglot/go/store"

// ServeRequest answers one request from the store.
func ServeRequest(s *store.Store, key string) string {
	return s.LookupValue(key)
}

func seedDefaults() *store.Store {
	s := store.NewStore()
	s.SaveValue("greeting", "hello")
	return s
}
