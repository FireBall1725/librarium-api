// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725 (Adaléa)

package service

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/fireball1725/librarium-api/internal/models"
	"github.com/fireball1725/librarium-api/internal/repository"
	"github.com/google/uuid"
)

// ValidationError is returned by service methods when the error is caused by
// invalid input from the caller (e.g. unsupported file type, bad path).
// Handlers should respond with 400; other errors should be 500.
type ValidationError struct{ msg string }

func (e *ValidationError) Error() string { return e.msg }

func validationErr(format string, args ...any) error {
	return &ValidationError{msg: fmt.Sprintf(format, args...)}
}

// knownBookExtensions maps file extensions (without dot) to their MIME types.
var knownBookExtensions = map[string]string{
	"epub": "application/epub+zip",
	"pdf":  "application/pdf",
	"mobi": "application/x-mobipocket-ebook",
	"azw3": "application/vnd.amazon.ebook",
	"cbz":  "application/vnd.comicbook+zip",
	"cbr":  "application/vnd.comicbook-rar",
	"mp3":  "audio/mpeg",
	"m4a":  "audio/mp4",
	"m4b":  "audio/mp4",
	"aax":  "audio/vnd.audible.aax",
	"aaxc": "audio/vnd.audible.aaxc",
	"ogg":  "audio/ogg",
	"flac": "audio/flac",
	"opus": "audio/opus",
}

var isbnRegex = regexp.MustCompile(`(?:^|[^0-9])(\d{13}|\d{10})(?:[^0-9]|$)`)

// illegalNameChars are characters not allowed in file or directory name components.
var illegalNameChars = regexp.MustCompile(`[\\/:*?"<>|]`)
var multiSpaces = regexp.MustCompile(`\s{2,}`)

// sanitizeName makes s safe for use as a file or directory name component.
func sanitizeName(s string) string {
	s = illegalNameChars.ReplaceAllString(s, "")
	s = multiSpaces.ReplaceAllString(s, " ")
	s = strings.TrimSpace(s)
	s = strings.Trim(s, ".")
	if s == "" {
		s = "unknown"
	}
	return s
}

// resolveTemplate substitutes known tokens in tmpl using book and edition metadata,
// returning a relative directory path (not including the filename).
//
// Supported tokens:
//
//	{title}   — book title
//	{author}  — first author name (falls back to first contributor)
//	{year}    — publication year (edition date, else book year)
//	{isbn13}  — ISBN-13
//	{isbn10}  — ISBN-10
//	{edition} — edition name
func resolveTemplate(tmpl string, book *models.Book, edition *models.BookEdition) string {
	year := ""
	if book.PublishYear != nil {
		year = strconv.Itoa(*book.PublishYear)
	} else if edition.PublishDate != nil {
		year = strconv.Itoa(edition.PublishDate.Year())
	}

	author := ""
	for _, c := range book.Contributors {
		if strings.EqualFold(c.Role, "author") {
			author = c.Name
			break
		}
	}
	if author == "" && len(book.Contributors) > 0 {
		author = book.Contributors[0].Name
	}

	replacer := strings.NewReplacer(
		"{title}",   sanitizeName(book.Title),
		"{author}",  sanitizeName(author),
		"{year}",    year,
		"{isbn13}",  edition.ISBN13,
		"{isbn10}",  edition.ISBN10,
		"{edition}", sanitizeName(edition.EditionName),
	)
	result := replacer.Replace(tmpl)
	cleaned := filepath.Clean(result)
	if cleaned == "." || cleaned == "" {
		cleaned = "unknown"
	}
	return cleaned
}

type EditionFileService struct {
	bookRepo          *repository.BookRepo
	editions          *repository.EditionRepo
	files             *repository.EditionFileRepo
	locations         *repository.StorageLocationRepo
	ebookPath         string
	audiobookPath     string
	ebookTemplate     string
	audiobookTemplate string
}

