// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725 (Adaléa)

package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/fireball1725/librarium-api/internal/providers"
	"github.com/fireball1725/librarium-api/internal/repository"
)

const (
	settingsProviderPrefix = "provider:"
	settingsProviderOrder  = "metadata_provider_order"
)

// ProviderService manages provider configuration stored in instance_settings.
// coverProbeClient reads cover headers during a lookup. Its own timeout
// backs up the probe deadline.
var coverProbeClient = &http.Client{Timeout: 5 * time.Second}

type ProviderService struct {
	registry *providers.Registry
	settings *repository.SettingsRepo

	// recent holds each lookup's raw answers for a while, so adding the book
	// can store them without asking every provider a second time.
	recentMu sync.Mutex
	recent   map[string]recentAnswers
}

type recentAnswers struct {
	results []*providers.BookResult
	at      time.Time
}

// recentAnswersTTL is how long a lookup's answers wait for the book to be
// added. Long enough to read the result and pick fields; short enough that a
// stale answer isn't saved hours later.
const recentAnswersTTL = 30 * time.Minute

// recentAnswersMax caps the cache; a scan session is tens of books, not
// thousands.
const recentAnswersMax = 500

func NewProviderService(registry *providers.Registry, settings *repository.SettingsRepo) *ProviderService {
	return &ProviderService{registry: registry, settings: settings, recent: map[string]recentAnswers{}}
}

func (s *ProviderService) rememberAnswers(code string, results []*providers.BookResult) {
	if len(results) == 0 {
		return
	}
	key := providers.BarcodeKey(code)
	s.recentMu.Lock()
	defer s.recentMu.Unlock()
	if len(s.recent) >= recentAnswersMax {
		for k, v := range s.recent {
			if time.Since(v.at) > recentAnswersTTL {
				delete(s.recent, k)
			}
		}
		for k := range s.recent {
			if len(s.recent) < recentAnswersMax {
				break
			}
			delete(s.recent, k)
		}
	}
	s.recent[key] = recentAnswers{results: results, at: time.Now()}
}

// RecentAnswers returns the raw answers from a recent lookup of this ISBN or
// UPC, or nil if there wasn't one in the last half hour.
func (s *ProviderService) RecentAnswers(code string) []*providers.BookResult {
	key := providers.BarcodeKey(code)
	s.recentMu.Lock()
	defer s.recentMu.Unlock()
	r, ok := s.recent[key]
	if !ok || time.Since(r.at) > recentAnswersTTL {
		return nil
	}
	return r.results
}

// LookupOne asks one provider about an ISBN. ok is false when no enabled
// ISBN provider has that name.
func (s *ProviderService) LookupOne(ctx context.Context, name, isbn string) (*providers.BookResult, bool, error) {
	return s.registry.LookupOne(ctx, name, isbn)
}

// LoadAll reads provider configs from the DB and applies them to the registry.
func (s *ProviderService) LoadAll(ctx context.Context) error {
	for _, p := range s.registry.All() {
		cfg, err := s.loadConfig(ctx, p.Info().Name)
		if err != nil && !errors.Is(err, repository.ErrNotFound) {
			return err
		}
		p.Configure(cfg)
	}
	return nil
}

// GetAllProviderStatus returns info + current config for every provider.
// API keys are masked.
func (s *ProviderService) GetAllProviderStatus(ctx context.Context) ([]ProviderStatus, error) {
	var out []ProviderStatus
	for _, p := range s.registry.All() {
		info := p.Info()
		cfg, err := s.loadConfig(ctx, info.Name)
		if err != nil && !errors.Is(err, repository.ErrNotFound) {
			return nil, err
		}

		status := ProviderStatus{
			Name:         info.Name,
			DisplayName:  info.DisplayName,
			Description:  info.Description,
			RequiresKey:  info.RequiresKey,
			Capabilities: info.Capabilities,
			HelpText:     info.HelpText,
			HelpURL:      info.HelpURL,
			ConfigFields: info.ConfigFields,
			Enabled:      p.Enabled(),

			Kind:          info.Kind,
			Region:        info.Region,
			Languages:     info.Languages,
			Sends:         info.Sends,
			ContributedBy: info.ContributedBy,
			DocsURL:       info.DocsURL,
		}
		if status.Kind == "" {
			status.Kind = providers.KindData
		}
		status.Config, status.HasAPIKey = maskProviderConfig(info, cfg)
		status.Listed = providerListed(cfg, status.Enabled)

		out = append(out, status)
	}
	return out, nil
}

