package discordgo

import (
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// syncTestCommand builds a chat (slash) command with the given name and
// description.
func syncTestCommand(name, description string) *ApplicationCommand {
	return &ApplicationCommand{
		Type:        ChatApplicationCommand,
		Name:        name,
		Description: description,
	}
}

func TestApplicationCommandsEqual(t *testing.T) {
	int64Ptr := func(v int64) *int64 { return &v }
	boolPtr := func(v bool) *bool { return &v }

	tests := []struct {
		name string
		a    *ApplicationCommand
		b    *ApplicationCommand
		want bool
	}{
		{
			name: "identical content",
			a:    syncTestCommand("ping", "Replies with pong"),
			b:    syncTestCommand("ping", "Replies with pong"),
			want: true,
		},
		{
			name: "both nil",
			a:    nil,
			b:    nil,
			want: true,
		},
		{
			name: "nil vs non-nil",
			a:    nil,
			b:    syncTestCommand("ping", "d"),
			want: false,
		},
		{
			name: "server-assigned fields ignored",
			a: func() *ApplicationCommand {
				c := syncTestCommand("ping", "d")
				c.ID = "1001"
				c.ApplicationID = "app1"
				c.Version = "9"
				c.GuildID = "55"
				return c
			}(),
			b:    syncTestCommand("ping", "d"),
			want: true,
		},
		{
			name: "unset type equals chat type",
			a:    &ApplicationCommand{Name: "ping", Description: "d"},
			b:    syncTestCommand("ping", "d"),
			want: true,
		},
		{
			name: "command name case-insensitive for slash commands",
			a:    syncTestCommand("Ping", "d"),
			b:    syncTestCommand("ping", "d"),
			want: true,
		},
		{
			name: "command name case-sensitive for context menu commands",
			a:    &ApplicationCommand{Type: UserApplicationCommand, Name: "My Cmd"},
			b:    &ApplicationCommand{Type: UserApplicationCommand, Name: "my cmd"},
			want: false,
		},
		{
			name: "nil options equal empty options",
			a:    syncTestCommand("ping", "d"),
			b: func() *ApplicationCommand {
				c := syncTestCommand("ping", "d")
				c.Options = []*ApplicationCommandOption{}
				return c
			}(),
			want: true,
		},
		{
			name: "option name case-insensitive for slash commands",
			a: func() *ApplicationCommand {
				c := syncTestCommand("ping", "d")
				c.Options = []*ApplicationCommandOption{{Type: ApplicationCommandOptionString, Name: "Target", Description: "t"}}
				return c
			}(),
			b: func() *ApplicationCommand {
				c := syncTestCommand("ping", "d")
				c.Options = []*ApplicationCommandOption{{Type: ApplicationCommandOptionString, Name: "target", Description: "t"}}
				return c
			}(),
			want: true,
		},
		{
			name: "default_permission true equals nil",
			a: func() *ApplicationCommand {
				c := syncTestCommand("ping", "d")
				c.DefaultPermission = boolPtr(true)
				return c
			}(),
			b:    syncTestCommand("ping", "d"),
			want: true,
		},
		{
			name: "default_permission false differs from nil",
			a: func() *ApplicationCommand {
				c := syncTestCommand("ping", "d")
				c.DefaultPermission = boolPtr(false)
				return c
			}(),
			b:    syncTestCommand("ping", "d"),
			want: false,
		},
		{
			name: "zero default member permissions equal nil",
			a: func() *ApplicationCommand {
				c := syncTestCommand("ping", "d")
				c.DefaultMemberPermissions = int64Ptr(0)
				return c
			}(),
			b:    syncTestCommand("ping", "d"),
			want: true,
		},
		{
			name: "nsfw false equals nil",
			a: func() *ApplicationCommand {
				c := syncTestCommand("ping", "d")
				c.NSFW = boolPtr(false)
				return c
			}(),
			b:    syncTestCommand("ping", "d"),
			want: true,
		},
		{
			name: "empty name localizations equal nil",
			a: func() *ApplicationCommand {
				c := syncTestCommand("ping", "d")
				m := map[Locale]string{}
				c.NameLocalizations = &m
				return c
			}(),
			b:    syncTestCommand("ping", "d"),
			want: true,
		},
		{
			name: "different description",
			a:    syncTestCommand("ping", "one"),
			b:    syncTestCommand("ping", "two"),
			want: false,
		},
		{
			name: "different option content",
			a: func() *ApplicationCommand {
				c := syncTestCommand("ping", "d")
				c.Options = []*ApplicationCommandOption{{Type: ApplicationCommandOptionString, Name: "t", Description: "a"}}
				return c
			}(),
			b: func() *ApplicationCommand {
				c := syncTestCommand("ping", "d")
				c.Options = []*ApplicationCommandOption{{Type: ApplicationCommandOptionString, Name: "t", Description: "b"}}
				return c
			}(),
			want: false,
		},
		{
			name: "different command type",
			a:    syncTestCommand("x", "d"),
			b:    &ApplicationCommand{Type: MessageApplicationCommand, Name: "x"},
			want: false,
		},
		{
			name: "choice value int equals float64",
			a: func() *ApplicationCommand {
				c := syncTestCommand("roll", "d")
				c.Options = []*ApplicationCommandOption{{
					Type:    ApplicationCommandOptionString,
					Name:    "side",
					Choices: []*ApplicationCommandOptionChoice{{Name: "six", Value: 6}},
				}}
				return c
			}(),
			b: func() *ApplicationCommand {
				c := syncTestCommand("roll", "d")
				c.Options = []*ApplicationCommandOption{{
					Type:    ApplicationCommandOptionString,
					Name:    "side",
					Choices: []*ApplicationCommandOptionChoice{{Name: "six", Value: float64(6)}},
				}}
				return c
			}(),
			// The API returns choice values as JSON numbers (decoded to
			// float64); comparing through the normalized JSON representation
			// must not report a difference for equivalent numbers.
			want: true,
		},
		{
			name: "different choice value",
			a: func() *ApplicationCommand {
				c := syncTestCommand("roll", "d")
				c.Options = []*ApplicationCommandOption{{
					Type:    ApplicationCommandOptionString,
					Name:    "side",
					Choices: []*ApplicationCommandOptionChoice{{Name: "six", Value: "6"}},
				}}
				return c
			}(),
			b: func() *ApplicationCommand {
				c := syncTestCommand("roll", "d")
				c.Options = []*ApplicationCommandOption{{
					Type:    ApplicationCommandOptionString,
					Name:    "side",
					Choices: []*ApplicationCommandOptionChoice{{Name: "six", Value: "seven"}},
				}}
				return c
			}(),
			want: false,
		},
		{
			name: "nil default_member_permissions vs non-zero",
			a:    syncTestCommand("ban", "d"),
			b: func() *ApplicationCommand {
				c := syncTestCommand("ban", "d")
				c.DefaultMemberPermissions = int64Ptr(8)
				return c
			}(),
			want: false,
		},
		{
			name: "nil vs empty-string default member permissions pointer",
			a: func() *ApplicationCommand {
				c := syncTestCommand("ban", "d")
				c.DefaultMemberPermissions = int64Ptr(0)
				return c
			}(),
			b:    syncTestCommand("ban", "d"),
			want: true,
		},
		{
			name: "localizations kept when non-empty",
			a: func() *ApplicationCommand {
				c := syncTestCommand("ping", "d")
				mm := map[Locale]string{German: "knopf"}
				c.NameLocalizations = &mm
				return c
			}(),
			b:    syncTestCommand("ping", "d"),
			want: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := ApplicationCommandsEqual(tc.a, tc.b); got != tc.want {
				t.Fatalf("ApplicationCommandsEqual(a, b) = %v, want %v", got, tc.want)
			}
			if got := ApplicationCommandsEqual(tc.b, tc.a); got != tc.want {
				t.Fatalf("ApplicationCommandsEqual(b, a) = %v, want %v (comparison must be symmetric)", got, tc.want)
			}
		})
	}
}

