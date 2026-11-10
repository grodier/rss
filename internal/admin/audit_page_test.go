package admin

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/grodier/rss/internal/rss"
)

// auditTestServer returns a logged-in browser and the server's fakeAudit,
// which then holds only the test admin's login.
func auditTestServer(t *testing.T) (*browser, *fakeAudit) {
	t.Helper()
	s, _ := newTestServerWithAdmin(t)
	b := loggedIn(t, s)
	return b, s.services.Audit.(*fakeAudit)
}

func TestAuditPage(t *testing.T) {
	b, audit := auditTestServer(t)
	audit.Record(t.Context(), rss.AuditEntry{Via: "cli", Action: "invite.create", TargetType: "invite", TargetID: "inv-1",
		After: map[string]any{"note": "for Grace"}})
	audit.Record(t.Context(), rss.AuditEntry{AdminEmail: testEmail, Via: "web", IP: "203.0.113.9", Action: "user.update", TargetType: "user", TargetID: "u-1",
		Before: map[string]any{"name": `<script>alert("x")</script>`}, After: map[string]any{"name": "Ada", "admin": true}})

	rr := b.get(t, "/audit")
	if rr.Code != http.StatusOK {
		t.Fatalf("GET /audit = %d:\n%s", rr.Code, rr.Body)
	}
	body := rr.Body.String()
	for _, want := range []string{
		`<a href="/audit" aria-current="page">Audit log</a>`,
		"invite.create", "Command line", "user.update", "203.0.113.9", "login",
		`href="/audit?target_id=u-1&amp;target_type=user">History</a>`,
		// Before and after values are JSON, escaped.
		`<dt>name</dt>`,
		`<code>&#34;\u003cscript\u003ealert(\&#34;x\&#34;)\u003c/script\u003e&#34;</code> → <code>&#34;Ada&#34;</code>`,
		`<dt>admin</dt>`, `<code>—</code> → <code>true</code>`,
		`<code>—</code> → <code>&#34;for Grace&#34;</code>`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("GET /audit lacks %s", want)
		}
	}
	if strings.Contains(body, "<script>") {
		t.Error("GET /audit renders a before value as markup")
	}
	// Newest first.
	if strings.Index(body, "user.update") > strings.Index(body, "invite.create") {
		t.Error("GET /audit isn't newest first")
	}
	if strings.Contains(body, ">Older<") {
		t.Error("GET /audit links to older entries, with none")
	}
}

func TestAuditPageFilters(t *testing.T) {
	b, audit := auditTestServer(t)
	audit.Record(t.Context(), rss.AuditEntry{Via: "cli", Action: "invite.create", TargetType: "invite", TargetID: "inv-1"})
	audit.Record(t.Context(), rss.AuditEntry{Via: "cli", Action: "invite.revoke", TargetType: "invite", TargetID: "inv-1"})
	audit.Record(t.Context(), rss.AuditEntry{Via: "cli", Action: "invite.create", TargetType: "invite", TargetID: "inv-2"})

	for _, tt := range []struct {
		query         string
		want, notWant []string
	}{
		{"?action=invite.create", []string{"inv-1", "inv-2"}, []string{"invite.revoke", ">login<"}},
		{"?target_type=invite&target_id=inv-1", []string{"invite.create", "invite.revoke"}, []string{"inv-2", ">login<"}},
		{"?target_type=invite", []string{"inv-1", "inv-2"}, []string{">login<"}},
		{"?action=user.delete", []string{"Nothing matches these filters.", `href="/audit">Clear</a>`}, []string{"<table"}},
	} {
		rr := b.get(t, "/audit"+tt.query)
		if rr.Code != http.StatusOK {
			t.Fatalf("GET /audit%s = %d", tt.query, rr.Code)
		}
		body := rr.Body.String()
		for _, w := range tt.want {
			if !strings.Contains(body, w) {
				t.Errorf("GET /audit%s lacks %s", tt.query, w)
			}
		}
		for _, w := range tt.notWant {
			if strings.Contains(body, w) {
				t.Errorf("GET /audit%s has %s", tt.query, w)
			}
		}
	}
}

func TestAuditPagePagination(t *testing.T) {
	b, audit := auditTestServer(t)
	// The login is entry 1; these are 2 to auditPageSize+11.
	for i := range auditPageSize + 10 {
		audit.Record(t.Context(), rss.AuditEntry{Via: "cli", Action: "invite.create", TargetType: "invite", TargetID: fmt.Sprintf("inv-%d", i)})
	}

	rr := b.get(t, "/audit?action=invite.create")
	body := rr.Body.String()
	if n := strings.Count(body, "<tr>") - 1; n != auditPageSize {
		t.Errorf("first page has %d rows, want %d", n, auditPageSize)
	}
	// The oldest on the first page is inv-10, entry 12.
	next := `<a class="btn btn-sm" href="/audit?action=invite.create&amp;before=12">Older</a>`
	if !strings.Contains(body, next) {
		t.Fatalf("first page lacks %s", next)
	}

	rr = b.get(t, "/audit?action=invite.create&before=12")
	body = rr.Body.String()
	if n := strings.Count(body, "<tr>") - 1; n != 10 {
		t.Errorf("second page has %d rows, want 10", n)
	}
	if !strings.Contains(body, ">invite inv-0 <") || strings.Contains(body, ">invite inv-10 <") || strings.Contains(body, ">Older<") {
		t.Errorf("second page:\n%s", body)
	}

	for _, bad := range []string{"x", "-1", "0"} {
		if rr := b.get(t, "/audit?before="+bad); rr.Code != http.StatusBadRequest {
			t.Errorf("GET /audit?before=%s = %d, want 400", bad, rr.Code)
		}
	}
}
