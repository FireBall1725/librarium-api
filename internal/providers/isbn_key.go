// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725

package providers

import "strings"

// BarcodeKey is the form a barcode is remembered under: digits only, and an
// ISBN-10 as its ISBN-13, so a lookup by one form and a save by the other
// meet on the same key.
func BarcodeKey(code string) string {
	code = strings.ToUpper(strings.NewReplacer("-", "", " ", "").Replace(strings.TrimSpace(code)))
	if len(code) != 10 {
		return code
	}
	body := "978" + code[:9]
	sum := 0
	for i, c := range body {
		if c < '0' || c > '9' {
			return code
		}
		d := int(c - '0')
		if i%2 == 1 {
			d *= 3
		}
		sum += d
	}
	return body + string(rune('0'+(10-sum%10)%10))
}
