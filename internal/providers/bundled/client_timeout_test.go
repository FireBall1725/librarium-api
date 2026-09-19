// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725

package bundled

import (
	"net/http"
	"reflect"
	"testing"
)

// offline providers make no HTTP calls, so they're allowed no client.
var offline = map[string]bool{"test": true}

// TestProviderClientsAreBounded pins the BookSearchProvider contract: every
// provider must bound its own HTTP calls.
//
// Registry.SearchBooks starts its deadline only after some provider has
// already responded, so before the first result the sole backstops are a
// provider returning and the request context being cancelled. A provider
// built on a zero-value http.Client has no timeout at all, and one hung
// connection then stalls every search that includes it for as long as the
// caller is willing to wait.
//
// The list comes from All, which main.go registers from, so a new provider is
// covered the moment it ships. The client is an unexported field, so it's
// found by type rather than by name.
func TestProviderClientsAreBounded(t *testing.T) {
	for _, p := range All() {
		name := p.Info().Name
		t.Run(name, func(t *testing.T) {
			clients := httpClients(reflect.ValueOf(p))
			if len(clients) == 0 && !offline[name] {
				t.Fatal("provider holds no http.Client; if it makes no HTTP calls, add it to offline")
			}
			for _, c := range clients {
				if c.IsNil() {
					t.Error("provider has a nil http.Client")
					continue
				}
				if c.Elem().FieldByName("Timeout").Int() <= 0 {
					t.Error("http.Client has no timeout; an unbounded provider stalls every search it takes part in")
				}
			}
		})
	}
}

// httpClients returns every *http.Client field in v's struct, including
// those in embedded structs.
func httpClients(v reflect.Value) []reflect.Value {
	for v.Kind() == reflect.Pointer || v.Kind() == reflect.Interface {
		if v.IsNil() {
			return nil
		}
		v = v.Elem()
	}
	if v.Kind() != reflect.Struct {
		return nil
	}
	clientType := reflect.TypeFor[*http.Client]()
	var out []reflect.Value
	for i := range v.NumField() {
		f := v.Field(i)
		switch {
		case f.Type() == clientType:
			out = append(out, f)
		case v.Type().Field(i).Anonymous:
			out = append(out, httpClients(f)...)
		}
	}
	return out
}
