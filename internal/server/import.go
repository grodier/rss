package server

import (
	"cmp"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"
	"github.com/grodier/rss/internal/discovery"
	"github.com/grodier/rss/internal/opml"
	"github.com/grodier/rss/internal/rss"
	"github.com/grodier/rss/internal/validator"
)

const (
	// maxImportBytes caps the size of an uploaded OPML file. The request body
	// may be up to 64 KiB more, for the multipart headers.
	maxImportBytes = 1 << 20
	// maxImportFeeds caps how many feeds one import may have.
	maxImportFeeds = 1000
	// maxImportFilenameBytes caps the stored filename.
	maxImportFilenameBytes = 255
	// recentImports is how many imports the upload page lists.
	recentImports = 5
)

type importForm struct {
	validator.Validator
}

type settingsImportPage struct {
	Nav     navData
	Form    importForm
	Imports []rss.ImportSummary
}

// renderSettingsImport renders settings_import.html with status and the
// user's recent imports.
func (s *Server) renderSettingsImport(w http.ResponseWriter, r *http.Request, status int, form importForm) {
	userID, _ := s.authenticatedUserID(r)
	imports, err := s.services.ImportService.ListByUser(r.Context(), userID, recentImports)
	if err != nil {
		s.serverErrorHTML(w, r, err)
		return
	}

	data := settingsImportPage{Nav: s.nav(r, "settings"), Form: form, Imports: imports}
	if err := s.renderHTML(w, status, "settings_import.html", data); err != nil {
		s.serverErrorHTML(w, r, err)
	}
}

func (s *Server) settingsImportHandler(w http.ResponseWriter, r *http.Request) {
	s.renderSettingsImport(w, r, http.StatusOK, importForm{})
}

// settingsImportFormHandler reads an uploaded OPML file, queues an import of
// its feeds and redirects to the import's results page. The upload is a
// multipart form, so it has its own size cap instead of decodePostForm's.
func (s *Server) settingsImportFormHandler(w http.ResponseWriter, r *http.Request) {
	var form importForm
	fail := func(status int, message string) {
		form.AddFieldError("file", message)
		s.renderSettingsImport(w, r, status, form)
	}
	const tooLarge = "That file is too large. Imports can be up to 1 MB."

	r.Body = http.MaxBytesReader(w, r.Body, maxImportBytes+64<<10)
	if err := r.ParseMultipartForm(maxImportBytes); err != nil {
		if _, ok := errors.AsType[*http.MaxBytesError](err); ok {
			fail(http.StatusRequestEntityTooLarge, tooLarge)
			return
		}
		s.errorHTML(w, r, http.StatusBadRequest, "Bad request", "That form submission couldn't be read.")
		return
	}
	defer r.MultipartForm.RemoveAll()

	file, header, err := r.FormFile("file")
	if err != nil {
		if errors.Is(err, http.ErrMissingFile) {
			fail(http.StatusUnprocessableEntity, "Choose an OPML file to import.")
			return
		}
		s.serverErrorHTML(w, r, err)
		return
	}
	defer file.Close()

	if header.Size > maxImportBytes {
		fail(http.StatusRequestEntityTooLarge, tooLarge)
		return
	}

	outlines, err := opml.Parse(file)
	switch {
	// A file that starts as OPML but breaks off is as unusable as one that
	// isn't OPML at all.
	case err != nil:
		fail(http.StatusUnprocessableEntity, "That file isn't an OPML file. Export one from your other reader's settings.")
		return
	case len(outlines) == 0:
		fail(http.StatusUnprocessableEntity, "That file has no feeds in it.")
		return
	case len(outlines) > maxImportFeeds:
		fail(http.StatusUnprocessableEntity, "That file has more than 1,000 feeds. Split it into smaller files and import each one.")
		return
	}

	userID, _ := s.authenticatedUserID(r)
	if ok, retryAfter := s.importLimiter.AllowWithRetry(userID); !ok {
		setRetryAfter(w, retryAfter)
		fail(http.StatusTooManyRequests, "You've started several imports in the last hour. Try again later.")
		return
	}

	feeds := make([]rss.ImportFeed, len(outlines))
	for i, o := range outlines {
		feeds[i] = rss.ImportFeed{URL: o.URL, Title: o.Title}
	}
	id, err := s.services.ImportService.Create(r.Context(), userID, importFilename(header.Filename), feeds)
	if err != nil {
		s.serverErrorHTML(w, r, err)
		return
	}
	http.Redirect(w, r, "/settings/imports/"+id, http.StatusSeeOther)
}

