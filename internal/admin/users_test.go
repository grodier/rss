package admin

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/grodier/rss/internal/rss"
)

func userID(n int) string { return fmt.Sprintf("40000000-0000-4000-8000-%012d", n) }

// fakeUsers is a UserStore: ListUsers returns rows, at most the query's
// limit, keeping the query it was asked; User returns user when its ID
// matches, else rss.ErrNoRecord; both return err when it's set.
type fakeUsers struct {
	mu      sync.Mutex
	rows    []rss.AdminUserRow
	queries []rss.AdminUserQuery
	user    rss.AdminUser
	err     error
}

func (f *fakeUsers) ListUsers(_ context.Context, q rss.AdminUserQuery) ([]rss.AdminUserRow, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.queries = append(f.queries, q)
	if f.err != nil {
		return nil, f.err
	}
	return f.rows[:min(len(f.rows), q.Limit)], nil
}

func (f *fakeUsers) User(_ context.Context, id string) (rss.AdminUser, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return rss.AdminUser{}, f.err
	}
	if id != f.user.ID {
		return rss.AdminUser{}, rss.ErrNoRecord
	}
	return f.user, nil
}

func usersServer(t *testing.T, users *fakeUsers) *browser {
	t.Helper()
	s, _ := newTestServerWithAdminConfig(t, Config{Port: 4001, Env: "development", Version: "test", BaseURL: "http://localhost:4001"},
		Services{Database: pinger{}, Users: users, Audit: &fakeAudit{}})
	return loggedIn(t, s)
}

// testUserRow is user n: confirmed, joined n days ago and seen an hour
// ago.
func testUserRow(n int) rss.AdminUserRow {
	return rss.AdminUserRow{
		ID: userID(n), Name: fmt.Sprintf("Reader %d", n), Email: fmt.Sprintf("reader%d@example.com", n),
		EmailConfirmedAt: testNow.Add(-time.Duration(n) * 24 * time.Hour),
		CreatedAt:        testNow.Add(-time.Duration(n) * 24 * time.Hour),
		LastSeenAt:       testNow.Add(-time.Hour),
	}
}

func TestUsersPageQuery(t *testing.T) {
	users := &fakeUsers{rows: []rss.AdminUserRow{testUserRow(1)}}
	b := usersServer(t, users)

	tests := []struct {
		query string
		want  rss.AdminUserQuery
	}{
		{"", rss.AdminUserQuery{}},
		{"?q=+Ann+", rss.AdminUserQuery{Q: "Ann"}},
		{"?q=ann&after=1700000000_" + userID(9), rss.AdminUserQuery{Q: "ann", After: rss.AdminUserCursor{CreatedAt: time.Unix(1700000000, 0), ID: userID(9)}}},
	}
	for _, tt := range tests {
		rr := b.get(t, "/users"+tt.query)
		if rr.Code != http.StatusOK {
			t.Errorf("GET /users%s = %d, want 200", tt.query, rr.Code)
			continue
		}
		tt.want.Limit = usersPageSize + 1
		if got := users.queries[len(users.queries)-1]; got != tt.want {
			t.Errorf("GET /users%s asked %+v, want %+v", tt.query, got, tt.want)
		}
	}

	// A cursor that isn't one: no time, a bad time, a bad ID.
	for _, after := range []string{"nope", userID(9), "x_" + userID(9), "1700000000_nope", "1700000000_"} {
		if rr := b.get(t, "/users?after="+after); rr.Code != http.StatusNotFound {
			t.Errorf("GET /users?after=%s = %d, want 404", after, rr.Code)
		}
	}
	// A page after the last is missing.
	users.rows = nil
	if rr := b.get(t, "/users?after=1700000000_"+userID(9)); rr.Code != http.StatusNotFound {
		t.Errorf("GET /users?after= past the end = %d, want 404", rr.Code)
	}

	users.err = errors.New("boom")
	if rr := b.get(t, "/users"); rr.Code != http.StatusInternalServerError {
		t.Errorf("GET /users with the store failing = %d, want 500", rr.Code)
	}
}