func NewEditionFileService(
	bookRepo *repository.BookRepo,
	editions *repository.EditionRepo,
	files *repository.EditionFileRepo,
	locations *repository.StorageLocationRepo,
	ebookPath, audiobookPath string,
	ebookTemplate, audiobookTemplate string,
) *EditionFileService {
	if ebookTemplate == "" {
		ebookTemplate = "{title}"
	}
	if audiobookTemplate == "" {
		audiobookTemplate = "{title}"
	}
	return &EditionFileService{
		bookRepo:          bookRepo,
		editions:          editions,
		files:             files,
		locations:         locations,
		ebookPath:         ebookPath,
		audiobookPath:     audiobookPath,
		ebookTemplate:     ebookTemplate,
		audiobookTemplate: audiobookTemplate,
	}
}

// pathForEdition returns the base storage path for the given edition format.
func (s *EditionFileService) pathForEdition(format string) string {
	if format == models.EditionFormatAudiobook {
		return s.audiobookPath
	}
	return s.ebookPath
}

// templateForEdition returns the path template for the given edition format.
func (s *EditionFileService) templateForEdition(format string) string {
	if format == models.EditionFormatAudiobook {
		return s.audiobookTemplate
	}
	return s.ebookTemplate
}

// ─── File operations ──────────────────────────────────────────────────────────

// UploadEditionFile writes an uploaded file to disk and creates an EditionFile record.
// The directory structure is determined by the configured path template resolved
// against the book and edition metadata, producing human-readable paths like:
//
//	Andy Weir/Project Hail Mary/project-hail-mary.epub
func (s *EditionFileService) UploadEditionFile(ctx context.Context, edition *models.BookEdition, reader io.Reader, originalName string, size int64) (*models.EditionFile, error) {
	ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(originalName), "."))
	if _, ok := knownBookExtensions[ext]; !ok {
		return nil, validationErr("unsupported file type %q", ext)
	}

	book, err := s.bookRepo.FindByID(ctx, edition.BookID, uuid.Nil, uuid.Nil)
	if err != nil {
		return nil, fmt.Errorf("looking up book: %w", err)
	}

	basePath := s.pathForEdition(edition.Format)
	tmpl := s.templateForEdition(edition.Format)
	subDir := resolveTemplate(tmpl, book, edition)

	dir := filepath.Join(basePath, subDir)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("creating media directory: %w", err)
	}

	// Build a safe filename from the original, stripping the extension first.
	origBase := strings.TrimSuffix(filepath.Base(originalName), filepath.Ext(originalName))
	safeName := sanitizeName(origBase)
	if safeName == "" {
		safeName = uuid.New().String()
	}
	filename := safeName + "." + ext

	// Resolve collisions: if the path already exists, append (2), (3), …
	absPath := filepath.Join(dir, filename)
	for i := 2; ; i++ {
		if _, statErr := os.Stat(absPath); os.IsNotExist(statErr) {
			break
		}
		filename = fmt.Sprintf("%s (%d).%s", safeName, i, ext)
		absPath = filepath.Join(dir, filename)
	}

	relPath := filepath.Join(subDir, filename)

	f, err := os.Create(absPath)
	if err != nil {
		return nil, fmt.Errorf("creating file: %w", err)
	}
	defer func() { _ = f.Close() }()

	written, err := io.Copy(f, reader)
	if err != nil {
		_ = os.Remove(absPath)
		return nil, fmt.Errorf("writing file: %w", err)
	}
	if size > 0 && written != size {
		_ = os.Remove(absPath)
		return nil, fmt.Errorf("incomplete write: expected %d bytes, wrote %d", size, written)
	}

	ef := &models.EditionFile{
		ID:         uuid.New(),
		EditionID:  edition.ID,
		FileFormat: ext,
		FileName:   originalName, // display name keeps the original
		FilePath:   relPath,
		FileSize:   &written,
		RootPath:   basePath,
	}
	if err := s.files.Add(ctx, ef); err != nil {
		_ = os.Remove(absPath)
		return nil, err
	}
	return ef, nil
}

