// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725 (Adaléa)

package service

import (
	"context"
	"fmt"
	"sort"

	"github.com/fireball1725/librarium-api/internal/models"
	"github.com/fireball1725/librarium-api/internal/repository"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

type SeriesService struct {
	series  *repository.SeriesRepo
	arcs    *repository.SeriesArcRepo
	volumes *repository.SeriesVolumesRepo
	tags    *repository.TagRepo
}

func NewSeriesService(series *repository.SeriesRepo, arcs *repository.SeriesArcRepo, volumes *repository.SeriesVolumesRepo, tags *repository.TagRepo) *SeriesService {
	return &SeriesService{series: series, arcs: arcs, volumes: volumes, tags: tags}
}

type SeriesRequest struct {
	Name             string
	Description      string
	TotalCount       *int
	Status           string // "ongoing"|"completed"|"hiatus"|"cancelled"
	OriginalLanguage string
	PublicationYear  *int
	Demographic      string
	Genres           []string
	URL              string
	ExternalID       string
	ExternalSource   string
	TagIDs           []uuid.UUID
}

func (s *SeriesService) ListSeries(ctx context.Context, libraryID, callerID uuid.UUID, search, tagFilter string) ([]*models.Series, error) {
	return s.series.List(ctx, libraryID, callerID, search, tagFilter)
}

func (s *SeriesService) GetSeries(ctx context.Context, id, callerID uuid.UUID) (*models.Series, error) {
	return s.series.FindByID(ctx, id, callerID)
}

func (s *SeriesService) CreateSeries(ctx context.Context, libraryID, callerID uuid.UUID, req SeriesRequest) (*models.Series, error) {
	if req.Name == "" {
		return nil, fmt.Errorf("name is required")
	}
	ser, err := s.series.Create(ctx, uuid.New(), libraryID, req.Name, req.Description, req.TotalCount, req.Status, req.OriginalLanguage, req.PublicationYear, req.Demographic, req.Genres, req.URL, req.ExternalID, req.ExternalSource, callerID)
	if err != nil {
		return nil, err
	}
	if req.TagIDs != nil {
		if err := s.tags.SetSeriesTags(ctx, ser.ID, req.TagIDs); err != nil {
			return nil, fmt.Errorf("setting series tags: %w", err)
		}
		// Reload to get tags populated
		ser, err = s.series.FindByID(ctx, ser.ID, uuid.Nil)
		if err != nil {
			return nil, err
		}
	}
	return ser, nil
}

func (s *SeriesService) UpdateSeries(ctx context.Context, id uuid.UUID, req SeriesRequest) (*models.Series, error) {
	if req.Name == "" {
		return nil, fmt.Errorf("name is required")
	}
	ser, err := s.series.Update(ctx, id, req.Name, req.Description, req.TotalCount, req.Status, req.OriginalLanguage, req.PublicationYear, req.Demographic, req.Genres, req.URL, req.ExternalID, req.ExternalSource)
	if err != nil {
		return nil, err
	}
	if req.TagIDs != nil {
		if err := s.tags.SetSeriesTags(ctx, ser.ID, req.TagIDs); err != nil {
			return nil, fmt.Errorf("setting series tags: %w", err)
		}
		// Reload to get tags populated
		ser, err = s.series.FindByID(ctx, ser.ID, uuid.Nil)
		if err != nil {
			return nil, err
		}
	}
	return ser, nil
}

func (s *SeriesService) DeleteSeries(ctx context.Context, id uuid.UUID) error {
	return s.series.Delete(ctx, id)
}

func (s *SeriesService) ListSeriesBooks(ctx context.Context, seriesID, callerID uuid.UUID) ([]*models.SeriesEntry, error) {
	return s.series.ListBooks(ctx, seriesID, callerID)
}

// UpsertSeriesBook places a book at a given position within the series. If
// arcID is non-nil it also assigns the book to that arc (or unassigns when
// arcID points at a zero UUID — see handler decoding).
func (s *SeriesService) UpsertSeriesBook(ctx context.Context, seriesID, bookID uuid.UUID, position float64, arcID *uuid.UUID) error {
	if err := s.series.UpsertBook(ctx, seriesID, bookID, position); err != nil {
		return err
	}
	if arcID != nil {
		// Caller wants to set or clear the arc explicitly. A zero UUID means
		// "clear"; otherwise it's the target arc.
		var target *uuid.UUID
		if *arcID != uuid.Nil {
			target = arcID
		}
		if err := s.arcs.SetBookArc(ctx, seriesID, bookID, target); err != nil {
			return err
		}
	}
	return nil
}

func (s *SeriesService) RemoveSeriesBook(ctx context.Context, seriesID, bookID uuid.UUID) error {
	return s.series.RemoveBook(ctx, seriesID, bookID)
}

// ─── Arcs ──────────────────────────────────────────────────────────────────────

type SeriesArcRequest struct {
	Name        string
	Description string
	Position    float64
	VolStart    *float64
	VolEnd      *float64
}

func (s *SeriesService) ListSeriesArcs(ctx context.Context, seriesID uuid.UUID) ([]*models.SeriesArc, error) {
	return s.arcs.List(ctx, seriesID)
}

func (s *SeriesService) CreateSeriesArc(ctx context.Context, seriesID uuid.UUID, req SeriesArcRequest) (*models.SeriesArc, error) {
	if req.Name == "" {
		return nil, fmt.Errorf("name is required")
	}
	return s.arcs.Create(ctx, uuid.New(), seriesID, req.Name, req.Description, req.Position, req.VolStart, req.VolEnd)
}

func (s *SeriesService) UpdateSeriesArc(ctx context.Context, arcID uuid.UUID, req SeriesArcRequest) (*models.SeriesArc, error) {
	if req.Name == "" {
		return nil, fmt.Errorf("name is required")
	}
	return s.arcs.Update(ctx, arcID, req.Name, req.Description, req.Position, req.VolStart, req.VolEnd)
}

func (s *SeriesService) DeleteSeriesArc(ctx context.Context, arcID uuid.UUID) error {
	return s.arcs.Delete(ctx, arcID)
}

func (s *SeriesService) GetSeriesForBook(ctx context.Context, libraryID, bookID uuid.UUID) ([]*models.BookSeriesRef, error) {
	return s.series.GetSeriesForBook(ctx, libraryID, bookID)
}

func (s *SeriesService) ListSeriesVolumes(ctx context.Context, seriesID uuid.UUID) ([]*models.SeriesVolume, error) {
	return s.volumes.List(ctx, seriesID)
}

// MatchCandidate is a library book whose title matches the series name and
// carries a proposed volume position. OtherSeries lists any series the book
// is already a member of so the UI can warn before double-assigning.
type MatchCandidate struct {
	BookID      uuid.UUID
	Title       string
	Subtitle    string
	Position    float64
	OtherSeries []models.BookSeriesRef
}

// MatchCandidates scans every book in the library that is not yet in the
// target series, regex-matches each title against the series name, and
// returns the books with a proposed volume position. Results are sorted by
// position ascending.
func (s *SeriesService) MatchCandidates(ctx context.Context, seriesID uuid.UUID) ([]*MatchCandidate, error) {
	ser, err := s.series.FindByID(ctx, seriesID, uuid.Nil)
	if err != nil {
		return nil, err
	}
	cands, err := s.series.ListMatchCandidates(ctx, ser.LibraryID, seriesID)
	if err != nil {
		return nil, err
	}
	out := make([]*MatchCandidate, 0, len(cands))
	for _, c := range cands {
		pos, ok := MatchTitleToSeries(c.Title, ser.Name)
		if !ok {
			continue
		}
		out = append(out, &MatchCandidate{
			BookID:      c.BookID,
			Title:       c.Title,
			Subtitle:    c.Subtitle,
			Position:    pos,
			OtherSeries: c.OtherSeries,
		})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Position < out[j].Position })
	return out, nil
}

