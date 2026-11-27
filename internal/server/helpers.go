package server

import (
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/grodier/rss/internal/httpserve"
	"github.com/grodier/rss/internal/rss"
	"github.com/grodier/rss/internal/validator"
)

// writeJSON is only for /healthcheck (and its serverErrorJSON error path);
// see httpserve.WriteJSON.
func (s *Server) writeJSON(w http.ResponseWriter, status int, data any) error {
	return httpserve.WriteJSON(w, status, data)
}

func (s *Server) logError(r *http.Request, err error) {
	s.logger.Error(err.Error(), "method", r.Method, "uri", r.URL.RequestURI())
}

// decodePostForm parses the request body, capped at httpserve.MaxFormBytes,
// into dst. Respond to its errors with formErrorResponse.
func (s *Server) decodePostForm(w http.ResponseWriter, r *http.Request, dst any) error {
	return httpserve.DecodePostForm(w, r, s.formDecoder, dst, httpserve.MaxFormBytes)
}

// authenticatedUserID returns the ID of the logged-in user, set by the
// authenticate middleware. ok is false for anonymous requests.
func (s *Server) authenticatedUserID(r *http.Request) (id string, ok bool) {
	id, ok = r.Context().Value(authenticatedUserIDContextKey).(string)
	return id, ok && id != ""
}

// authenticatedSession returns the logged-in device's session and user, set
// by authenticate. ok is false for anonymous requests.
func (s *Server) authenticatedSession(r *http.Request) (rss.Session, bool) {
	sess, ok := r.Context().Value(authenticatedSessionContextKey).(rss.Session)
	return sess, ok
}

func (s *Server) isAuthenticated(r *http.Request) bool {
	_, ok := s.authenticatedUserID(r)
	return ok
}

// fetched reports whether r was sent by a script's fetch rather than a form
// submission or a navigation, from its Sec-Fetch-Mode: a handler answers
// it with a 204 instead of a redirect. A beacon isn't fetched (see
// scripted).
func fetched(r *http.Request) bool {
	m := r.Header.Get("Sec-Fetch-Mode")
	return m == "cors" || m == "same-origin"
}

// scripted reports whether r was sent by a script (a fetch or a beacon,
// whose mode is no-cors) rather than a navigation, from its
// Sec-Fetch-Mode. A request without the header isn't.
func scripted(r *http.Request) bool {
	m := r.Header.Get("Sec-Fetch-Mode")
	return m != "" && m != "navigate"
}

// returnForm is a form whose only field says where to go back to: the
// subscribe, unsubscribe, save and unsave buttons post it.
type returnForm struct {
	// ReturnTo is the local path to redirect back to, usually the page the
	// form was posted from.
	ReturnTo string `form:"return_to"`
}

// uuidParam returns the URL parameter name if it's a UUID. If ok is
// false it has already answered 404.
func (s *Server) uuidParam(w http.ResponseWriter, r *http.Request, name string) (id string, ok bool) {
	id = chi.URLParam(r, name)
	if !validator.Matches(id, validator.UUIDRX) {
		s.notFoundResponse(w, r)
		return "", false
	}
	return id, true
}

// visibleFeed returns the feed the URL's {id} names, as the logged-in reader
// sees it (FeedStore.GetVisible). If ok is false it has already written the
// response: 404 for an id that isn't a UUID, no such feed, or another
// reader's newsletter feed.
func (s *Server) visibleFeed(w http.ResponseWriter, r *http.Request) (feed rss.Feed, ok bool) {
	id, ok := s.uuidParam(w, r, "id")
	if !ok {
		return rss.Feed{}, false
	}
	userID, _ := s.authenticatedUserID(r)
	feed, err := s.services.FeedService.GetVisible(r.Context(), userID, id)
	if err != nil {
		if errors.Is(err, rss.ErrNoRecord) {
			s.notFoundResponse(w, r)
		} else {
			s.serverErrorHTML(w, r, err)
		}
		return rss.Feed{}, false
	}
	return feed, true
}

// visibleArticle returns the article the URL's {id} names, as the logged-in
// reader sees it (ArticleStore.GetVisible). If ok is false it has already
// written the response: 404 for an id that isn't a UUID, no such article,
// or an article in another reader's newsletter feed.
func (s *Server) visibleArticle(w http.ResponseWriter, r *http.Request) (article rss.Article, ok bool) {
	id, ok := s.uuidParam(w, r, "id")
	if !ok {
		return rss.Article{}, false
	}
	userID, _ := s.authenticatedUserID(r)
	article, err := s.services.ArticleService.GetVisible(r.Context(), userID, id)
	if err != nil {
		if errors.Is(err, rss.ErrNoRecord) {
			s.notFoundResponse(w, r)
		} else {
			s.serverErrorHTML(w, r, err)
		}
		return rss.Article{}, false
	}
	return article, true
}