// DeleteEditionFile removes a specific file from disk and its database record.
func (s *EditionFileService) DeleteEditionFile(ctx context.Context, edition *models.BookEdition, ef *models.EditionFile) error {
	// Only delete the physical file for directly-uploaded files (no storage location).
	if ef.StorageLocationID == nil {
		absPath := filepath.Join(s.pathForEdition(edition.Format), ef.FilePath)
		if err := os.Remove(absPath); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("removing file: %w", err)
		}
	}
	return s.files.Delete(ctx, ef.ID)
}

// GetEditionFilePath returns the absolute path and MIME type for serving a specific edition file.
func (s *EditionFileService) GetEditionFilePath(ctx context.Context, edition *models.BookEdition, ef *models.EditionFile) (absPath, mimeType string, err error) {
	if ef.StorageLocationID != nil {
		loc, err := s.locations.FindByID(ctx, *ef.StorageLocationID)
		if err != nil {
			return "", "", err
		}
		absPath = filepath.Join(loc.RootPath, ef.FilePath)
	} else {
		absPath = filepath.Join(s.pathForEdition(edition.Format), ef.FilePath)
	}

	ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(ef.FilePath), "."))
	mimeType = knownBookExtensions[ext]
	if mimeType == "" {
		mimeType = "application/octet-stream"
	}
	return absPath, mimeType, nil
}

// PopulateRootPaths sets RootPath on each file.
// For files with no StorageLocationID the editionFormat determines the default path.
// Unique storage locations are fetched once via individual lookups (counts are small in practice).
func (s *EditionFileService) PopulateRootPaths(ctx context.Context, files []*models.EditionFile, editionFormat string) {
	defaultRoot := s.pathForEdition(editionFormat)
	cache := map[uuid.UUID]string{}
	for _, f := range files {
		if f.StorageLocationID == nil {
			f.RootPath = defaultRoot
			continue
		}
		id := *f.StorageLocationID
		if root, ok := cache[id]; ok {
			f.RootPath = root
			continue
		}
		if loc, err := s.locations.FindByID(ctx, id); err == nil {
			cache[id] = loc.RootPath
			f.RootPath = loc.RootPath
		}
	}
}

// ListEditionFiles returns all files attached to an edition.
func (s *EditionFileService) ListEditionFiles(ctx context.Context, editionID uuid.UUID) ([]*models.EditionFile, error) {
	return s.files.ListByEdition(ctx, editionID)
}

// ListEditionFilesByEditions batch-fetches files for multiple editions.
func (s *EditionFileService) ListEditionFilesByEditions(ctx context.Context, editionIDs []uuid.UUID) (map[uuid.UUID][]*models.EditionFile, error) {
	return s.files.ListByEditions(ctx, editionIDs)
}

// FindEditionFile looks up a single file by ID.
func (s *EditionFileService) FindEditionFile(ctx context.Context, fileID uuid.UUID) (*models.EditionFile, error) {
	return s.files.FindByID(ctx, fileID)
}

// ─── Storage locations ────────────────────────────────────────────────────────

func (s *EditionFileService) ListStorageLocations(ctx context.Context, libraryID uuid.UUID) ([]*models.StorageLocation, error) {
	return s.locations.List(ctx, libraryID)
}

func (s *EditionFileService) CreateStorageLocation(ctx context.Context, libraryID uuid.UUID, name, rootPath, mediaFormat, pathTemplate string) (*models.StorageLocation, error) {
	if name == "" || rootPath == "" || mediaFormat == "" {
		return nil, fmt.Errorf("name, root_path, and media_format are required")
	}
	if pathTemplate == "" {
		pathTemplate = "{title}"
	}
	return s.locations.Create(ctx, uuid.New(), libraryID, name, rootPath, mediaFormat, pathTemplate)
}

func (s *EditionFileService) UpdateStorageLocation(ctx context.Context, id uuid.UUID, name, rootPath, mediaFormat, pathTemplate string) (*models.StorageLocation, error) {
	return s.locations.Update(ctx, id, name, rootPath, mediaFormat, pathTemplate)
}

