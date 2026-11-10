package admin

import (
	"context"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/grodier/rss/internal/rss"
	"github.com/grodier/rss/internal/validator"
)

// The audit log's transactions (an action's change and its row commit
// together, or neither does) are the repositories': see
// TestAuditInTransaction in internal/psql.

func TestActorAndAudit(t *testing.T) {
	s, _ := newTestServerWithAdmin(t)
	audit := s.services.Audit.(*fakeAudit)
	admin, err := s.services.Admins.GetByEmail(t.Context(), testEmail)
	if err != nil {
		t.Fatal(err)
	}

	anon := httptest.NewRequest("POST", "/", nil)
	anon.RemoteAddr = "203.0.113.9:1234"
	loggedIn := anon.WithContext(context.WithValue(anon.Context(), authenticatedAdminContextKey, admin))

	if got, want := s.actor(anon), (rss.AuditActor{Via: "web", IP: "203.0.113.9"}); got != want {
		t.Errorf("actor(anonymous) = %+v, want %+v", got, want)
	}
	if got, want := s.actor(loggedIn), (rss.AuditActor{AdminID: admin.ID, AdminEmail: testEmail, Via: "web", IP: "203.0.113.9"}); got != want {
		t.Errorf("actor(logged in) = %+v, want %+v", got, want)
	}

	// Before anyone is logged in, the admin or email given; after, the
	// logged-in admin.
	for _, call := range []struct {
		r              *http.Request
		action, id, em string
	}{
		{anon, "login_failed", "", "typed@example.com"},
		{anon, "login", "pending-id", ""},
		{loggedIn, "logout", "", ""},
		{loggedIn, "login_failed", "other", "ignored@example.com"},
	} {
		if err := s.audit(call.r, call.action, call.id, call.em); err != nil {
			t.Fatal(err)
		}
	}
	web := func(id, email, action string) rss.AuditEntry {
		return rss.AuditEntry{AuditActor: rss.AuditActor{AdminID: id, AdminEmail: email, Via: "web", IP: "203.0.113.9"}, Action: action}
	}
	want := []rss.AuditEntry{
		web("", "typed@example.com", "login_failed"),
		web("pending-id", "", "login"),
		web(admin.ID, testEmail, "logout"),
		web(admin.ID, testEmail, "login_failed"),
	}
	var got []rss.AuditEntry
	for _, e := range audit.events {
		got = append(got, e.AuditEntry)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("recorded:\n got %+v\nwant %+v", got, want)
	}
}

func TestEnrollmentIsByTheCommandLine(t *testing.T) {
	admins := newFakeAdmins()
	step := testNow.Unix() / totpStepSeconds
	e, err := NewEnrollment("new@example.com", "a long password")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.Finish(t.Context(), admins, testKey, Code(e.secret, step), false, testNow); err != nil {
		t.Fatalf("Finish: %v", err)
	}
	e2, _ := NewEnrollment("new@example.com", "another long password")
	if _, err := e2.Finish(t.Context(), admins, testKey, Code(e2.secret, step), true, testNow); err != nil {
		t.Fatalf("Finish with reset: %v", err)
	}
	cli := rss.AuditActor{Via: "cli"}
	if want := []rss.AuditActor{cli, cli}; !reflect.DeepEqual(admins.actors, want) {
		t.Errorf("Create and Reset by %+v, want %+v", admins.actors, want)
	}
}

func TestConfirmDestructive(t *testing.T) {
	for _, tt := range []struct {
		typed, name string
		want        bool
	}{
		{"ada@example.com", "ada@example.com", true},
		{"  ADA@example.com ", "ada@example.com", true},
		{"Daring Fireball", "Daring Fireball", true},
		{"", "ada@example.com", false},
		{"ada@example.org", "ada@example.com", false},
		{"Daring", "Daring Fireball", false},
	} {
		var v validator.Validator
		if got := confirmDestructive(&v, "confirm", tt.typed, tt.name); got != tt.want {
			t.Errorf("confirmDestructive(%q, %q) = %v, want %v", tt.typed, tt.name, got, tt.want)
		}
		if tt.want {
			if !v.Valid() {
				t.Errorf("confirmDestructive(%q, %q) added errors: %v", tt.typed, tt.name, v.FieldErrors)
			}
			continue
		}
		if want := "Type " + tt.name + " to confirm."; v.FieldErrors["confirm"] != want {
			t.Errorf("confirmDestructive(%q, %q) error = %q, want %q", tt.typed, tt.name, v.FieldErrors["confirm"], want)
		}
	}
}
