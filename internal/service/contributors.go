// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725 (Adaléa)

package service

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/fireball1725/librarium-api/internal/models"
	"github.com/fireball1725/librarium-api/internal/providers"
	"github.com/fireball1725/librarium-api/internal/repository"
	"github.com/google/uuid"
)

// ContributorService handles contributor profile enrichment and bibliography management.
type ContributorService struct {
	contributors *repository.ContributorRepo
	books        *repository.BookRepo
	covers       *repository.CoverRepo
	registry     *providers.Registry
	coverPath    string
}

func NewContributorService(
	contributors *repository.ContributorRepo,
	books *repository.BookRepo,
	covers *repository.CoverRepo,
	registry *providers.Registry,
	coverPath string,
) *ContributorService {
	return &ContributorService{
		contributors: contributors,
		books:        books,
		covers:       covers,
		registry:     registry,
		coverPath:    coverPath,
	}
}

// contributorPhotoDir returns the directory where contributor photos are stored.
func (s *ContributorService) contributorPhotoDir(contributorID uuid.UUID) string {
	return filepath.Join(s.coverPath, "contributors", contributorID.String())
}

// ListForLibraryPaged returns a paginated, filtered slice of contributors.
func (s *ContributorService) ListForLibraryPaged(ctx context.Context, libraryID uuid.UUID, opts repository.ContributorListOpts) ([]*models.Contributor, int, error) {
	return s.contributors.ListForLibraryPaged(ctx, libraryID, opts)
}

// LettersForLibrary returns distinct first letters of contributor names in the library.
func (s *ContributorService) LettersForLibrary(ctx context.Context, libraryID uuid.UUID) ([]string, error) {
	return s.contributors.LettersForLibrary(ctx, libraryID)
}

// DeleteContributor hard-deletes a contributor. Returns ErrInUse if they still
// have books anywhere.
func (s *ContributorService) DeleteContributor(ctx context.Context, contributorID uuid.UUID) error {
	return s.contributors.Delete(ctx, contributorID)
}

// UpdateContributorInput holds the fields a caller may change on a contributor.
// Pointer fields: nil = leave unchanged; pointer to empty string = clear.
type UpdateContributorInput struct {
	Name        string  // empty = leave unchanged
	SortName    *string // nil = leave unchanged; "" = re-derive from Name
	IsCorporate *bool   // nil = leave unchanged
	Bio         *string // nil = leave unchanged; "" = clear
	BornDate    *string // nil = leave unchanged; "" = clear; "YYYY-MM-DD" = set
	DiedDate    *string // nil = leave unchanged; "" = clear; "YYYY-MM-DD" = set
	Nationality *string // nil = leave unchanged; "" = clear
}

// UpdateContributor applies the non-nil fields from input and returns the refreshed record.
func (s *ContributorService) UpdateContributor(ctx context.Context, contributorID uuid.UUID, input UpdateContributorInput) (*models.Contributor, error) {
	c, err := s.contributors.FindByID(ctx, contributorID)
	if err != nil {
		return nil, err
	}
	nameChanged := false
	if input.Name != "" && input.Name != c.Name {
		c.Name = input.Name
		nameChanged = true
	}
	if input.IsCorporate != nil {
		c.IsCorporate = *input.IsCorporate
	}
	if input.SortName != nil {
		trimmed := strings.TrimSpace(*input.SortName)
		if trimmed == "" {
			// Empty string → re-derive. Corporate entities keep the display
			// name as their sort key.
			if c.IsCorporate {
				c.SortName = c.Name
			} else {
				c.SortName = DeriveSortName(c.Name)
			}
		} else {
			c.SortName = trimmed
		}
	} else if nameChanged && !c.IsCorporate {
		// Name changed without an explicit sort_name override: auto-refresh
		// the derived sort key so it stays in sync.
		c.SortName = DeriveSortName(c.Name)
	} else if nameChanged && c.IsCorporate {
		c.SortName = c.Name
	}
	if input.Bio != nil {
		c.Bio = *input.Bio
	}
	if input.BornDate != nil {
		if *input.BornDate == "" {
			c.BornDate = nil
		} else {
			for _, layout := range []string{"2006-01-02", "2006-01", "2006"} {
				if t, err := time.Parse(layout, *input.BornDate); err == nil {
					c.BornDate = &t
					break
				}
			}
		}
	}
	if input.DiedDate != nil {
		if *input.DiedDate == "" {
			c.DiedDate = nil
		} else {
			for _, layout := range []string{"2006-01-02", "2006-01", "2006"} {
				if t, err := time.Parse(layout, *input.DiedDate); err == nil {
					c.DiedDate = &t
					break
				}
			}
		}
	}
	if input.Nationality != nil {
		c.Nationality = *input.Nationality
	}
	if err := s.contributors.UpdateProfile(ctx, c); err != nil {
		return nil, err
	}
	return s.contributors.FindByID(ctx, contributorID)
}