func TestUsersPage(t *testing.T) {
	unconfirmed := testUserRow(2)
	unconfirmed.Name, unconfirmed.EmailConfirmedAt, unconfirmed.LastSeenAt = `<b>Bold</b>`, time.Time{}, time.Time{}
	users := &fakeUsers{rows: []rss.AdminUserRow{testUserRow(1), unconfirmed}}
	b := usersServer(t, users)

	rr := b.get(t, "/users?q=re")
	if rr.Code != http.StatusOK {
		t.Fatalf("GET /users = %d, want 200", rr.Code)
	}
	body := rr.Body.String()
	for _, want := range []string{
		`<a href="/users" aria-current="page">Users</a>`,
		`value="re"`,
		`<a class="btn btn-sm" href="/users">Clear</a>`,
		`<a href="/users/` + userID(1) + `">Reader 1</a>`,
		`reader1@example.com`,
		`<span class="status status-redeemed">Confirmed</span>`,
		`title="2026-11-08 12:00 UTC">24 h ago</time>`,
		`title="2026-11-09 11:00 UTC">1 h ago</time>`,
		// Names are escaped text.
		`<a href="/users/` + userID(2) + `">&lt;b&gt;Bold&lt;/b&gt;</a>`,
		`<span class="status status-pending">Not confirmed</span>`,
		`<td>Never</td>`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("GET /users lacks %s", want)
		}
	}
	if strings.Contains(body, "<b>Bold</b>") {
		t.Error("GET /users renders a name as markup")
	}
	if strings.Contains(body, "Older") {
		t.Error("GET /users with one page links to an older one")
	}
}

func TestUsersPagePaging(t *testing.T) {
	var rows []rss.AdminUserRow
	for i := range usersPageSize + 1 {
		rows = append(rows, testUserRow(i+1))
	}
	users := &fakeUsers{rows: rows}
	b := usersServer(t, users)

	body := b.get(t, "/users?q=reader").Body.String()
	if n := strings.Count(body, `<a href="/users/`); n != usersPageSize {
		t.Errorf("GET /users lists %d users, want %d", n, usersPageSize)
	}
	if want := `<a class="btn btn-sm" href="/users?after=` + userCursor(rows[usersPageSize-1]) + `&amp;q=reader">Older</a>`; !strings.Contains(body, want) {
		t.Errorf("GET /users lacks %s", want)
	}
}

func TestUserCursor(t *testing.T) {
	row := testUserRow(3)
	got, ok := parseUserCursor(userCursor(row))
	if !ok || !got.CreatedAt.Equal(row.CreatedAt) || got.ID != row.ID {
		t.Errorf("parseUserCursor(userCursor(row)) = %+v, %v, want the row's time and ID", got, ok)
	}
}

func TestUsersPageEmpty(t *testing.T) {
	b := usersServer(t, &fakeUsers{})
	if body := b.get(t, "/users").Body.String(); !strings.Contains(body, "No users yet.") {
		t.Error("GET /users with no users lacks its empty line")
	}
	if body := b.get(t, "/users?q=%3Czz%3E").Body.String(); !strings.Contains(body, "No users’ email or name starts with “&lt;zz&gt;”.") {
		t.Error("GET /users?q= finding none lacks its empty line")
	}
}

// testUser is user 1's page: invited in a wave, with counts, two emails
// (one failed) and an outstanding link.
func testUser() rss.AdminUser {
	return rss.AdminUser{
		AdminUserRow: testUserRow(1),
		Invite:       rss.Invite{ID: "i1", Wave: "<i>Wave 3</i>", CreatedAt: testNow.Add(-72 * time.Hour), UserID: userID(1)},
		Counts: rss.AdminUserCounts{Devices: 2, Subscriptions: 1234, Tags: 5, Timelines: 3, SharedTimelines: 1,
			Saved: 40, Starred: 7, Imports: 2, ImportedFeeds: 120, ImportFailures: 3},
		Emails: []rss.AdminUserEmail{
			{ID: "e2", To: "reader1@example.com", Subject: "<b>Reset</b> your password", Status: "failed", Attempts: 5, CreatedAt: testNow.Add(-time.Hour)},
			{ID: "e1", Subject: "Confirm your email", Status: "sent", Attempts: 1, CreatedAt: testNow.Add(-48 * time.Hour), SentAt: testNow.Add(-48 * time.Hour)},
		},
		Tokens: []rss.AdminUserToken{
			{Purpose: rss.TokenChangeEmail, Email: "new@example.com", CreatedAt: testNow.Add(-time.Hour), ExpiresAt: testNow.Add(23 * time.Hour)},
		},
	}
}

