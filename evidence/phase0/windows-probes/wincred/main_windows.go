//go:build windows

// wincred probes Windows Credential Manager through zalando/go-keyring: round trip, size limit, and error shapes.
package main

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/zalando/go-keyring"
)

func main() {
	fail := false
	check := func(name string, err error, wantErr error) {
		ok := (wantErr == nil && err == nil) || (wantErr != nil && errors.Is(err, wantErr))
		fmt.Printf("%-34s ok=%v err=%v\n", name, ok, err)
		if !ok {
			fail = true
		}
	}
	svc, user := "drydock-probe", "probe-user"
	check("set small", keyring.Set(svc, user, "s3cret"), nil)
	v, err := keyring.Get(svc, user)
	check("get small", err, nil)
	fmt.Println("roundtrip equal:", v == "s3cret")
	check("set 2560 bytes", keyring.Set(svc, user, strings.Repeat("a", 2560)), nil)
	v, err = keyring.Get(svc, user)
	check("get 2560 bytes", err, nil)
	fmt.Println("len after get:", len(v))
	check("set 2561 bytes (expect too big)", keyring.Set(svc, user, strings.Repeat("a", 2561)), keyring.ErrSetDataTooBig)
	check("delete", keyring.Delete(svc, user), nil)
	_, err = keyring.Get(svc, user)
	check("get after delete (expect not found)", err, keyring.ErrNotFound)
	if fail {
		os.Exit(1)
	}
}