// maskProviderConfig builds the display-safe config map for a provider's
// current settings. When the provider declares ConfigFields, every
// "password"-type field present in cfg is masked and every other field
// (e.g. a mirror's base_url) passes through in the clear; hasAPIKey is true
// if any password field is set. Providers with no ConfigFields fall back to
// the legacy single api_key convention, unchanged from before ConfigFields
// existed.
// providerListed reads the "listed" flag. Providers that were on before the
// flag existed stay on the list without anyone re-adding them.
func providerListed(cfg map[string]string, enabled bool) bool {
	switch cfg["listed"] {
	case "true":
		return true
	case "false":
		return false
	}
	return enabled
}

func maskProviderConfig(info providers.ProviderInfo, cfg map[string]string) (config map[string]string, hasAPIKey bool) {
	if len(info.ConfigFields) > 0 {
		masked := make(map[string]string, len(info.ConfigFields))
		for _, field := range info.ConfigFields {
			val, ok := cfg[field.Key]
			if !ok || val == "" {
				continue
			}
			if field.Type == "password" {
				masked[field.Key] = "***"
				hasAPIKey = true
			} else {
				masked[field.Key] = val
			}
		}
		return masked, hasAPIKey
	}

	if key, ok := cfg["api_key"]; ok && key != "" {
		return map[string]string{"api_key": "***"}, true
	}
	return nil, false
}

// ConfigureProvider saves config to the DB and reconfigures the live provider.
// Incoming cfg is merged on top of the existing stored config so that omitted
// keys (e.g. api_key when only toggling enabled) are preserved.
func (s *ProviderService) ConfigureProvider(ctx context.Context, name string, cfg map[string]string) error {
	// Validate the provider exists
	found := false
	for _, p := range s.registry.All() {
		if p.Info().Name == name {
			found = true
			break
		}
	}
	if !found {
		return fmt.Errorf("unknown provider %q", name)
	}

	// Load existing config so we can merge rather than overwrite.
	merged, err := s.loadConfig(ctx, name)
	if err != nil && !errors.Is(err, repository.ErrNotFound) {
		return err
	}
	if merged == nil {
		merged = make(map[string]string)
	}
	maps.Copy(merged, cfg)

	data, err := json.Marshal(merged)
	if err != nil {
		return err
	}
	if err := s.settings.Set(ctx, settingsProviderPrefix+name, string(data)); err != nil {
		return err
	}

	s.registry.Configure(name, merged)
	return nil
}

// Registry returns the underlying provider registry.
func (s *ProviderService) Registry() *providers.Registry {
	return s.registry
}

// Probes for the admin Test button, one per kind of lookup. Each is something
// every general provider of that kind has.
const (
	// Harry Potter and the Philosopher's Stone, in every major book DB. A
	// region-specific provider overrides it via Info().TestISBN (e.g. Finna,
	// which only has Finnish editions and never this UK one).
	defaultTestISBN = "9780439708180"
	// Ender's Game, Tor mass-market paperback; found in UPCitemdb 2026-09-18.
	defaultTestUPC = "037145006994"
	// A series every manga and series source carries.
	defaultTestSeries = "One Piece"
)