func (s *EditionFileService) DeleteStorageLocation(ctx context.Context, id uuid.UUID) error {
	return s.locations.Delete(ctx, id)
}

// ─── Upload-path browse & link ────────────────────────────────────────────────

// BrowseUploadPath lists the contents of a sub-path within the server's
// configured ebook or audiobook upload directory.
// format must be "ebook" or "audiobook".
func (s *EditionFileService) BrowseUploadPath(ctx context.Context, format, subPath string) (rootPath string, entries []BrowseEntry, err error) {
	basePath := s.pathForEdition(format)
	rootPath = basePath

	cleaned := filepath.Clean(filepath.Join(string(filepath.Separator), subPath))
	absDir := filepath.Join(basePath, cleaned)
	baseClean := filepath.Clean(basePath)
	if absDir != baseClean && !strings.HasPrefix(absDir, baseClean+string(filepath.Separator)) {
		return "", nil, fmt.Errorf("path outside upload directory")
	}

	dirEntries, err := os.ReadDir(absDir)
	if err != nil {
		if os.IsNotExist(err) {
			return rootPath, []BrowseEntry{}, nil // empty but valid
		}
		return "", nil, fmt.Errorf("reading directory: %w", err)
	}

	out := make([]BrowseEntry, 0, len(dirEntries))
	for _, de := range dirEntries {
		rel := strings.TrimPrefix(filepath.Join(cleaned, de.Name()), string(filepath.Separator))
		if de.IsDir() {
			out = append(out, BrowseEntry{Name: de.Name(), Path: rel, IsDir: true})
		} else {
			ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(de.Name()), "."))
			info, _ := de.Info()
			size := int64(0)
			if info != nil {
				size = info.Size()
			}
			_, bookable := knownBookExtensions[ext]
			out = append(out, BrowseEntry{
				Name:       de.Name(),
				Path:       rel,
				IsDir:      false,
				Size:       size,
				Ext:        ext,
				IsBookable: bookable,
			})
		}
	}
	return rootPath, out, nil
}

// LinkUploadedFile links a file that already exists in the server's default
// upload directory (ebook or audiobook path) to an edition without copying it.
// relPath is relative to the upload directory for the edition's format.
func (s *EditionFileService) LinkUploadedFile(ctx context.Context, edition *models.BookEdition, relPath string) (*models.EditionFile, error) {
	basePath := s.pathForEdition(edition.Format)

	cleaned := filepath.Clean(filepath.Join(string(filepath.Separator), relPath))
	absPath := filepath.Join(basePath, cleaned)
	baseClean := filepath.Clean(basePath)
	if absPath != baseClean && !strings.HasPrefix(absPath, baseClean+string(filepath.Separator)) {
		return nil, validationErr("path outside upload directory")
	}

	info, err := os.Stat(absPath)
	if err != nil {
		return nil, validationErr("file not found: %s", relPath)
	}
	if info.IsDir() {
		return nil, validationErr("path is a directory, not a file")
	}

	ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(relPath), "."))
	if _, ok := knownBookExtensions[ext]; !ok {
		return nil, validationErr("unsupported file type %q", ext)
	}

	storablePath := strings.TrimPrefix(cleaned, string(filepath.Separator))
	fileSize := info.Size()
	ef := &models.EditionFile{
		ID:         uuid.New(),
		EditionID:  edition.ID,
		FileFormat: ext,
		FileName:   filepath.Base(relPath),
		FilePath:   storablePath,
		FileSize:   &fileSize,
		RootPath:   basePath,
		// StorageLocationID intentionally nil — uses pathForEdition at serve time
	}
	if err := s.files.Add(ctx, ef); err != nil {
		return nil, err
	}
	return ef, nil
}

// ─── File browser ─────────────────────────────────────────────────────────────

