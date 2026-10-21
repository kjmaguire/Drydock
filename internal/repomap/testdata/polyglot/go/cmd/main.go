package main

import (
	"fmt"

	"example.com/polyglot/go/api"
	"example.com/polyglot/go/store"
)

func main() {
	s := store.NewStore()
	s.SaveValue("k", "v")
	fmt.Println(api.ServeRequest(s, "k"), s.LookupValue("k"))
}
