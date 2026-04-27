// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725 (Adaléa)

// Package responses contains named structs used exclusively as swaggo/swag
// annotation targets. They mirror the JSON shapes produced by the handler
// body-helper functions so that the generated OpenAPI spec has proper schemas
// instead of empty `{}` objects.
//
// These types are NOT used at runtime — handlers still build their responses
// via the existing map[string]any helpers. Only the documentation annotations
// reference this package.
package responses

import (
	"time"

	"github.com/google/uuid"
)

// ─── Shared primitives ────────────────────────────────────────────────────────

// TagRef is a lightweight tag embedded in books, shelves, loans, series, and members.
type TagRef struct {
	ID        uuid.UUID `json:"id"`
	Name      string    `json:"name"`
	Color     string    `json:"color"`
	LibraryID uuid.UUID `json:"library_id,omitempty"`
	CreatedAt time.Time `json:"created_at,omitempty"`
}

// ContributorRef is a contributor embedded in a book response.
type ContributorRef struct {
	ContributorID uuid.UUID `json:"contributor_id"`
	Name          string    `json:"name"`
	Role          string    `json:"role"`
	DisplayOrder  int       `json:"display_order"`
}

// GenreRef is a genre embedded in a book response.
type GenreRef struct {
	ID   uuid.UUID `json:"id"`
	Name string    `json:"name"`
}

// SeriesRef is a series membership embedded in a book response.
type SeriesRef struct {
	SeriesID   uuid.UUID `json:"series_id"`
	SeriesName string    `json:"series_name"`
	Position   float64   `json:"position"`
}

// ShelfRef is a shelf reference embedded in a book response.
type ShelfRef struct {
	ID   uuid.UUID `json:"id"`
	Name string    `json:"name"`
}

// ─── Auth ─────────────────────────────────────────────────────────────────────

// UserResponse is the public user shape returned by auth and profile endpoints.
type UserResponse struct {
	ID              uuid.UUID `json:"id"`
	Username        string    `json:"username"`
	Email           string    `json:"email"`
	DisplayName     string    `json:"display_name"`
	IsInstanceAdmin bool      `json:"is_instance_admin"`
}

// AuthResponse is returned after a successful login or token refresh.
type AuthResponse struct {
	AccessToken  string       `json:"access_token"`
	RefreshToken string       `json:"refresh_token"`
	ExpiresIn    int          `json:"expires_in"`
	User         UserResponse `json:"user"`
}

// PreferencesResponse is returned by GET /auth/me/preferences.
type PreferencesResponse struct {
	Prefs map[string]interface{} `json:"prefs"`
}

// ─── Admin ────────────────────────────────────────────────────────────────────

// AdminUserResponse is returned by admin user CRUD endpoints.
type AdminUserResponse struct {
	ID              uuid.UUID  `json:"id"`
	Username        string     `json:"username"`
	Email           string     `json:"email"`
	DisplayName     string     `json:"display_name"`
	IsActive        bool       `json:"is_active"`
	IsInstanceAdmin bool       `json:"is_instance_admin"`
	CreatedAt       time.Time  `json:"created_at"`
	LastLoginAt     *time.Time `json:"last_login_at"`
}

// AdminUsersPage is the paginated response from GET /admin/users.
type AdminUsersPage struct {
	Items   []AdminUserResponse `json:"items"`
	Total   int                 `json:"total"`
	Page    int                 `json:"page"`
	PerPage int                 `json:"per_page"`
}

// ─── Libraries ────────────────────────────────────────────────────────────────

// LibraryResponse is returned by library CRUD endpoints.
type LibraryResponse struct {
	ID          uuid.UUID `json:"id"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	Slug        string    `json:"slug"`
	OwnerID     uuid.UUID `json:"owner_id"`
	IsPublic    bool      `json:"is_public"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
	// BookCount is the total number of books in the library. ReadingCount
	// and ReadCount are caller-scoped — the calling user's reading + read
	// books in this library. Always populated on list endpoints; on single-
	// library endpoints they fall back to 0 unless the path was scoped.
	BookCount    int `json:"book_count"`
	ReadingCount int `json:"reading_count"`
	ReadCount    int `json:"read_count"`
}