type BrowseEntry struct {
	Name       string `json:"name"`
	Path       string `json:"path"` // relative to location root
	IsDir      bool   `json:"is_dir"`
	Size       int64  `json:"size,omitempty"`
	Ext        string `json:"ext,omitempty"`
	IsBookable bool   `json:"is_bookable"` // can be linked to an edition
}

// BrowseStorageLocation lists the contents of a sub-path within a storage location.
// All directories and all files are returned; is_bookable marks book-format files.
func (s *EditionFileService) BrowseStorageLocation(ctx context.Context, locationID uuid.UUID, subPath string) ([]BrowseEntry, error) {
	loc, err := s.locations.FindByID(ctx, locationID)
	if err != nil {
		return nil, err
	}

	// Sanitise subPath to prevent directory traversal.
	cleaned := filepath.Clean(filepath.Join(string(filepath.Separator), subPath))
	absDir := filepath.Join(loc.RootPath, cleaned)
	rootClean := filepath.Clean(loc.RootPath)
	if absDir != rootClean && !strings.HasPrefix(absDir, rootClean+string(filepath.Separator)) {
		return nil, fmt.Errorf("path outside storage location")
	}

	dirEntries, err := os.ReadDir(absDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("directory not found: %s", absDir)
		}
		return nil, fmt.Errorf("reading directory: %w", err)
	}

	out := make([]BrowseEntry, 0, len(dirEntries))
	for _, de := range dirEntries {
		// Compute relative path (strip leading separator added by filepath.Join(sep, ...))
		rel := strings.TrimPrefix(filepath.Join(cleaned, de.Name()), string(filepath.Separator))

		if de.IsDir() {
			out = append(out, BrowseEntry{Name: de.Name(), Path: rel, IsDir: true})
		} else {
			ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(de.Name()), "."))
			info, _ := de.Info()
			size := int64(0)
			if info != nil {
				size = info.Size()
			}
			_, bookable := knownBookExtensions[ext]
			out = append(out, BrowseEntry{
				Name:       de.Name(),
				Path:       rel,
				IsDir:      false,
				Size:       size,
				Ext:        ext,
				IsBookable: bookable,
			})
		}
	}
	return out, nil
}

// LinkEditionFile links an existing file from a storage location to an edition
// without copying it. The filePath is relative to the storage location's root.
func (s *EditionFileService) LinkEditionFile(ctx context.Context, edition *models.BookEdition, locationID uuid.UUID, relPath string) (*models.EditionFile, error) {
	loc, err := s.locations.FindByID(ctx, locationID)
	if err != nil {
		return nil, err
	}

	// Validate the path stays inside the root.
	cleaned := filepath.Clean(filepath.Join(string(filepath.Separator), relPath))
	absPath := filepath.Join(loc.RootPath, cleaned)
	rootClean := filepath.Clean(loc.RootPath)
	if absPath != rootClean && !strings.HasPrefix(absPath, rootClean+string(filepath.Separator)) {
		return nil, validationErr("path outside storage location")
	}

	info, err := os.Stat(absPath)
	if err != nil {
		return nil, validationErr("file not found: %s", relPath)
	}
	if info.IsDir() {
		return nil, validationErr("path is a directory, not a file")
	}

	ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(relPath), "."))
	if _, ok := knownBookExtensions[ext]; !ok {
		return nil, validationErr("unsupported file type %q", ext)
	}

	storablePath := strings.TrimPrefix(cleaned, string(filepath.Separator))
	fileSize := info.Size()
	ef := &models.EditionFile{
		ID:                uuid.New(),
		EditionID:         edition.ID,
		FileFormat:        ext,
		FileName:          filepath.Base(relPath),
		FilePath:          storablePath,
		StorageLocationID: &locationID,
		FileSize:          &fileSize,
		RootPath:          loc.RootPath,
	}
	if err := s.files.Add(ctx, ef); err != nil {
		return nil, err
	}
	return ef, nil
}

// ─── Scan ─────────────────────────────────────────────────────────────────────