// GetContributor returns a contributor with their works and library books annotated for the given library context.
// callerID is used to populate user_read_status on each book; pass uuid.Nil to omit it.
func (s *ContributorService) GetContributor(ctx context.Context, contributorID, libraryID, callerID uuid.UUID) (*models.Contributor, []*models.ContributorWork, []*models.Book, error) {
	c, err := s.contributors.FindByID(ctx, contributorID)
	if err != nil {
		return nil, nil, nil, err
	}
	works, err := s.contributors.ListWorks(ctx, contributorID, libraryID)
	if err != nil {
		return nil, nil, nil, err
	}
	libraryBooks, err := s.books.ListByContributor(ctx, libraryID, contributorID, callerID)
	if err != nil {
		return nil, nil, nil, err
	}
	return c, works, libraryBooks, nil
}

// ─── External metadata search/fetch ──────────────────────────────────────────

// ContributorSearchCandidate is one provider result from a contributor name search.
type ContributorSearchCandidate struct {
	Provider   string
	ExternalID string
	Name       string
	PhotoURL   string
}

// SearchExternal queries all enabled ContributorProviders for a name and returns
// candidates the user can pick from.
func (s *ContributorService) SearchExternal(ctx context.Context, name string) ([]ContributorSearchCandidate, error) {
	ps := s.registry.ContributorProviders()
	if len(ps) == 0 {
		return nil, nil
	}

	type result struct {
		provider string
		items    []*providers.ContributorSearchResult
		err      error
	}

	ch := make(chan result, len(ps))
	for _, p := range ps {
		go func(cp providers.ContributorProvider) {
			items, err := cp.SearchContributors(ctx, name)
			ch <- result{provider: cp.Info().Name, items: items, err: err}
		}(p)
	}

	var out []ContributorSearchCandidate
	for range ps {
		res := <-ch
		if res.err != nil {
			slog.WarnContext(ctx, "contributor search error", "provider", res.provider, "error", res.err)
			continue
		}
		for _, item := range res.items {
			out = append(out, ContributorSearchCandidate{
				Provider:   res.provider,
				ExternalID: item.ExternalID,
				Name:       item.Name,
				PhotoURL:   item.PhotoURL,
			})
		}
	}
	return out, nil
}

// FetchFromProvider fetches full contributor data from a specific provider.
func (s *ContributorService) FetchFromProvider(ctx context.Context, providerName, externalID string) (*providers.ContributorData, error) {
	for _, p := range s.registry.ContributorProviders() {
		if p.Info().Name != providerName {
			continue
		}
		data, err := p.FetchContributor(ctx, externalID)
		if err != nil {
			return nil, fmt.Errorf("fetch from %s: %w", providerName, err)
		}
		return data, nil
	}
	return nil, fmt.Errorf("no enabled contributor provider named %q", providerName)
}

// ─── Apply metadata ───────────────────────────────────────────────────────────

// ApplyMetadata writes provider-sourced fields to a contributor and upserts their works.
// Fields in data are merged: only non-empty values overwrite existing ones.
// The caller decides which fields to apply by zeroing out fields they don't want.
func (s *ContributorService) ApplyMetadata(ctx context.Context, contributorID, callerID uuid.UUID, data *providers.ContributorData) (*models.Contributor, error) {
	c, err := s.contributors.FindByID(ctx, contributorID)
	if err != nil {
		return nil, err
	}

	// Merge non-empty fields.
	if data.Bio != "" {
		c.Bio = data.Bio
	}
	if data.BornDate != nil {
		c.BornDate = data.BornDate
	}
	if data.DiedDate != nil {
		c.DiedDate = data.DiedDate
	}
	if data.Nationality != "" {
		c.Nationality = data.Nationality
	}
	if data.ExternalID != "" && data.Provider != "" {
		if c.ExternalIDs == nil {
			c.ExternalIDs = map[string]string{}
		}
		c.ExternalIDs[data.Provider] = data.ExternalID
	}

	if err := s.contributors.Update(ctx, c); err != nil {
		return nil, fmt.Errorf("updating contributor: %w", err)
	}

	// Download and store photo if a URL is provided.
	if data.PhotoURL != "" {
		if err := s.fetchAndStorePhoto(ctx, contributorID, callerID, data.PhotoURL); err != nil {
			// Photo failure is non-fatal — log and continue.
			slog.ErrorContext(ctx, "contributor photo fetch failed",
				"contributor_id", contributorID, "url", data.PhotoURL, "error", err)
		}
	}

	// Upsert bibliography.
	if len(data.Works) > 0 {
		works := make([]*models.ContributorWork, 0, len(data.Works))
		for _, w := range data.Works {
			works = append(works, &models.ContributorWork{
				Title:       w.Title,
				ISBN13:      w.ISBN13,
				ISBN10:      w.ISBN10,
				PublishYear: w.PublishYear,
				CoverURL:    w.CoverURL,
			})
		}
		if err := s.contributors.UpsertWorks(ctx, contributorID, data.Provider, works); err != nil {
			return nil, fmt.Errorf("upserting works: %w", err)
		}
	}

	// Return fresh contributor (HasPhoto may have changed).
	return s.contributors.FindByID(ctx, contributorID)
}