func TestUserPage(t *testing.T) {
	b := usersServer(t, &fakeUsers{user: testUser()})

	rr := b.get(t, "/users/"+userID(1))
	if rr.Code != http.StatusOK {
		t.Fatalf("GET /users/{id} = %d, want 200", rr.Code)
	}
	body := rr.Body.String()
	for _, want := range []string{
		`<a href="/users" aria-current="page">Users</a>`,
		`<h1>Reader 1</h1>`,
		`<a class="btn btn-sm" href="/audit?target_id=` + userID(1) + `&amp;target_type=user">History</a>`,
		// Account.
		`<dt>Email confirmed</dt><dd>24 h ago <small class="quiet">2026-11-08 12:00 UTC</small></dd>`,
		`<dt>Last seen</dt><dd>1 h ago <small class="quiet">2026-11-09 11:00 UTC</small></dd>`,
		`Wave “&lt;i&gt;Wave 3&lt;/i&gt;”, 2026-11-06`,
		`<code>` + userID(1) + `</code>`,
		// Counts.
		`<dt>Devices</dt>`, `<dd>2</dd>`,
		`<dt>Subscriptions</dt>`, `<dd>1,234</dd>`,
		`<dt>Timelines</dt>`, `1 shared`,
		`<dt>Imports</dt>`, `120 feeds subscribed, 3 failed`,
		// Email: escaped subjects, statuses and attempts; the link.
		`&lt;b&gt;Reset&lt;/b&gt; your password`,
		`<span class="status status-danger">Failed</span>`,
		`<span class="status status-redeemed">Sent</span>`,
		`<td class="num">5</td>`,
		`<td class="wrap">reader1@example.com</td>`,
		`<td>Change email</td>`,
		`new@example.com`,
		`title="2026-11-10 11:00 UTC">in 23 h</time>`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("GET /users/{id} lacks %s", want)
		}
	}
	for _, bad := range []string{"<b>Reset</b>", "<i>Wave 3</i>"} {
		if strings.Contains(body, bad) {
			t.Errorf("GET /users/{id} renders %s as markup", bad)
		}
	}
}

func TestUserPageBare(t *testing.T) {
	// Unconfirmed, never seen, no invite, nothing counted, no emails or
	// links.
	user := rss.AdminUser{AdminUserRow: rss.AdminUserRow{ID: userID(3), Email: "new@example.com", CreatedAt: testNow.Add(-time.Hour)}}
	b := usersServer(t, &fakeUsers{user: user})

	body := b.get(t, "/users/"+userID(3)).Body.String()
	for _, want := range []string{
		`<h1>(no name)</h1>`,
		`<dt>Email confirmed</dt><dd><span class="status status-pending">Not confirmed</span></dd>`,
		`<dt>Last seen</dt><dd>Never</dd>`,
		`<dt>Invite</dt><dd class="wrap">None</dd>`,
		`none shared`,
		`none yet`,
		`No emails to them in the outbox`,
		`No email links waiting to be used.`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("GET /users/{id} of a bare account lacks %s", want)
		}
	}
}

func TestUserPageMissing(t *testing.T) {
	users := &fakeUsers{user: testUser()}
	b := usersServer(t, users)
	for _, path := range []string{"/users/" + userID(2), "/users/nope"} {
		if rr := b.get(t, path); rr.Code != http.StatusNotFound {
			t.Errorf("GET %s = %d, want 404", path, rr.Code)
		}
	}
	users.err = errors.New("boom")
	if rr := b.get(t, "/users/"+userID(1)); rr.Code != http.StatusInternalServerError {
		t.Errorf("GET /users/{id} with the store failing = %d, want 500", rr.Code)
	}
}

// TestUserPageHoldsNoReading checks that nothing a reader's page is given
// could hold their reading, an email's body or a link's hash ("Admin" in
// docs/decisions.md): its only lists are of counts, emails and links, and
// no field anywhere in it is named for a body or a hash. A store method
// returning reading data would need a field here to reach the page.
func TestUserPageHoldsNoReading(t *testing.T) {
	lists := map[reflect.Type]bool{
		reflect.TypeFor[statTile]():           true,
		reflect.TypeFor[userEmail]():          true,
		reflect.TypeFor[userToken]():          true,
		reflect.TypeFor[rss.AdminUserEmail](): true,
		reflect.TypeFor[rss.AdminUserToken](): true,
	}
	forbidden := []string{"body", "hash", "html", "content"}
	seen := map[reflect.Type]bool{}
	var walk func(path string, typ reflect.Type)
	walk = func(path string, typ reflect.Type) {
		if seen[typ] {
			return
		}
		seen[typ] = true
		switch typ.Kind() {
		case reflect.Slice, reflect.Array, reflect.Map:
			if !lists[typ.Elem()] {
				t.Errorf("%s is a list of %s, which isn't counts, emails or links", path, typ.Elem())
				return
			}
			walk(path+"[]", typ.Elem())
		case reflect.Pointer, reflect.Interface, reflect.Func, reflect.Chan:
			t.Errorf("%s is a %s, which could hold anything", path, typ.Kind())
		case reflect.Struct:
			if typ.PkgPath() == "time" {
				return
			}
			for f := range typ.Fields() {
				for _, bad := range forbidden {
					if strings.Contains(strings.ToLower(f.Name), bad) {
						t.Errorf("%s.%s may hold an email's body or a hash", path, f.Name)
					}
				}
				walk(path+"."+f.Name, f.Type)
			}
		}
	}
	walk("userPage", reflect.TypeFor[userPage]())
}