// TestProvider makes a live test call to the named provider with whatever it
// can look up (an ISBN, a UPC, or a series name) and returns what it found or
// an actionable error message.
func (s *ProviderService) TestProvider(ctx context.Context, name string) (string, error) {
	for _, p := range s.registry.All() {
		info := p.Info()
		if info.Name != name {
			continue
		}
		if !p.Enabled() {
			return "", fmt.Errorf("provider is disabled — save an API key and enable it first")
		}
		switch pp := p.(type) {
		case providers.BookISBNProvider:
			testISBN := defaultTestISBN
			if info.TestISBN != "" {
				testISBN = info.TestISBN
			}
			result, err := pp.LookupByISBN(ctx, testISBN)
			if err != nil {
				return "", err
			}
			if result == nil {
				return "", fmt.Errorf("no result returned for test ISBN %s", testISBN)
			}
			return result.Title, nil
		case providers.BookUPCProvider:
			result, err := pp.LookupByUPC(ctx, defaultTestUPC)
			if err != nil {
				return "", err
			}
			if result == nil {
				return "", fmt.Errorf("no result returned for test UPC %s", defaultTestUPC)
			}
			return result.Title, nil
		case providers.SeriesSearchProvider:
			results, err := pp.SearchSeries(ctx, defaultTestSeries)
			if err != nil {
				return "", err
			}
			if len(results) == 0 {
				return "", fmt.Errorf("no result returned for test series %q", defaultTestSeries)
			}
			return results[0].Name, nil
		}
		return "", fmt.Errorf("this provider has nothing the test knows how to look up")
	}
	return "", fmt.Errorf("unknown provider %q", name)
}

// LookupISBN queries all enabled BookISBN providers.
func (s *ProviderService) LookupISBN(ctx context.Context, isbn string) []*providers.BookResult {
	results := s.registry.LookupISBN(ctx, isbn)
	s.rememberAnswers(isbn, results)
	return results
}

// LookupUPC queries all enabled BookUPC providers.
func (s *ProviderService) LookupUPC(ctx context.Context, code string) []*providers.BookResult {
	results := s.registry.LookupUPC(ctx, code)
	s.rememberAnswers(code, results)
	return results
}

// LookupISBNMerged asks every enabled provider at once and merges what came
// back. Each field is pre-selected from the answers themselves; the saved
// provider order is kept for older clients but no longer used here. Covers are
// probed for size so the largest is offered first.
func (s *ProviderService) LookupISBNMerged(ctx context.Context, isbn string) (*providers.MergedBookResult, error) {
	results, statuses := s.registry.LookupISBNReport(ctx, isbn)
	s.rememberAnswers(isbn, results)
	merged := providers.MergeBookResults(results)
	merged.Providers = statuses
	providers.ProbeCoverSizes(ctx, coverProbeClient, merged.Covers)
	merged.SortCoversBySize()
	return merged, nil
}

// GetProviderOrder returns the saved provider priority order. Defaults to
// registration order if none has been configured.
func (s *ProviderService) GetProviderOrder(ctx context.Context) ([]string, error) {
	raw, err := s.settings.Get(ctx, settingsProviderOrder)
	if errors.Is(err, repository.ErrNotFound) || raw == "" {
		var names []string
		for _, p := range s.registry.All() {
			names = append(names, p.Info().Name)
		}
		return names, nil
	}
	if err != nil {
		return nil, err
	}
	var order []string
	if err := json.Unmarshal([]byte(raw), &order); err != nil {
		return nil, err
	}
	// Append any providers registered after the order was saved.
	inOrder := make(map[string]bool, len(order))
	for _, name := range order {
		inOrder[name] = true
	}
	for _, p := range s.registry.All() {
		if !inOrder[p.Info().Name] {
			order = append(order, p.Info().Name)
		}
	}
	return order, nil
}

// SetProviderOrder persists the provider priority order.
func (s *ProviderService) SetProviderOrder(ctx context.Context, order []string) error {
	data, err := json.Marshal(order)
	if err != nil {
		return err
	}
	return s.settings.Set(ctx, settingsProviderOrder, string(data))
}

// SearchSeries queries all enabled SeriesSearch providers.
func (s *ProviderService) SearchSeries(ctx context.Context, query string) []providers.SeriesResult {
	return s.registry.SearchSeries(ctx, query)
}