// DeleteWork soft-deletes a bibliography entry.
func (s *ContributorService) DeleteWork(ctx context.Context, workID uuid.UUID) error {
	return s.contributors.DeleteWork(ctx, workID)
}

// ─── Photo serving ────────────────────────────────────────────────────────────

// GetContributorPhotoPath returns the on-disk path and MIME type for a contributor's photo.
func (s *ContributorService) GetContributorPhotoPath(ctx context.Context, contributorID uuid.UUID) (filePath, mimeType string, err error) {
	cover, err := s.covers.FindPrimary(ctx, "contributor", contributorID)
	if err != nil {
		return "", "", err
	}
	return filepath.Join(s.contributorPhotoDir(contributorID), cover.Filename), cover.MimeType, nil
}

// StorePhotoFromUpload stores an uploaded image as the contributor's primary photo.
func (s *ContributorService) StorePhotoFromUpload(ctx context.Context, contributorID, callerID uuid.UUID, data []byte, mimeType string) error {
	return s.storePhoto(ctx, contributorID, callerID, data, mimeType, "")
}

// fetchAndStorePhoto downloads a photo URL and stores it as the contributor's cover image.
func (s *ContributorService) fetchAndStorePhoto(ctx context.Context, contributorID, callerID uuid.UUID, photoURL string) error {
	slog.InfoContext(ctx, "fetching contributor photo", "contributor_id", contributorID, "url", photoURL)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, photoURL, nil)
	if err != nil {
		return fmt.Errorf("building photo request: %w", err)
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; Librarium/1.0)")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("downloading photo: %w", err)
	}
	defer resp.Body.Close()

	slog.InfoContext(ctx, "photo response", "status", resp.StatusCode, "content_type", resp.Header.Get("Content-Type"), "url", photoURL)

	if resp.StatusCode >= 400 {
		return ErrUpstreamHTTP{StatusCode: resp.StatusCode}
	}

	mime := resp.Header.Get("Content-Type")
	if idx := strings.Index(mime, ";"); idx >= 0 {
		mime = strings.TrimSpace(mime[:idx])
	}
	if mime == "" || !strings.HasPrefix(mime, "image/") {
		mime = "image/jpeg"
	}

	data, err := io.ReadAll(io.LimitReader(resp.Body, 10<<20))
	if err != nil {
		return fmt.Errorf("reading photo body: %w", err)
	}

	slog.InfoContext(ctx, "storing contributor photo", "contributor_id", contributorID, "mime", mime, "bytes", len(data))
	return s.storePhoto(ctx, contributorID, callerID, data, mime, photoURL)
}

func (s *ContributorService) storePhoto(ctx context.Context, contributorID, callerID uuid.UUID, data []byte, mimeType, sourceURL string) error {
	dir := s.contributorPhotoDir(contributorID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("creating photo dir: %w", err)
	}

	// Remove any existing photo records and files.
	existing, err := s.covers.DeleteByEntityID(ctx, "contributor", contributorID)
	if err != nil {
		return fmt.Errorf("removing old photo records: %w", err)
	}
	for _, fn := range existing {
		_ = os.Remove(filepath.Join(dir, fn))
	}

	coverID := uuid.New()
	filename := coverID.String() + extForMime(mimeType)
	filePath := filepath.Join(dir, filename)
	if err := os.WriteFile(filePath, data, 0o644); err != nil {
		return fmt.Errorf("writing photo file: %w", err)
	}

	now := time.Now()
	return s.covers.Insert(ctx, &models.CoverImage{
		ID:         coverID,
		EntityType: "contributor",
		EntityID:   contributorID,
		Filename:   filename,
		MimeType:   mimeType,
		FileSize:   int64(len(data)),
		IsPrimary:  true,
		SourceURL:  sourceURL,
		CreatedBy:  &callerID,
		CreatedAt:  now,
	})
}

// DeleteContributorPhoto removes the contributor's photo from disk and the database.
func (s *ContributorService) DeleteContributorPhoto(ctx context.Context, contributorID uuid.UUID) error {
	filenames, err := s.covers.DeleteByEntityID(ctx, "contributor", contributorID)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return nil
		}
		return err
	}
	dir := s.contributorPhotoDir(contributorID)
	for _, fn := range filenames {
		_ = os.Remove(filepath.Join(dir, fn))
	}
	return nil
}