func TestApplicationCommandContentHash(t *testing.T) {
	a := syncTestCommand("ping", "Replies with pong")
	b := syncTestCommand("ping", "Replies with pong")
	b.ID = "999" // server-assigned field must not affect the hash

	ha, err := ApplicationCommandContentHash(a)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	hb, err := ApplicationCommandContentHash(b)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ha != hb {
		t.Fatalf("expected identical hash for equal content, got %q vs %q", ha, hb)
	}
	if len(ha) != 64 {
		t.Fatalf("expected 64-char sha256 hex, got %q", ha)
	}

	c := syncTestCommand("ping", "Replies with pong indeed")
	hc, err := ApplicationCommandContentHash(c)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if hc == ha {
		t.Fatal("expected different hash for changed content")
	}

	// Deterministic across repeated calls.
	ha2, _ := ApplicationCommandContentHash(a)
	if ha2 != ha {
		t.Fatalf("expected deterministic hash, got %q vs %q", ha, ha2)
	}
}

func TestCompareApplicationCommands(t *testing.T) {
	tests := []struct {
		name       string
		registered []*ApplicationCommand
		desired    []*ApplicationCommand
		check      func(t *testing.T, plan *ApplicationCommandSyncPlan)
	}{
		{
			name:       "all new commands are creates",
			registered: nil,
			desired:    []*ApplicationCommand{syncTestCommand("a", "d"), syncTestCommand("b", "d")},
			check: func(t *testing.T, plan *ApplicationCommandSyncPlan) {
				if len(plan.Creates) != 2 || len(plan.Updates) != 0 || len(plan.Deletes) != 0 || len(plan.Unchanged) != 0 {
					t.Fatalf("unexpected plan: %#v", plan)
				}
			},
		},
		{
			name:       "changed content is an update keeping the ID",
			registered: []*ApplicationCommand{withSyncID(syncTestCommand("a", "old"), "1001")},
			desired:    []*ApplicationCommand{syncTestCommand("a", "new")},
			check: func(t *testing.T, plan *ApplicationCommandSyncPlan) {
				if len(plan.Updates) != 1 {
					t.Fatalf("expected exactly one update, got %#v", plan)
				}
				u := plan.Updates[0]
				if u.ID != "1001" {
					t.Errorf("expected update of command 1001, got %q", u.ID)
				}
				if u.Desired.Description != "new" || u.Registered.Description != "old" {
					t.Errorf("unexpected update pair: %#v", u)
				}
			},
		},
		{
			name:       "identical content is unchanged",
			registered: []*ApplicationCommand{withSyncID(syncTestCommand("a", "same"), "1001")},
			desired:    []*ApplicationCommand{syncTestCommand("a", "same")},
			check: func(t *testing.T, plan *ApplicationCommandSyncPlan) {
				if !plan.Empty() {
					t.Fatalf("expected empty plan, got %#v", plan)
				}
				if len(plan.Unchanged) != 1 || plan.Unchanged[0].ID != "1001" {
					t.Errorf("expected command 1001 to be unchanged, got %#v", plan.Unchanged)
				}
			},
		},
		{
			name: "registered commands missing from desired are deletes",
			registered: []*ApplicationCommand{
				withSyncID(syncTestCommand("a", "d"), "1001"),
				withSyncID(syncTestCommand("stale", "d"), "1002"),
			},
			desired: []*ApplicationCommand{syncTestCommand("a", "d")},
			check: func(t *testing.T, plan *ApplicationCommandSyncPlan) {
				if len(plan.Deletes) != 1 || plan.Deletes[0].ID != "1002" {
					t.Fatalf("expected delete of 1002, got %#v", plan.Deletes)
				}
			},
		},
		{
			name: "rename is create plus delete",
			registered: []*ApplicationCommand{
				withSyncID(syncTestCommand("old-name", "d"), "1001"),
			},
			desired: []*ApplicationCommand{syncTestCommand("new-name", "d")},
			check: func(t *testing.T, plan *ApplicationCommandSyncPlan) {
				if len(plan.Creates) != 1 || plan.Creates[0].Name != "new-name" {
					t.Errorf("expected create of new-name, got %#v", plan.Creates)
				}
				if len(plan.Deletes) != 1 || plan.Deletes[0].ID != "1001" {
					t.Errorf("expected delete of 1001, got %#v", plan.Deletes)
				}
			},
		},
		{
			name: "slash command names match case-insensitively",
			registered: []*ApplicationCommand{
				withSyncID(syncTestCommand("Ping", "same"), "1001"),
			},
			desired: []*ApplicationCommand{syncTestCommand("ping", "same")},
			check: func(t *testing.T, plan *ApplicationCommandSyncPlan) {
				if !plan.Empty() {
					t.Fatalf("expected case-insensitive match to produce an empty plan, got %#v", plan)
				}
			},
		},
		{
			name: "different command types with the same name do not match",
			registered: []*ApplicationCommand{
				withSyncID(&ApplicationCommand{Type: UserApplicationCommand, Name: "x"}, "1001"),
			},
			desired: []*ApplicationCommand{syncTestCommand("x", "d")},
			check: func(t *testing.T, plan *ApplicationCommandSyncPlan) {
				if len(plan.Creates) != 1 || len(plan.Deletes) != 1 {
					t.Fatalf("expected create plus delete for type change, got %#v", plan)
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			plan, err := CompareApplicationCommands(tc.registered, tc.desired)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			tc.check(t, plan)
		})
	}

	t.Run("nil entry in desired set", func(t *testing.T) {
		_, err := CompareApplicationCommands(nil, []*ApplicationCommand{nil})
		if !errors.Is(err, ErrApplicationCommandSyncNilCommand) {
			t.Fatalf("expected ErrApplicationCommandSyncNilCommand, got %v", err)
		}
	})

	t.Run("duplicate desired name", func(t *testing.T) {
		_, err := CompareApplicationCommands(nil, []*ApplicationCommand{
			syncTestCommand("a", "one"),
			syncTestCommand("a", "two"),
		})
		if !errors.Is(err, ErrApplicationCommandSyncDuplicateName) {
			t.Fatalf("expected ErrApplicationCommandSyncDuplicateName, got %v", err)
		}
	})

	t.Run("same name with different types is allowed", func(t *testing.T) {
		plan, err := CompareApplicationCommands(nil, []*ApplicationCommand{
			syncTestCommand("a", "one"),
			{Type: UserApplicationCommand, Name: "a"},
			{Type: MessageApplicationCommand, Name: "a"},
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(plan.Creates) != 3 {
			t.Fatalf("expected three creates, got %#v", plan.Creates)
		}
	})
}

func withSyncID(cmd *ApplicationCommand, id string) *ApplicationCommand {
	cmd.ID = id
	return cmd
}

// syncTestFixture serves a realistic registered-command list and returns the
// desired set plus the expectations for a full sync.
var syncFixtureRegistered = []*ApplicationCommand{
	withSyncID(syncTestCommand("hello", "old desc"), "1001"),
	withSyncID(syncTestCommand("bye", "same desc"), "1002"),
	withSyncID(syncTestCommand("legacy", "legacy desc"), "1003"),
}

func syncFixtureDesired() []*ApplicationCommand {
	return []*ApplicationCommand{
		syncTestCommand("hello", "new desc"), // update
		syncTestCommand("bye", "same desc"),  // unchanged
		syncTestCommand("wow", "brand new"),  // create
	}
}

const syncFixtureGETResponse = `[
	{"id":"1001","application_id":"app1","type":1,"name":"hello","description":"old desc","version":"9"},
	{"id":"1002","application_id":"app1","type":1,"name":"bye","description":"same desc","version":"8"},
	{"id":"1003","application_id":"app1","type":1,"name":"legacy","description":"legacy desc","version":"7"}
]`

func TestApplicationCommandsSyncREST(t *testing.T) {
	var requests [][2]string

	s, cleanup := newFeatureTestSession(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, [2]string{r.Method, r.URL.Path})

		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/applications/app1/commands":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(syncFixtureGETResponse))
			return

		case r.Method == http.MethodPost && r.URL.Path == "/applications/app1/commands":
			body := decodeFeatureTestBody(t, r)
			if body["name"] != "wow" || body["description"] != "brand new" {
				t.Errorf("unexpected POST body: %v", body)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":"2001","application_id":"app1","type":1,"name":"wow","description":"brand new","version":"1"}`))
			return

		case r.Method == http.MethodPatch && r.URL.Path == "/applications/app1/commands/1001":
			body := decodeFeatureTestBody(t, r)
			if body["description"] != "new desc" {
				t.Errorf("PATCH body description = %v, want %v", body["description"], "new desc")
			}
			if _, has := body["id"]; has {
				t.Errorf("PATCH body must not carry the server-assigned id field, got %v", body["id"])
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":"1001","application_id":"app1","type":1,"name":"hello","description":"new desc","version":"2"}`))
			return

		case r.Method == http.MethodDelete && r.URL.Path == "/applications/app1/commands/1003":
			w.WriteHeader(http.StatusNoContent)
			return
		}

		http.NotFound(w, r)
	}))
	defer cleanup()

	report, err := s.ApplicationCommandsSync("app1", syncFixtureDesired(), &ApplicationCommandSyncOptions{DeleteRemoved: true})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Plan content.
	if report.Plan == nil {
		t.Fatal("expected plan in report")
	}
	if len(report.Plan.Creates) != 1 || report.Plan.Creates[0].Name != "wow" {
		t.Errorf("expected create of wow, got %#v", report.Plan.Creates)
	}
	if len(report.Plan.Updates) != 1 || report.Plan.Updates[0].ID != "1001" {
		t.Errorf("expected update of 1001, got %#v", report.Plan.Updates)
	}
	if len(report.Plan.Deletes) != 1 || report.Plan.Deletes[0].ID != "1003" {
		t.Errorf("expected delete of 1003, got %#v", report.Plan.Deletes)
	}
	if len(report.Plan.Unchanged) != 1 || report.Plan.Unchanged[0].ID != "1002" {
		t.Errorf("expected 1002 unchanged, got %#v", report.Plan.Unchanged)
	}

	// Applied results.
	if len(report.Created) != 1 || report.Created[0].ID != "2001" {
		t.Errorf("expected created command 2001, got %#v", report.Created)
	}
	if len(report.Updated) != 1 || report.Updated[0].ID != "1001" || report.Updated[0].Description != "new desc" {
		t.Errorf("unexpected updated command: %#v", report.Updated)
	}
	if len(report.Deleted) != 1 || report.Deleted[0] != "1003" {
		t.Errorf("expected deleted ID 1003, got %#v", report.Deleted)
	}
	if len(report.Unchanged) != 1 || report.Unchanged[0].ID != "1002" {
		t.Errorf("expected unchanged 1002 in report, got %#v", report.Unchanged)
	}
	if report.DryRun {
		t.Error("expected DryRun to be false")
	}

	// Request sequence: one GET, one POST, one PATCH, one DELETE — and no
	// request for the unchanged command.
	want := [][2]string{
		{"GET", "/applications/app1/commands"},
		{"POST", "/applications/app1/commands"},
		{"PATCH", "/applications/app1/commands/1001"},
		{"DELETE", "/applications/app1/commands/1003"},
	}
	if len(requests) != len(want) {
		t.Fatalf("expected %d requests, got %d: %v", len(want), len(requests), requests)
	}
	for i, w := range want {
		if requests[i] != w {
			t.Errorf("request %d = %v, want %v", i, requests[i], w)
		}
	}
}

func TestApplicationCommandsSyncOptions(t *testing.T) {
	t.Run("DeleteRemoved false keeps stale commands", func(t *testing.T) {
		var requests [][2]string
		s, cleanup := newFeatureTestSession(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			requests = append(requests, [2]string{r.Method, r.URL.Path})
			if r.Method == http.MethodGet && r.URL.Path == "/applications/app1/commands" {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(syncFixtureGETResponse))
				return
			}
			http.NotFound(w, r)
		}))
		defer cleanup()

		// Desired matches "hello"/"bye" content so nothing but the plan is produced.
		report, err := s.ApplicationCommandsSync("app1", []*ApplicationCommand{
			syncTestCommand("hello", "old desc"),
			syncTestCommand("bye", "same desc"),
		}, &ApplicationCommandSyncOptions{})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(report.Plan.Deletes) != 1 {
			t.Errorf("expected stale command in plan deletes, got %#v", report.Plan.Deletes)
		}
		if len(report.Deleted) != 0 {
			t.Errorf("expected no deletions without DeleteRemoved, got %v", report.Deleted)
		}
		for _, req := range requests {
			if req[0] == "DELETE" {
				t.Errorf("unexpected DELETE request: %v", req)
			}
		}
	})

	t.Run("dry run issues no modifying requests", func(t *testing.T) {
		var requests [][2]string
		s, cleanup := newFeatureTestSession(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			requests = append(requests, [2]string{r.Method, r.URL.Path})
			if r.Method == http.MethodGet && r.URL.Path == "/applications/app1/commands" {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(syncFixtureGETResponse))
				return
			}
			http.NotFound(w, r)
		}))
		defer cleanup()

		report, err := s.ApplicationCommandsSync("app1", syncFixtureDesired(), &ApplicationCommandSyncOptions{
			DryRun:        true,
			DeleteRemoved: true,
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !report.DryRun {
			t.Error("expected DryRun report")
		}
		if len(report.Created)+len(report.Updated)+len(report.Deleted) != 0 {
			t.Errorf("expected no applied changes in dry run, got %#v", report)
		}
		if len(report.Plan.Creates) != 1 || len(report.Plan.Updates) != 1 || len(report.Plan.Deletes) != 1 {
			t.Errorf("expected full plan despite dry run, got %#v", report.Plan)
		}
		for _, req := range requests {
			if req[0] != "GET" {
				t.Errorf("unexpected non-GET request in dry run: %v", req)
			}
		}
	})

	t.Run("already in sync skips all writes", func(t *testing.T) {
		var requests [][2]string
		s, cleanup := newFeatureTestSession(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			requests = append(requests, [2]string{r.Method, r.URL.Path})
			if r.Method == http.MethodGet && r.URL.Path == "/applications/app1/commands" {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(syncFixtureGETResponse))
				return
			}
			http.NotFound(w, r)
		}))
		defer cleanup()

		report, err := s.ApplicationCommandsSync("app1", []*ApplicationCommand{
			syncTestCommand("hello", "old desc"),
			syncTestCommand("bye", "same desc"),
			syncTestCommand("legacy", "legacy desc"),
		}, &ApplicationCommandSyncOptions{DeleteRemoved: true})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !report.Plan.Empty() {
			t.Fatalf("expected empty plan, got %#v", report.Plan)
		}
		for _, req := range requests {
			if req[0] != "GET" {
				t.Errorf("unexpected write request for in-sync state: %v", req)
			}
		}
	})

	t.Run("guild scope uses guild endpoints", func(t *testing.T) {
		var gotPath string
		s, cleanup := newFeatureTestSession(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/applications/app1/guilds/55/commands" {
				http.NotFound(w, r)
				return
			}
			switch r.Method {
			case http.MethodGet:
				gotPath = r.URL.Path
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`[]`))
			case http.MethodPost:
				body := decodeFeatureTestBody(t, r)
				if body["name"] != "guild-cmd" {
					t.Errorf("unexpected POST body: %v", body)
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"id":"3001","application_id":"app1","type":1,"name":"guild-cmd","description":"d","guild_id":"55"}`))
			default:
				http.NotFound(w, r)
			}
		}))
		defer cleanup()

		report, err := s.ApplicationCommandsSync("app1", []*ApplicationCommand{syncTestCommand("guild-cmd", "d")}, &ApplicationCommandSyncOptions{GuildID: "55"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if gotPath == "" {
			t.Fatal("expected a request to the guild command endpoint")
		}
		if report.GuildID != "55" {
			t.Errorf("expected report GuildID 55, got %q", report.GuildID)
		}
		if len(report.Created) != 1 {
			t.Errorf("expected one created guild command, got %#v", report.Created)
		}
	})
}

func TestApplicationCommandsSyncErrors(t *testing.T) {
	t.Run("missing application ID", func(t *testing.T) {
		s, cleanup := newFeatureTestSession(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			t.Error("no request should be made without an application ID")
		}))
		defer cleanup()

		_, err := s.ApplicationCommandsSync("", syncFixtureDesired(), nil)
		if !errors.Is(err, ErrApplicationCommandSyncMissingAppID) {
			t.Fatalf("expected ErrApplicationCommandSyncMissingAppID, got %v", err)
		}
	})

	t.Run("nil desired entry", func(t *testing.T) {
		s, cleanup := newFeatureTestSession(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`[]`))
		}))
		defer cleanup()

		_, err := s.ApplicationCommandsSync("app1", []*ApplicationCommand{nil}, nil)
		if !errors.Is(err, ErrApplicationCommandSyncNilCommand) {
			t.Fatalf("expected ErrApplicationCommandSyncNilCommand, got %v", err)
		}
	})

	t.Run("failed update aborts with partial report", func(t *testing.T) {
		s, cleanup := newFeatureTestSession(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			switch {
			case r.Method == http.MethodGet:
				_, _ = w.Write([]byte(`[{"id":"1001","application_id":"app1","type":1,"name":"hello","description":"old"}]`))
			case r.Method == http.MethodPost:
				_, _ = w.Write([]byte(`{"id":"2001","application_id":"app1","type":1,"name":"wow","description":"n"}`))
			case r.Method == http.MethodPatch:
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(`{"message":"bad edit","code":50035}`))
			default:
				http.NotFound(w, r)
			}
		}))
		defer cleanup()

		report, err := s.ApplicationCommandsSync("app1", []*ApplicationCommand{
			syncTestCommand("hello", "new"),
			syncTestCommand("wow", "n"),
		}, nil)
		if err == nil {
			t.Fatal("expected update error")
		}
		if !strings.Contains(err.Error(), "failed to update") {
			t.Errorf("expected error to mention the failed update, got %v", err)
		}
		if len(report.Created) != 1 {
			t.Errorf("expected the create to have been applied before the failure, got %#v", report.Created)
		}
		if len(report.Updated) != 0 {
			t.Errorf("expected no applied updates, got %#v", report.Updated)
		}
	})
}

func TestApplicationCommandSnapshotSaveLoad(t *testing.T) {
	fetched := time.Date(2026, 11, 11, 12, 0, 0, 0, time.UTC)
	snap := &ApplicationCommandSnapshot{
		ApplicationID: "app1",
		GuildID:       "55",
		FetchedAt:     fetched,
		Commands: []*ApplicationCommand{
			withSyncID(syncTestCommand("hello", "old desc"), "1001"),
			withSyncID(syncTestCommand("bye", "same"), "1002"),
		},
	}

	path := filepath.Join(t.TempDir(), "snapshot.json")
	if err := SaveApplicationCommandSnapshot(path, snap); err != nil {
		t.Fatalf("failed to save snapshot: %v", err)
	}

	loaded, err := LoadApplicationCommandSnapshot(path)
	if err != nil {
		t.Fatalf("failed to load snapshot: %v", err)
	}
	if loaded.ApplicationID != "app1" || loaded.GuildID != "55" {
		t.Errorf("unexpected snapshot metadata: %#v", loaded)
	}
	if !loaded.FetchedAt.Equal(fetched) {
		t.Errorf("expected FetchedAt %v, got %v", fetched, loaded.FetchedAt)
	}
	if len(loaded.Commands) != 2 {
		t.Fatalf("expected 2 commands, got %d", len(loaded.Commands))
	}
	if !ApplicationCommandsEqual(snap.Commands[0], loaded.Commands[0]) {
		t.Errorf("command content changed during the round trip: %#v vs %#v", snap.Commands[0], loaded.Commands[0])
	}

	t.Run("save nil snapshot", func(t *testing.T) {
		if err := SaveApplicationCommandSnapshot(path, nil); !errors.Is(err, ErrApplicationCommandSyncNilSnapshot) {
			t.Fatalf("expected ErrApplicationCommandSyncNilSnapshot, got %v", err)
		}
	})

	t.Run("load missing file", func(t *testing.T) {
		if _, err := LoadApplicationCommandSnapshot(filepath.Join(t.TempDir(), "missing.json")); err == nil {
			t.Fatal("expected error for missing file")
		}
	})

	t.Run("load corrupt file", func(t *testing.T) {
		bad := filepath.Join(t.TempDir(), "bad.json")
		if err := os.WriteFile(bad, []byte("{not json"), 0644); err != nil {
			t.Fatal(err)
		}
		if _, err := LoadApplicationCommandSnapshot(bad); err == nil {
			t.Fatal("expected error for corrupt snapshot")
		}
	})
}

func TestApplicationCommandSnapshotDiffCommands(t *testing.T) {
	snap := &ApplicationCommandSnapshot{
		ApplicationID: "app1",
		GuildID:       "55",
		Commands:      syncFixtureRegistered,
	}

	t.Run("offline drift detection", func(t *testing.T) {
		plan, err := snap.DiffCommands(syncFixtureDesired())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if plan.GuildID != "55" {
			t.Errorf("expected plan GuildID 55, got %q", plan.GuildID)
		}
		if len(plan.Creates) != 1 || plan.Creates[0].Name != "wow" {
			t.Errorf("expected create of wow, got %#v", plan.Creates)
		}
		if len(plan.Updates) != 1 || plan.Updates[0].ID != "1001" {
			t.Errorf("expected update of 1001, got %#v", plan.Updates)
		}
		if len(plan.Deletes) != 1 || plan.Deletes[0].ID != "1003" {
			t.Errorf("expected delete of 1003, got %#v", plan.Deletes)
		}
		if len(plan.Unchanged) != 1 || plan.Unchanged[0].ID != "1002" {
			t.Errorf("expected 1002 unchanged, got %#v", plan.Unchanged)
		}
	})

	t.Run("no drift produces empty plan", func(t *testing.T) {
		plan, err := snap.DiffCommands([]*ApplicationCommand{
			syncTestCommand("hello", "old desc"),
			syncTestCommand("bye", "same desc"),
			syncTestCommand("legacy", "legacy desc"),
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !plan.Empty() {
			t.Fatalf("expected empty plan, got %#v", plan)
		}
	})

	t.Run("nil snapshot receiver", func(t *testing.T) {
		var nilSnap *ApplicationCommandSnapshot
		if _, err := nilSnap.DiffCommands(nil); !errors.Is(err, ErrApplicationCommandSyncNilSnapshot) {
			t.Fatalf("expected ErrApplicationCommandSyncNilSnapshot, got %v", err)
		}
	})
}

func TestApplicationCommandSnapshotFromSession(t *testing.T) {
	var gotPath string
	s, cleanup := newFeatureTestSession(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/applications/app1/commands" {
			gotPath = r.URL.Path
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(syncFixtureGETResponse))
			return
		}
		http.NotFound(w, r)
	}))
	defer cleanup()

	snap, err := s.ApplicationCommandSnapshot("app1", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if snap.ApplicationID != "app1" || snap.GuildID != "" {
		t.Errorf("unexpected snapshot metadata: %#v", snap)
	}
	if len(snap.Commands) != 3 {
		t.Errorf("expected 3 commands, got %d", len(snap.Commands))
	}
	if gotPath == "" {
		t.Fatal("expected a GET request")
	}
	if snap.FetchedAt.IsZero() {
		t.Error("expected FetchedAt to be set")
	}

	// Snapshot diffs produce the same plan as a live sync comparison.
	plan, err := snap.DiffCommands(syncFixtureDesired())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(plan.Creates) != 1 || len(plan.Updates) != 1 || len(plan.Deletes) != 1 {
		t.Errorf("expected 1 create, 1 update, 1 delete; got %#v", plan)
	}

	t.Run("missing application ID", func(t *testing.T) {
		if _, err := s.ApplicationCommandSnapshot("", ""); !errors.Is(err, ErrApplicationCommandSyncMissingAppID) {
			t.Fatalf("expected ErrApplicationCommandSyncMissingAppID, got %v", err)
		}
	})
}