// SeriesMatchApply is one entry in a bulk auto-match apply request.
type SeriesMatchApply struct {
	BookID   uuid.UUID
	Position float64
}

// ApplyMatches upserts each (book, position) into the series. Returns the
// number of rows successfully written; stops on the first error.
func (s *SeriesService) ApplyMatches(ctx context.Context, seriesID uuid.UUID, matches []SeriesMatchApply) (int, error) {
	n := 0
	for _, m := range matches {
		if err := s.series.UpsertBook(ctx, seriesID, m.BookID, m.Position); err != nil {
			return n, fmt.Errorf("applying match (book=%s, pos=%g): %w", m.BookID, m.Position, err)
		}
		n++
	}
	return n, nil
}

// SuggestedBook is a book that the suggester believes belongs to a proposed
// series, including its detected volume position.
type SuggestedBook struct {
	BookID    uuid.UUID
	Title     string
	Subtitle  string
	Position  float64
	HasCover  bool
	CreatedAt pgtype.Timestamptz
}

// SeriesSuggestion is a proposed new series derived from orphan books whose
// titles share a base name plus volume numbering.
type SeriesSuggestion struct {
	ProposedName string
	Books        []*SuggestedBook
}

// SuggestSeries scans the library for books not in any series, strips volume
// suffixes from each title, groups the results by normalized base name, and
// returns every group with 2 or more members. Groups are sorted by member
// count descending, then by proposed name.
func (s *SeriesService) SuggestSeries(ctx context.Context, libraryID uuid.UUID, mediaTypes []string) ([]*SeriesSuggestion, error) {
	orphans, err := s.series.ListOrphanBooks(ctx, libraryID, mediaTypes)
	if err != nil {
		return nil, err
	}

	groups := make(map[string]*SeriesSuggestion)
	nameVotes := make(map[string]map[string]int) // key -> original base -> count

	for _, ob := range orphans {
		base, pos, ok := ExtractSeriesBase(ob.Title)
		if !ok {
			continue
		}
		key := NormalizeSeriesKey(base)
		if key == "" {
			continue
		}
		sug, exists := groups[key]
		if !exists {
			sug = &SeriesSuggestion{ProposedName: base}
			groups[key] = sug
			nameVotes[key] = make(map[string]int)
		}
		nameVotes[key][base]++
		sug.Books = append(sug.Books, &SuggestedBook{
			BookID:    ob.BookID,
			Title:     ob.Title,
			Subtitle:  ob.Subtitle,
			Position:  pos,
			HasCover:  ob.HasCover,
			CreatedAt: ob.CreatedAt,
		})
	}

	out := make([]*SeriesSuggestion, 0, len(groups))
	for key, g := range groups {
		if len(g.Books) < 2 {
			continue
		}
		// Choose the most common original-case base as the display name.
		best, bestCount := g.ProposedName, 0
		for name, c := range nameVotes[key] {
			if c > bestCount {
				best, bestCount = name, c
			}
		}
		g.ProposedName = best
		sort.SliceStable(g.Books, func(i, j int) bool { return g.Books[i].Position < g.Books[j].Position })
		out = append(out, g)
	}

	sort.SliceStable(out, func(i, j int) bool {
		if len(out[i].Books) != len(out[j].Books) {
			return len(out[i].Books) > len(out[j].Books)
		}
		return out[i].ProposedName < out[j].ProposedName
	})
	return out, nil
}

