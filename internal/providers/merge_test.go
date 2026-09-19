// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725

package providers

import (
	"bytes"
	"context"
	"image"
	"image/png"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"
)

func answer(provider string, set func(*BookResult)) *BookResult {
	r := &BookResult{Provider: provider, ProviderDisplay: provider}
	set(r)
	return r
}

func TestMerge_AgreementBeatsArrivalOrder(t *testing.T) {
	m := MergeBookResults([]*BookResult{
		answer("fast", func(r *BookResult) { r.Publisher = "Ace Books" }),
		answer("b", func(r *BookResult) { r.Publisher = "ace  books " }), // same after case and spacing
		answer("c", func(r *BookResult) { r.Publisher = "Chilton" }),
		answer("d", func(r *BookResult) { r.Publisher = "Chilton Books" }),
	})
	p := m.Publisher
	if p.Value != "Ace Books" || p.Reason != ReasonAgreed {
		t.Fatalf("publisher = %q (%s), want the agreed value", p.Value, p.Reason)
	}
	if !reflect.DeepEqual(p.Sources, []string{"fast", "b"}) {
		t.Errorf("sources = %v", p.Sources)
	}
	if len(p.Alternatives) != 2 {
		t.Errorf("want one alternative per distinct value, got %+v", p.Alternatives)
	}
}

// The first answer to arrive used to win every field; now a slow provider
// that agrees with others outvotes it.
func TestMerge_SlowMajorityWins(t *testing.T) {
	m := MergeBookResults([]*BookResult{
		answer("fast", func(r *BookResult) { r.Title = "Dune (Movie Tie-In)" }),
		answer("slow1", func(r *BookResult) { r.Title = "Dune" }),
		answer("slow2", func(r *BookResult) { r.Title = "Dune" }),
	})
	if m.Title.Value != "Dune" || m.Title.Source != "slow1" {
		t.Errorf("title = %q from %s", m.Title.Value, m.Title.Source)
	}
}

func TestMerge_SingleAnswerIsOnly(t *testing.T) {
	m := MergeBookResults([]*BookResult{
		answer("a", func(r *BookResult) { r.Subtitle = "A Novel" }),
		answer("b", func(r *BookResult) {}),
	})
	if m.Subtitle.Reason != ReasonOnly || len(m.Subtitle.Alternatives) != 0 {
		t.Errorf("subtitle = %+v", m.Subtitle)
	}
	if m.Title != nil {
		t.Errorf("no provider had a title, got %+v", m.Title)
	}
}

func TestMerge_NoAgreementUsesTheFieldsRule(t *testing.T) {
	m := MergeBookResults([]*BookResult{
		answer("a", func(r *BookResult) {
			r.Description = "Short."
			r.PublishDate = "1965"
			r.Publisher = "Chilton"
		}),
		answer("b", func(r *BookResult) {
			r.Description = "A much longer description of the book."
			r.PublishDate = "1965-08-01"
			r.Publisher = "Ace"
		}),
		answer("c", func(r *BookResult) {
			r.PublishDate = "August 1965"
		}),
	})
	if m.Description.Source != "b" || m.Description.Reason != ReasonLongest {
		t.Errorf("description from %s (%s)", m.Description.Source, m.Description.Reason)
	}
	if m.PublishDate.Value != "1965-08-01" || m.PublishDate.Reason != ReasonMostDetail {
		t.Errorf("date = %q (%s)", m.PublishDate.Value, m.PublishDate.Reason)
	}
	if m.Publisher.Value != "Chilton" || m.Publisher.Reason != ReasonFirst {
		t.Errorf("publisher = %q (%s)", m.Publisher.Value, m.Publisher.Reason)
	}
}

// A 2-2 tie isn't agreement; the field's rule picks between the tied values.
func TestMerge_TiedMajoritiesFallBackToTheRule(t *testing.T) {
	m := MergeBookResults([]*BookResult{
		answer("a", func(r *BookResult) { r.Description = "Short." }),
		answer("b", func(r *BookResult) { r.Description = "Short." }),
		answer("c", func(r *BookResult) { r.Description = "The longer one." }),
		answer("d", func(r *BookResult) { r.Description = "The longer one." }),
	})
	if m.Description.Value != "The longer one." || m.Description.Reason != ReasonLongest {
		t.Errorf("description = %q (%s)", m.Description.Value, m.Description.Reason)
	}
}

