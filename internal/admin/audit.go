package admin

import (
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"github.com/grodier/rss/internal/rss"
	"github.com/grodier/rss/internal/validator"
)

// The audit log records every admin action and login (see "Admin" in
// docs/decisions.md). An action that changes something passes who did it
// (actor) to the repository method that makes the change, which records the
// change in the same transaction, so it's never made without its record;
// audit records what changes nothing, the logins.

// auditPageSize is how many entries the audit log's page shows at once.
const auditPageSize = 100

// actor is who sent r, for the audit log: the logged-in admin, if any, from
// the web, with the client's IP.
func (s *Server) actor(r *http.Request) rss.AuditActor {
	by := rss.AuditActor{Via: rss.AuditViaWeb, IP: s.clientIP(r)}
	if a, ok := s.authenticatedAdmin(r); ok {
		by.AdminID, by.AdminEmail = a.ID, a.Email
	}
	return by
}

// audit records action by r's actor, or, before anyone is logged in, by the
// admin with adminID or the submitted email.
func (s *Server) audit(r *http.Request, action, adminID, email string) error {
	by := s.actor(r)
	if by.AdminID == "" {
		by.AdminID, by.AdminEmail = adminID, email
	}
	return s.services.Audit.Record(r.Context(), rss.AuditEntry{AuditActor: by, Action: action})
}

// confirmDestructive checks, before a destructive action, that the admin
// typed the target's name (an email, a feed title) in the form's field,
// ignoring case and the spaces around it. It adds the error to field when
// they didn't, and reports whether they did.
func confirmDestructive(v *validator.Validator, field, typed, name string) bool {
	ok := strings.EqualFold(strings.TrimSpace(typed), strings.TrimSpace(name))
	v.CheckField(ok, field, fmt.Sprintf("Type %s to confirm.", name))
	return ok
}

// auditPage is the audit log's data.
type auditPage struct {
	Nav    navData
	Filter auditQuery
	Rows   []auditRow
	// Next is the URL of the page of older entries, "" on the last page.
	Next string
}

// auditQuery is the audit log's filters, from its URL.
type auditQuery struct {
	Action     string
	TargetType string
	TargetID   string
}

// IsSet reports whether any filter is set.
func (q auditQuery) IsSet() bool {
	return q.Action != "" || q.TargetType != "" || q.TargetID != ""
}

// values returns the filters as query parameters.
func (q auditQuery) values() url.Values {
	v := url.Values{}
	for k, val := range map[string]string{"action": q.Action, "target_type": q.TargetType, "target_id": q.TargetID} {
		if val != "" {
			v.Set(k, val)
		}
	}
	return v
}

// auditRow is one entry as the page shows it.
type auditRow struct {
	rss.AuditEvent
	Changes []auditChange
	// History is the URL of its target's entries, "" with no target.
	History string
}

// auditChange is a field an action changed, with its values before and
// after as JSON, "" when it had none.
type auditChange struct {
	Field, Before, After string
}

// auditHistoryURL is the audit log filtered to one record: the "History"
// link of its row here, and of its record's admin page.
func auditHistoryURL(targetType, targetID string) string {
	return "/audit?" + auditQuery{TargetType: targetType, TargetID: targetID}.values().Encode()
}

// auditHandler shows the audit log, newest first, auditPageSize entries at
// a time: filtered by ?action=, ?target_type= and ?target_id=, from before
// the entry ?before=.
func (s *Server) auditHandler(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	query := auditQuery{
		Action:     strings.TrimSpace(q.Get("action")),
		TargetType: strings.TrimSpace(q.Get("target_type")),
		TargetID:   strings.TrimSpace(q.Get("target_id")),
	}
	var before int64
	if b := q.Get("before"); b != "" {
		var err error
		if before, err = strconv.ParseInt(b, 10, 64); err != nil || before <= 0 {
			s.errorHTML(w, r, http.StatusBadRequest, "Bad request", "That page of the audit log doesn't exist.")
			return
		}
	}

	// One more than a page, to know whether there's another.
	filter := rss.AuditFilter{Action: query.Action, TargetType: query.TargetType, TargetID: query.TargetID, BeforeID: before}
	events, err := s.services.Audit.List(r.Context(), filter, auditPageSize+1)
	if err != nil {
		s.serverErrorHTML(w, r, err)
		return
	}

	page := auditPage{Nav: s.nav(r, "audit"), Filter: query}
	if len(events) > auditPageSize {
		events = events[:auditPageSize]
		next := query.values()
		next.Set("before", strconv.FormatInt(events[len(events)-1].ID, 10))
		page.Next = "/audit?" + next.Encode()
	}
	for _, e := range events {
		row := auditRow{AuditEvent: e}
		row.Changes, err = auditChanges(e.Before, e.After)
		if err != nil {
			s.serverErrorHTML(w, r, err)
			return
		}
		if e.TargetType != "" {
			row.History = auditHistoryURL(e.TargetType, e.TargetID)
		}
		page.Rows = append(page.Rows, row)
	}

	if err := s.renderHTML(w, http.StatusOK, "audit.html", page); err != nil {
		s.serverErrorHTML(w, r, err)
	}
}

// auditChanges lists the fields in before or after, by name, with their
// values as JSON. The template escapes them like any text.
func auditChanges(before, after map[string]any) ([]auditChange, error) {
	fields := slices.Collect(maps.Keys(before))
	for k := range after {
		if _, ok := before[k]; !ok {
			fields = append(fields, k)
		}
	}
	slices.Sort(fields)

	encode := func(m map[string]any, k string) (string, error) {
		v, ok := m[k]
		if !ok {
			return "", nil
		}
		b, err := json.Marshal(v)
		return string(b), err
	}
	changes := make([]auditChange, 0, len(fields))
	for _, k := range fields {
		b, err := encode(before, k)
		if err != nil {
			return nil, err
		}
		a, err := encode(after, k)
		if err != nil {
			return nil, err
		}
		changes = append(changes, auditChange{Field: k, Before: b, After: a})
	}
	return changes, nil
}