// importFilename is name without any directories (browsers on Windows may
// send backslashes), cut to maxImportFilenameBytes at a character boundary.
func importFilename(name string) string {
	name = name[strings.LastIndexAny(name, `/\`)+1:]
	if len(name) <= maxImportFilenameBytes {
		return name
	}
	name = name[:maxImportFilenameBytes]
	for !utf8.ValidString(name) {
		name = name[:len(name)-1]
	}
	return name
}

// importRow is one feed on an import's results page.
type importRow struct {
	Title  string
	URL    string
	FeedID string
	Reason string // why it couldn't be added
	Host   string // to search for; "" when the URL has none
}

type importPage struct {
	Nav        navData
	Filename   string
	InProgress bool
	Done       int
	Total      int
	Summary    string

	Subscribed        []importRow
	AlreadySubscribed []importRow
	Failed            []importRow
}

var importReasons = map[rss.ImportFailure]string{
	rss.ImportInvalidURL:  "Not a valid web address.",
	rss.ImportUnreachable: "Couldn't reach the site.",
	rss.ImportNotFeed:     "There's no feed at this address.",
}

// importHandler shows an import's progress and then its results. While any
// feed is pending or running the page refreshes itself.
func (s *Server) importHandler(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if !validator.Matches(id, validator.UUIDRX) {
		s.notFoundResponse(w, r)
		return
	}

	imp, feeds, err := s.services.ImportService.Get(r.Context(), id)
	if err != nil {
		if errors.Is(err, rss.ErrNoRecord) {
			s.notFoundResponse(w, r)
		} else {
			s.serverErrorHTML(w, r, err)
		}
		return
	}
	if userID, _ := s.authenticatedUserID(r); imp.UserID != userID {
		s.notFoundResponse(w, r)
		return
	}

	data := importPage{Nav: s.nav(r, "settings"), Filename: imp.Filename, Total: len(feeds)}
	for _, f := range feeds {
		// f.Error is internal detail and must never reach the template.
		row := importRow{Title: f.Title, URL: f.URL, FeedID: f.FeedID}
		switch f.Status {
		case rss.ImportSubscribed, rss.ImportAlreadySubscribed:
			row.Title = cmp.Or(row.Title, f.FeedTitle, f.URL)
			if f.Status == rss.ImportSubscribed {
				data.Subscribed = append(data.Subscribed, row)
			} else {
				data.AlreadySubscribed = append(data.AlreadySubscribed, row)
			}
		case rss.ImportFailed:
			row.Title = cmp.Or(row.Title, f.URL)
			row.Reason = importReasons[f.Failure]
			if u, err := discovery.ParseInput(f.URL); err == nil {
				row.Host = u.Hostname()
			}
			data.Failed = append(data.Failed, row)
		default:
			continue
		}
		data.Done++
	}
	data.InProgress = data.Done < data.Total
	data.Summary = importSummary(len(data.Subscribed), len(data.AlreadySubscribed), len(data.Failed))

	if err := s.renderHTML(w, http.StatusOK, "import.html", data); err != nil {
		s.serverErrorHTML(w, r, err)
	}
}

// importSummary is the finished import's one-line outcome, leaving out the
// groups with no feeds.
func importSummary(subscribed, already, failed int) string {
	var parts []string
	if subscribed > 0 {
		parts = append(parts, fmt.Sprintf("%d subscribed", subscribed))
	}
	if already > 0 {
		parts = append(parts, fmt.Sprintf("%d already subscribed", already))
	}
	if failed > 0 {
		parts = append(parts, fmt.Sprintf("%d couldn't be added", failed))
	}
	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, ", ") + "."
}