// MemberResponse is returned by library member endpoints.
type MemberResponse struct {
	UserID      uuid.UUID  `json:"user_id"`
	Username    string     `json:"username"`
	DisplayName string     `json:"display_name"`
	Email       string     `json:"email"`
	RoleID      uuid.UUID  `json:"role_id"`
	Role        string     `json:"role"`
	JoinedAt    time.Time  `json:"joined_at"`
	InvitedBy   *uuid.UUID `json:"invited_by,omitempty"`
	Tags        []TagRef   `json:"tags"`
}

// ─── Books ────────────────────────────────────────────────────────────────────

// BookResponse is returned by book CRUD endpoints.
type BookResponse struct {
	ID           uuid.UUID        `json:"id"`
	LibraryID    uuid.UUID        `json:"library_id"`
	Title        string           `json:"title"`
	Subtitle     string           `json:"subtitle"`
	MediaTypeID  uuid.UUID        `json:"media_type_id"`
	MediaType    string           `json:"media_type"`
	Description  string           `json:"description"`
	Contributors []ContributorRef `json:"contributors"`
	Tags         []TagRef         `json:"tags"`
	Genres       []GenreRef       `json:"genres"`
	CoverURL     *string          `json:"cover_url"`
	Publisher    string           `json:"publisher"`
	PublishYear  *int             `json:"publish_year"`
	Language     string           `json:"language"`
	Series       []SeriesRef      `json:"series"`
	Shelves      []ShelfRef       `json:"shelves"`
	AddedBy      *uuid.UUID       `json:"added_by,omitempty"`
	// UserRating is the caller's rating (1-10 half-star integer; 0 = none).
	UserRating int `json:"user_rating"`
	// UserProgressPct is the caller's reading progress 0-100 (0 = none).
	UserProgressPct float64 `json:"user_progress_pct"`
	// ActiveLoanCount is the number of active (not yet returned) loans for
	// this book — scoped to the library when the read is library-scoped,
	// global otherwise. Always populated.
	ActiveLoanCount int `json:"active_loan_count"`
	// ActiveLoans is the full list of active loans for this book. Only
	// populated by single-book reads (GetBook); list endpoints omit it to
	// keep payloads lean — use active_loan_count there for the badge.
	ActiveLoans []LoanResponse `json:"active_loans,omitempty"`
	CreatedAt   time.Time      `json:"created_at"`
	UpdatedAt   time.Time      `json:"updated_at"`
}

// PagedBooksResponse is the paginated response from book list endpoints.
type PagedBooksResponse struct {
	Items   []BookResponse `json:"items"`
	Total   int            `json:"total"`
	Page    int            `json:"page"`
	PerPage int            `json:"per_page"`
}

// LettersResponse is returned by GET .../books/letters.
type LettersResponse []string

// ContributorItem is returned by the contributors list endpoint.
type ContributorItem struct {
	ID   uuid.UUID `json:"id"`
	Name string    `json:"name"`
}

// ─── Editions & Interactions ──────────────────────────────────────────────────

// EditionResponse is returned by edition endpoints.
type EditionResponse struct {
	ID              uuid.UUID `json:"id"`
	BookID          uuid.UUID `json:"book_id"`
	Format          string    `json:"format"`
	Language        string    `json:"language"`
	EditionName     string    `json:"edition_name"`
	Narrator        string    `json:"narrator"`
	Publisher       string    `json:"publisher"`
	PublishDate     *string   `json:"publish_date"`     // YYYY-MM-DD
	ISBN10          string    `json:"isbn_10"`
	ISBN13          string    `json:"isbn_13"`
	CopyCount       int       `json:"copy_count"`
	Description     string    `json:"description"`
	IsPrimary       bool      `json:"is_primary"`
	PageCount       *int      `json:"page_count"`
	DurationSeconds *int      `json:"duration_seconds"`
	AcquiredAt      *string   `json:"acquired_at"` // YYYY-MM-DD
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}