type ScanResult struct {
	Linked   []ScanLinked  `json:"linked"`
	Unlinked []ScanFile    `json:"unlinked"`
	Missing  []ScanMissing `json:"missing_files"`
}

type ScanLinked struct {
	FilePath  string `json:"file_path"`
	FileSize  int64  `json:"file_size"`
	FileExt   string `json:"file_ext"`
	EditionID string `json:"edition_id"`
	BookTitle string `json:"book_title"`
	ISBN      string `json:"isbn"`
}

type ScanFile struct {
	FilePath string `json:"file_path"`
	FileSize int64  `json:"file_size"`
}

type ScanMissing struct {
	EditionID string `json:"edition_id"`
	BookTitle string `json:"book_title"`
	Format    string `json:"format"`
	ISBN13    string `json:"isbn_13"`
	ISBN10    string `json:"isbn_10"`
}

// ScanStorageLocation walks the storage location's root_path, matches files to
// editions by ISBN, auto-links matches, and returns a full report.
func (s *EditionFileService) ScanStorageLocation(ctx context.Context, libraryID, locationID uuid.UUID) (*ScanResult, error) {
	loc, err := s.locations.FindByID(ctx, locationID)
	if err != nil {
		return nil, err
	}

	// Build ISBN → edition index for the library.
	missing, err := s.editions.ListMissingFiles(ctx, libraryID)
	if err != nil {
		return nil, err
	}
	isbn13Index := make(map[string]*models.BookEdition, len(missing))
	isbn10Index := make(map[string]*models.BookEdition, len(missing))
	for _, e := range missing {
		if e.ISBN13 != "" {
			isbn13Index[e.ISBN13] = e
		}
		if e.ISBN10 != "" {
			isbn10Index[e.ISBN10] = e
		}
	}

	stillMissing := make(map[uuid.UUID]*models.BookEdition, len(missing))
	for _, e := range missing {
		stillMissing[e.ID] = e
	}

	result := &ScanResult{}

	err = filepath.WalkDir(loc.RootPath, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil || d.IsDir() {
			return nil
		}

		ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(path), "."))
		if _, ok := knownBookExtensions[ext]; !ok {
			return nil
		}

		info, err := d.Info()
		if err != nil {
			return nil
		}
		size := info.Size()
		relPath, _ := filepath.Rel(loc.RootPath, path)
		base := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))

		var matched *models.BookEdition
		var matchedISBN string
		for _, m := range isbnRegex.FindAllStringSubmatch(base, -1) {
			candidate := m[1]
			if e, ok := isbn13Index[candidate]; ok {
				matched = e
				matchedISBN = candidate
				break
			}
			if e, ok := isbn10Index[candidate]; ok {
				matched = e
				matchedISBN = candidate
				break
			}
		}

		if matched == nil {
			result.Unlinked = append(result.Unlinked, ScanFile{FilePath: relPath, FileSize: size})
			return nil
		}

		actualName := filepath.Base(path)
		ef := &models.EditionFile{
			ID:                uuid.New(),
			EditionID:         matched.ID,
			FileFormat:        ext,
			FileName:          actualName,
			FilePath:          relPath,
			StorageLocationID: &locationID,
			FileSize:          &size,
		}
		if err := s.files.Add(ctx, ef); err == nil {
			result.Linked = append(result.Linked, ScanLinked{
				FilePath:  relPath,
				FileSize:  size,
				FileExt:   ext,
				EditionID: matched.ID.String(),
				BookTitle: matched.EditionName,
				ISBN:      matchedISBN,
			})
			delete(stillMissing, matched.ID)
			delete(isbn13Index, matched.ISBN13)
			delete(isbn10Index, matched.ISBN10)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("walking storage location: %w", err)
	}

	for _, e := range stillMissing {
		result.Missing = append(result.Missing, ScanMissing{
			EditionID: e.ID.String(),
			Format:    e.Format,
			ISBN13:    e.ISBN13,
			ISBN10:    e.ISBN10,
		})
	}

	return result, nil
}
