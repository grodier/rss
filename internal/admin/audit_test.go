package admin

import (
	"context"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/grodier/rss/internal/rss"
	"github.com/grodier/rss/internal/validator"
)

// The audit log's transactions (an action's change and its row commit
// together, or neither does) are the repositories': see
// TestAuditInTransaction in internal/psql.

func TestAudit(t *testing.T) {
	s, _ := newTestServerWithAdmin(t)
	audit := s.services.Audit.(*fakeAudit)
	admin, err := s.services.Admins.GetByEmail(t.Context(), testEmail)
	if err != nil {
		t.Fatal(err)
	}

	r := httptest.NewRequest("POST", "/", nil)
	r.RemoteAddr = "203.0.113.9:1234"
	if err := s.audit(r, rss.AuditEntry{Action: "login_failed", AdminEmail: "typed@example.com", Via: "cli", IP: "spoofed"}); err != nil {
		t.Fatal(err)
	}
	// The logged-in admin, unless the entry names one.
	r = r.WithContext(context.WithValue(r.Context(), authenticatedAdminContextKey, admin))
	if err := s.audit(r, rss.AuditEntry{Action: "invite.create", TargetType: "invite", TargetID: "i1"}); err != nil {
		t.Fatal(err)
	}
	if err := s.audit(r, rss.AuditEntry{Action: "login_failed", AdminID: "other"}); err != nil {
		t.Fatal(err)
	}

	want := []rss.AuditEntry{
		{AdminEmail: "typed@example.com", Via: "web", IP: "203.0.113.9", Action: "login_failed"},
		{AdminID: admin.ID, AdminEmail: testEmail, Via: "web", IP: "203.0.113.9", Action: "invite.create", TargetType: "invite", TargetID: "i1"},
		{AdminID: "other", Via: "web", IP: "203.0.113.9", Action: "login_failed"},
	}
	var got []rss.AuditEntry
	for _, e := range audit.events {
		got = append(got, e.AuditEntry)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("recorded:\n got %+v\nwant %+v", got, want)
	}
}

func TestEnrollmentIsAudited(t *testing.T) {
	admins := newFakeAdmins()
	step := testNow.Unix() / totpStepSeconds
	e, err := NewEnrollment("new@example.com", "a long password")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.Finish(t.Context(), admins, testKey, Code(e.secret, step), false, testNow); err != nil {
		t.Fatalf("Finish: %v", err)
	}
	id := admins.byEmail("new@example.com").ID
	e2, _ := NewEnrollment("new@example.com", "another long password")
	if _, err := e2.Finish(t.Context(), admins, testKey, Code(e2.secret, step), true, testNow); err != nil {
		t.Fatalf("Finish with reset: %v", err)
	}

	want := []rss.AuditEntry{
		{Via: "cli", Action: "admin.create", TargetType: "admin", TargetID: id, After: map[string]any{"email": "new@example.com"}},
		{Via: "cli", Action: "admin.reset", TargetType: "admin", TargetID: id},
	}
	if !reflect.DeepEqual(admins.audit, want) {
		t.Errorf("recorded:\n got %+v\nwant %+v", admins.audit, want)
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
