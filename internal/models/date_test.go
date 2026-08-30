// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725

package models

import "testing"

// TestParseFlexDatePrecision pins the half of the result that used to be
// thrown away. A year-only date is stored as 1 January, so without the
// precision beside it nothing downstream can tell "1932" from a book published
// on 1 January 1932.
func TestParseFlexDatePrecision(t *testing.T) {
	tests := []struct {
		in        string
		want      string
		precision DatePrecision
		ok        bool
	}{
		{in: "1932-05-14", want: "1932-05-14", precision: DatePrecisionDay, ok: true},
		{in: "1932-05", want: "1932-05-01", precision: DatePrecisionMonth, ok: true},
		{in: "1932", want: "1932-01-01", precision: DatePrecisionYear, ok: true},
		{in: "  1932  ", want: "1932-01-01", precision: DatePrecisionYear, ok: true},
		{in: "January 1932", want: "1932-01-01", precision: DatePrecisionMonth, ok: true},
		{in: "Jan 1932", want: "1932-01-01", precision: DatePrecisionMonth, ok: true},
		{in: "January 2, 1932", want: "1932-01-02", precision: DatePrecisionDay, ok: true},
		{in: "Jan 2, 1932", want: "1932-01-02", precision: DatePrecisionDay, ok: true},
		{in: "2 January 1932", want: "1932-01-02", precision: DatePrecisionDay, ok: true},
		{in: "14 Jan 1932", want: "1932-01-14", precision: DatePrecisionDay, ok: true},
		{in: "", ok: false},
		{in: "sometime in the thirties", ok: false},
	}

	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			got, precision, ok := ParseFlexDate(tt.in)
			if ok != tt.ok {
				t.Fatalf("ok = %t, want %t", ok, tt.ok)
			}
			if !ok {
				if precision != "" {
					t.Errorf("precision = %q on a failed parse, want empty", precision)
				}
				return
			}
			if formatted := got.Format("2006-01-02"); formatted != tt.want {
				t.Errorf("date = %s, want %s", formatted, tt.want)
			}
			if precision != tt.precision {
				t.Errorf("precision = %q, want %q", precision, tt.precision)
			}
		})
	}
}

// TestParseFlexDatePrecisionIsNeverEmptyOnSuccess guards the invariant the
// database constraint depends on: a date always arrives with a precision, so
// publish_date and publish_date_precision are written or omitted together.
func TestParseFlexDatePrecisionIsNeverEmptyOnSuccess(t *testing.T) {
	for _, in := range []string{"1932", "1932-05", "1932-05-14", "May 1932", "1 May 1932"} {
		if _, precision, ok := ParseFlexDate(in); ok && precision == "" {
			t.Errorf("ParseFlexDate(%q) succeeded with no precision", in)
		}
	}
}