// SeriesBulkCreateBook pairs a library book with its position in a proposed series.
type SeriesBulkCreateBook struct {
	BookID   uuid.UUID
	Position float64
}

// SeriesBulkCreateItem is a single proposed series to create in bulk.
type SeriesBulkCreateItem struct {
	Name  string
	Books []SeriesBulkCreateBook
}

// BulkCreateSeries creates each proposed series and upserts its member books.
// Skips items with an empty name or no books. Stops on the first error and
// returns everything created up to that point.
func (s *SeriesService) BulkCreateSeries(ctx context.Context, libraryID, callerID uuid.UUID, items []SeriesBulkCreateItem) ([]*models.Series, error) {
	out := make([]*models.Series, 0, len(items))
	for _, item := range items {
		if item.Name == "" || len(item.Books) == 0 {
			continue
		}
		ser, err := s.series.Create(ctx, uuid.New(), libraryID, item.Name, "", nil, "ongoing", "", nil, "", nil, "", "", "", callerID)
		if err != nil {
			return out, fmt.Errorf("creating series %q: %w", item.Name, err)
		}
		for _, b := range item.Books {
			if err := s.series.UpsertBook(ctx, ser.ID, b.BookID, b.Position); err != nil {
				return out, fmt.Errorf("adding book %s to series %q: %w", b.BookID, item.Name, err)
			}
		}
		reloaded, err := s.series.FindByID(ctx, ser.ID, uuid.Nil)
		if err == nil && reloaded != nil {
			ser = reloaded
		}
		out = append(out, ser)
	}
	return out, nil
}