// SearchBooks queries all enabled BookSearch providers, then ranks and deduplicates
// results according to the configured provider priority order.
func (s *ProviderService) SearchBooks(ctx context.Context, query string) []*providers.BookResult {
	results := s.registry.SearchBooks(ctx, query)
	if len(results) == 0 {
		return results
	}

	order, _ := s.GetProviderOrder(ctx)
	return rankAndDeduplicateBooks(results, order)
}

// rankAndDeduplicateBooks sorts results by provider priority order and removes
// duplicates, keeping the highest-priority provider's version of each book.
// Two results are considered the same book when they share an ISBN-13, ISBN-10,
// or a normalised (title + first-author) fingerprint.
func rankAndDeduplicateBooks(results []*providers.BookResult, order []string) []*providers.BookResult {
	// Build priority map: lower index = higher priority.
	priority := make(map[string]int, len(order))
	for i, name := range order {
		priority[name] = i
	}
	providerRank := func(name string) int {
		if r, ok := priority[name]; ok {
			return r
		}
		return len(order) // unlisted providers sort last
	}

	// Stable sort so within each provider the original result order is preserved.
	sorted := make([]*providers.BookResult, len(results))
	copy(sorted, results)
	stableSort(sorted, func(a, b *providers.BookResult) bool {
		return providerRank(a.Provider) < providerRank(b.Provider)
	})

	// First pass: assign each unique book a canonical slot (the highest-priority result).
	// Keep a pointer to the kept result so we can fill gaps from lower-priority duplicates.
	type slot struct {
		idx    int
		result *providers.BookResult
	}
	keyToSlot := make(map[string]*slot)
	out := make([]*providers.BookResult, 0, len(sorted))

	for _, r := range sorted {
		keys := bookKeys(r)

		// Find existing slot for this book, if any.
		var existing *slot
		for _, k := range keys {
			if s, ok := keyToSlot[k]; ok {
				existing = s
				break
			}
		}

		if existing == nil {
			// New book — add to output and register its keys.
			s := &slot{idx: len(out), result: r}
			out = append(out, r)
			for _, k := range keys {
				keyToSlot[k] = s
			}
			// Also register any keys the new result introduces.
			continue
		}

		// Duplicate — waterfall: fill any blank fields in the kept result.
		mergeBookResult(existing.result, r)
		// Register any new keys this duplicate introduced (e.g. kept result had no ISBN,
		// but this one does) so future duplicates can match against them too.
		for _, k := range keys {
			if _, ok := keyToSlot[k]; !ok {
				keyToSlot[k] = existing
			}
		}
	}
	return out
}

// mergeBookResult fills blank fields in dst with non-blank values from src.
func mergeBookResult(dst, src *providers.BookResult) {
	if dst.Subtitle == "" {
		dst.Subtitle = src.Subtitle
	}
	if len(dst.Authors) == 0 {
		dst.Authors = src.Authors
	}
	if dst.Publisher == "" {
		dst.Publisher = src.Publisher
	}
	if dst.PublishDate == "" {
		dst.PublishDate = src.PublishDate
	}
	if dst.ISBN10 == "" {
		dst.ISBN10 = src.ISBN10
	}
	if dst.ISBN13 == "" {
		dst.ISBN13 = src.ISBN13
	}
	if dst.Description == "" {
		dst.Description = src.Description
	}
	if dst.CoverURL == "" {
		dst.CoverURL = src.CoverURL
	}
	if dst.Language == "" {
		dst.Language = src.Language
	}
	if dst.PageCount == nil {
		dst.PageCount = src.PageCount
	}
	if len(dst.Categories) == 0 {
		dst.Categories = src.Categories
	}
}