func TestSortCoversBySize(t *testing.T) {
	m := &MergedBookResult{CoverReason: ReasonFirst, Covers: []CoverOption{
		{Source: "placeholder", Width: 1, Height: 1},
		{Source: "unknown"},
		{Source: "big", Width: 600, Height: 900},
	}}
	m.SortCoversBySize()
	got := []string{m.Covers[0].Source, m.Covers[1].Source, m.Covers[2].Source}
	if !reflect.DeepEqual(got, []string{"big", "placeholder", "unknown"}) || m.CoverReason != ReasonLargest {
		t.Errorf("order = %v (%s)", got, m.CoverReason)
	}

	unsized := &MergedBookResult{CoverReason: ReasonFirst, Covers: []CoverOption{{Source: "a"}, {Source: "b"}}}
	unsized.SortCoversBySize()
	if unsized.Covers[0].Source != "a" || unsized.CoverReason != ReasonFirst {
		t.Errorf("with no sizes the answer order and reason stay, got %+v", unsized)
	}
}

func TestProbeCoverSizes(t *testing.T) {
	var buf bytes.Buffer
	if err := png.Encode(&buf, image.NewRGBA(image.Rect(0, 0, 40, 60))); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/cover.png":
			_, _ = w.Write(buf.Bytes())
		case "/text":
			_, _ = w.Write([]byte("not an image"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	covers := []CoverOption{{CoverURL: srv.URL + "/cover.png"}, {CoverURL: srv.URL + "/text"}, {CoverURL: srv.URL + "/missing"}}
	ProbeCoverSizes(context.Background(), srv.Client(), covers)
	if covers[0].Width != 40 || covers[0].Height != 60 {
		t.Errorf("png size = %dx%d", covers[0].Width, covers[0].Height)
	}
	if covers[1].Width != 0 || covers[2].Width != 0 {
		t.Errorf("unreadable covers should stay unknown: %+v", covers[1:])
	}
}

// fakeReportProvider answers after delay with a book, no record, or an error.
type fakeReportProvider struct {
	name    string
	delay   time.Duration
	outcome string
}

func (p *fakeReportProvider) Info() ProviderInfo {
	return ProviderInfo{Name: p.name, DisplayName: p.name, Capabilities: []string{CapBookISBN}}
}
func (p *fakeReportProvider) Configure(map[string]string) {}
func (p *fakeReportProvider) Enabled() bool               { return true }
func (p *fakeReportProvider) LookupByISBN(ctx context.Context, _ string) (*BookResult, error) {
	select {
	case <-time.After(p.delay):
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	switch p.outcome {
	case StatusNoRecord:
		return nil, nil
	case StatusError:
		return nil, context.DeadlineExceeded
	}
	return &BookResult{Provider: p.name, Title: p.name}, nil
}

func TestLookupISBNReport_SaysWhatEachProviderDid(t *testing.T) {
	withISBNDeadline(t, 50*time.Millisecond)

	r := NewRegistry()
	r.Register(&fakeReportProvider{name: "hit", outcome: StatusAnswered})
	r.Register(&fakeReportProvider{name: "none", outcome: StatusNoRecord})
	r.Register(&fakeReportProvider{name: "broken", outcome: StatusError})
	r.Register(&fakeReportProvider{name: "slow", delay: 5 * time.Second, outcome: StatusAnswered})

	results, statuses := r.LookupISBNReport(context.Background(), "9780441172719")
	if len(results) != 1 || results[0].Provider != "hit" {
		t.Fatalf("results = %+v", results)
	}
	want := map[string]string{"hit": StatusAnswered, "none": StatusNoRecord, "broken": StatusError, "slow": StatusMissed}
	for _, s := range statuses {
		if s.Status != want[s.Name] {
			t.Errorf("%s: status %s, want %s", s.Name, s.Status, want[s.Name])
		}
	}
	if statuses[0].Name != "hit" || len(statuses) != 4 {
		t.Errorf("statuses should keep the asking order: %+v", statuses)
	}
}

// Every provider slow: the overall deadline still ends the lookup.
func TestLookupISBNReport_OverallDeadline(t *testing.T) {
	orig := lookupOverallDeadline
	lookupOverallDeadline = 50 * time.Millisecond
	t.Cleanup(func() { lookupOverallDeadline = orig })

	r := NewRegistry()
	r.Register(&fakeReportProvider{name: "slow", delay: 5 * time.Second})

	start := time.Now()
	_, statuses := r.LookupISBNReport(context.Background(), "9780441172719")
	if time.Since(start) > 2*time.Second {
		t.Fatalf("waited %v past the overall deadline", time.Since(start))
	}
	if statuses[0].Status != StatusMissed {
		t.Errorf("status = %s", statuses[0].Status)
	}
}