// InteractionResponse is returned by book reading-progress endpoints.
type InteractionResponse struct {
	ID            uuid.UUID `json:"id"`
	UserID        uuid.UUID `json:"user_id"`
	BookEditionID uuid.UUID `json:"book_edition_id"`
	ReadStatus    string    `json:"read_status"`
	Rating        *int      `json:"rating"`
	Notes         string    `json:"notes"`
	Review        string    `json:"review"`
	DateStarted   *string   `json:"date_started"`  // YYYY-MM-DD
	DateFinished  *string   `json:"date_finished"` // YYYY-MM-DD
	IsFavorite    bool      `json:"is_favorite"`
	RereadCount   int       `json:"reread_count"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

// ─── Shelves & Tags ───────────────────────────────────────────────────────────

// ShelfResponse is returned by shelf endpoints.
type ShelfResponse struct {
	ID           uuid.UUID `json:"id"`
	LibraryID    uuid.UUID `json:"library_id"`
	Name         string    `json:"name"`
	Description  string    `json:"description"`
	Color        string    `json:"color"`
	Icon         string    `json:"icon"`
	DisplayOrder int       `json:"display_order"`
	BookCount    int       `json:"book_count"`
	Tags         []TagRef  `json:"tags"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// TagResponse is returned by tag endpoints.
type TagResponse struct {
	ID        uuid.UUID `json:"id"`
	LibraryID uuid.UUID `json:"library_id"`
	Name      string    `json:"name"`
	Color     string    `json:"color"`
	CreatedAt time.Time `json:"created_at"`
}

// ─── Series ───────────────────────────────────────────────────────────────────

// SeriesResponse is returned by series CRUD endpoints.
type SeriesResponse struct {
	ID               uuid.UUID `json:"id"`
	LibraryID        uuid.UUID `json:"library_id"`
	Name             string    `json:"name"`
	Description      string    `json:"description"`
	Status           string    `json:"status"`
	IsComplete       bool      `json:"is_complete"`
	OriginalLanguage string    `json:"original_language"`
	Demographic      string    `json:"demographic"`
	TotalCount       *int      `json:"total_count"`
	PublicationYear  *int      `json:"publication_year"`
	LastReleaseDate  *string   `json:"last_release_date"` // YYYY-MM-DD
	NextReleaseDate  *string   `json:"next_release_date"` // YYYY-MM-DD
	Genres           []string  `json:"genres"`
	URL              string    `json:"url"`
	ExternalID       string    `json:"external_id"`
	ExternalSource   string    `json:"external_source"`
	BookCount        int       `json:"book_count"`
	Tags             []TagRef  `json:"tags"`
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`
}

// SeriesEntryResponse is a book entry within a series list.
type SeriesEntryResponse struct {
	Position     float64          `json:"position"`
	BookID       uuid.UUID        `json:"book_id"`
	Title        string           `json:"title"`
	Subtitle     string           `json:"subtitle"`
	MediaType    string           `json:"media_type"`
	Contributors []ContributorRef `json:"contributors"`
}

// SeriesVolumeResponse is an upcoming/released volume from an external source.
type SeriesVolumeResponse struct {
	ID          uuid.UUID `json:"id"`
	SeriesID    uuid.UUID `json:"series_id"`
	Position    float64   `json:"position"`
	Title       string    `json:"title"`
	ReleaseDate *string   `json:"release_date"` // YYYY-MM-DD
	CoverURL    string    `json:"cover_url"`
	ExternalID  string    `json:"external_id"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// ─── Genres & Media Types ─────────────────────────────────────────────────────

// GenreResponse is returned by genre endpoints.
type GenreResponse struct {
	ID        uuid.UUID `json:"id"`
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"created_at"`
}

// MediaTypeResponse is returned by media-type endpoints.
type MediaTypeResponse struct {
	ID          uuid.UUID `json:"id"`
	Name        string    `json:"name"`
	DisplayName string    `json:"display_name"`
	Description string    `json:"description,omitempty"`
	BookCount   int       `json:"book_count"`
}

// ─── Loans ────────────────────────────────────────────────────────────────────

// LoanResponse is returned by loan endpoints.
type LoanResponse struct {
	ID         uuid.UUID `json:"id"`
	LibraryID  uuid.UUID `json:"library_id"`
	BookID     uuid.UUID `json:"book_id"`
	BookTitle  string    `json:"book_title"`
	LoanedTo   string    `json:"loaned_to"`
	LoanedAt   string    `json:"loaned_at"`    // YYYY-MM-DD
	DueDate    *string   `json:"due_date"`     // YYYY-MM-DD
	ReturnedAt *string   `json:"returned_at"`  // YYYY-MM-DD
	Notes      string    `json:"notes"`
	Tags       []TagRef  `json:"tags"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}