// bookKeys returns a set of deduplication keys for a result.
// Sharing any key means two results represent the same book.
func bookKeys(r *providers.BookResult) []string {
	var keys []string
	// ISBN alone is kept as the merge key, deliberately not folded together
	// with year: providers disagree about publication year for the same ISBN
	// constantly (one reports the original publication, another the printing
	// in hand), so requiring exact year agreement here would split a single
	// real edition into duplicates far more often than it would ever catch a
	// genuine reprint. A publisher reusing one ISBN across separate print
	// runs years apart (e.g. Panther/Granada reprints in ISFDB's data) is a
	// real but rarer case than that common false split, and isn't handled by
	// this key — see TestRankAndDeduplicateBooks_SameISBNDifferentPrintings'
	// updated expectation.
	y := publishYear(r.PublishDate)
	if r.ISBN13 != "" {
		keys = append(keys, "13:"+r.ISBN13)
	}
	if r.ISBN10 != "" {
		keys = append(keys, "10:"+r.ISBN10)
	}
	t := normalizeBookToken(r.Title)
	if t != "" && len(r.Authors) > 0 {
		a := normalizeBookToken(r.Authors[0])
		if a != "" {
			// Title+author alone identifies a *work*, not an edition — a novel
			// reprinted a dozen times by different publishers over the decades
			// (common in ISFDB's data) would otherwise collide into a single
			// slot and silently lose every edition but the first one merged.
			// Folding in publisher+year narrows the fallback key to "same
			// edition, ISBN just wasn't reported by this provider" instead of
			// "same work, any edition" — distinct editions with no ISBN from
			// either side now survive as separate results.
			p := normalizeBookToken(r.Publisher)
			keys = append(keys, "ta:"+t+"|"+a+"|"+p+"|"+y)
		}
	}
	return keys
}

// publishYear extracts a leading 4-digit year from a provider's free-form
// publish date string (e.g. "1973-00-00", "1973", "May 1973"), or "" if none
// is found. Used only to narrow a dedup key, so an imprecise/missing year is
// fine — it just means that key component falls back to matching on "".
func publishYear(s string) string {
	i := strings.IndexFunc(s, func(r rune) bool { return r >= '0' && r <= '9' })
	if i < 0 || i+4 > len(s) {
		return ""
	}
	y := s[i : i+4]
	for _, r := range y {
		if r < '0' || r > '9' {
			return ""
		}
	}
	if y == "0000" {
		return ""
	}
	return y
}

// normalizeBookToken lowercases s and strips everything that isn't a letter or digit.
func normalizeBookToken(s string) string {
	s = strings.ToLower(s)
	var b strings.Builder
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// stableSort is an insertion-sort-based stable sort for small slices.
func stableSort[T any](s []T, less func(a, b T) bool) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && less(s[j], s[j-1]); j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}

// ─── Internal ─────────────────────────────────────────────────────────────────

func (s *ProviderService) loadConfig(ctx context.Context, name string) (map[string]string, error) {
	raw, err := s.settings.Get(ctx, settingsProviderPrefix+name)
	if err != nil {
		return nil, err
	}
	var cfg map[string]string
	if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
		return nil, err
	}
	return cfg, nil
}

// ─── DTO ──────────────────────────────────────────────────────────────────────

type ProviderStatus struct {
	Name         string                  `json:"name"`
	DisplayName  string                  `json:"display_name"`
	Description  string                  `json:"description"`
	RequiresKey  bool                    `json:"requires_key"`
	Capabilities []string                `json:"capabilities"`
	HelpText     string                  `json:"help_text,omitempty"`
	HelpURL      string                  `json:"help_url,omitempty"`
	Enabled      bool                    `json:"enabled"`
	HasAPIKey    bool                    `json:"has_api_key"`
	Config       map[string]string       `json:"config,omitempty"`
	ConfigFields []providers.ConfigField `json:"config_fields,omitempty"`

	// Listed is whether the provider shows on the admin Lookups list rather
	// than only in the catalogue. Kept as the "listed" config key, which no
	// provider reads; a provider that's on counts as listed unless removed.
	Listed bool `json:"listed"`

	Kind          string   `json:"kind"`
	Region        string   `json:"region,omitempty"`
	Languages     []string `json:"languages,omitempty"`
	Sends         string   `json:"sends,omitempty"`
	ContributedBy string   `json:"contributed_by,omitempty"`
	DocsURL       string   `json:"docs_url,omitempty"`
}
