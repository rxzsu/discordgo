package discordgo

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"
)

// newFeatureTestSession builds a Session whose REST requests are redirected
// to the given handler. It returns the session and a cleanup function.
func newFeatureTestSession(t *testing.T, h http.Handler) (*Session, func()) {
	t.Helper()

	srv := httptest.NewServer(h)
	s, err := New("Bot test-token")
	if err != nil {
		srv.Close()
		t.Fatal(err)
	}

	host := srv.Listener.Addr().String()
	s.Client = &http.Client{Transport: featureTestTransport(func(r *http.Request) (*http.Response, error) {
		r.URL.Scheme = "http"
		r.URL.Host = host
		// Discord paths carry the /api/vN prefix; the test mux has none.
		r.URL.Path = regexp.MustCompile("^/api/v[0-9]+").ReplaceAllString(r.URL.Path, "")
		return http.DefaultTransport.RoundTrip(r)
	})}

	return s, srv.Close
}

// featureTestTransport redirects Discord REST requests to a test server.
type featureTestTransport func(*http.Request) (*http.Response, error)

func (f featureTestTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// decodeFeatureTestBody decodes a JSON request body into a map.
func decodeFeatureTestBody(t *testing.T, r *http.Request) map[string]interface{} {
	t.Helper()
	b, err := io.ReadAll(r.Body)
	if err != nil {
		t.Fatalf("failed to read request body: %v", err)
	}
	var m map[string]interface{}
	if len(b) > 0 {
		if err := json.Unmarshal(b, &m); err != nil {
			t.Fatalf("failed to decode request body %q: %v", string(b), err)
		}
	}
	return m
}

func TestGuildIncidentActionsEdit(t *testing.T) {
	var (
		gotMethod string
		gotBody   map[string]interface{}
	)
	s, cleanup := newFeatureTestSession(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		if r.URL.Path != "/guilds/g1/incident-actions" {
			http.NotFound(w, r)
			return
		}
		gotBody = decodeFeatureTestBody(t, r)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"invites_disabled_until": "2026-11-08T00:00:00.000000+00:00",
			"dms_disabled_until": null,
			"raid_detected_at": "2026-11-07T10:00:00.000000+00:00"
		}`))
	}))
	defer cleanup()

	invitesUntil := time.Date(2026, 11, 8, 0, 0, 0, 0, time.UTC)
	incidents, err := s.GuildIncidentActionsEdit("g1", &GuildIncidentActionsParams{
		InvitesDisabledUntil: &invitesUntil,
		DMsDisabledUntil:     &time.Time{},
	})
	if err != nil {
		t.Fatal(err)
	}

	if gotMethod != http.MethodPut {
		t.Fatalf("expected PUT request, got %s", gotMethod)
	}
	if raw := gotBody["invites_disabled_until"]; raw == nil || raw == "" {
		t.Fatalf("expected invites_disabled_until timestamp, got %v", raw)
	}
	if raw, ok := gotBody["dms_disabled_until"]; !ok || raw != nil {
		t.Fatalf("expected explicit null dms_disabled_until, got %v (present: %v)", raw, ok)
	}

	if incidents == nil {
		t.Fatal("expected incidents data, got nil")
	}
	if incidents.InvitesDisabledUntil == nil || incidents.DMsDisabledUntil != nil {
		t.Fatalf("unexpected incidents data: %+v", incidents)
	}
	if incidents.RaidDetectedAt == nil {
		t.Fatal("expected raid_detected_at to be parsed")
	}
}

func TestGuildIncidentActionsEditNilParams(t *testing.T) {
	s, cleanup := newFeatureTestSession(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("no request should be made for nil params")
	}))
	defer cleanup()

	if _, err := s.GuildIncidentActionsEdit("g1", nil); !errors.Is(err, ErrNilIncidentActionsParams) {
		t.Fatalf("expected ErrNilIncidentActionsParams, got %v", err)
	}
}

func TestGuildIncidentActionsParamsMarshalJSON(t *testing.T) {
	future := time.Date(2026, 12, 1, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name   string
		params GuildIncidentActionsParams
		want   map[string]interface{}
	}{
		{
			name:   "empty params omit both fields",
			params: GuildIncidentActionsParams{},
			want:   map[string]interface{}{},
		},
		{
			name: "nil timestamps are omitted",
			params: GuildIncidentActionsParams{
				InvitesDisabledUntil: nil,
				DMsDisabledUntil:     nil,
			},
			want: map[string]interface{}{},
		},
		{
			name: "zero times are explicit nulls",
			params: GuildIncidentActionsParams{
				InvitesDisabledUntil: &time.Time{},
				DMsDisabledUntil:     &time.Time{},
			},
			want: map[string]interface{}{
				"invites_disabled_until": nil,
				"dms_disabled_until":     nil,
			},
		},
		{
			name: "timestamps are encoded",
			params: GuildIncidentActionsParams{
				InvitesDisabledUntil: &future,
				DMsDisabledUntil:     &time.Time{},
			},
			want: map[string]interface{}{
				"invites_disabled_until": "2026-12-01T12:00:00Z",
				"dms_disabled_until":     nil,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			raw, err := json.Marshal(tt.params)
			if err != nil {
				t.Fatal(err)
			}
			var got map[string]interface{}
			if err := json.Unmarshal(raw, &got); err != nil {
				t.Fatal(err)
			}
			if len(got) != len(tt.want) {
				t.Fatalf("got %s (%d keys), want %s (%d keys)", string(raw), len(got), mustJSON(t, tt.want), len(tt.want))
			}
			for k, want := range tt.want {
				gotVal, ok := got[k]
				if !ok {
					t.Fatalf("field %q missing in %s", k, string(raw))
				}
				if want == nil {
					if gotVal != nil {
						t.Fatalf("field %q = %v, want null (%s)", k, gotVal, string(raw))
					}
					continue
				}
				if gotVal != want.(string) {
					t.Fatalf("field %q = %v, want %v (%s)", k, gotVal, want, string(raw))
				}
			}
		})
	}
}

func mustJSON(t *testing.T, v interface{}) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestGuildWithIncidentsData(t *testing.T) {
	var gotQuery string
	s, cleanup := newFeatureTestSession(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/guilds/g1" {
			http.NotFound(w, r)
			return
		}
		gotQuery = r.URL.RawQuery
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"id": "g1",
			"name": "test guild",
			"incidents_data": {
				"invites_disabled_until": "2026-11-08T00:00:00.000000+00:00",
				"dms_disabled_until": null
			}
		}`))
	}))
	defer cleanup()

	guild, err := s.GuildWithIncidentsData("g1")
	if err != nil {
		t.Fatal(err)
	}

	if gotQuery != "with_incidents_data=true" {
		t.Fatalf("expected with_incidents_data=true query, got %q", gotQuery)
	}
	if guild.Name != "test guild" || guild.ID != "g1" {
		t.Fatalf("unexpected guild payload: %+v", guild.Guild)
	}
	if guild.IncidentsData == nil {
		t.Fatal("expected incidents_data to be parsed")
	}
	if guild.IncidentsData.InvitesDisabledUntil == nil || guild.IncidentsData.DMsDisabledUntil != nil {
		t.Fatalf("unexpected incidents data: %+v", guild.IncidentsData)
	}
}

func TestGuildAuditLogsComplex(t *testing.T) {
	var gotQuery string
	s, cleanup := newFeatureTestSession(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/guilds/g1/audit-logs" {
			http.NotFound(w, r)
			return
		}
		gotQuery = r.URL.RawQuery
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"audit_log_entries": [
				{"id": "1000", "action_type": 1, "user_id": "u1"}
			],
			"users": [{"id": "u1", "username": "actor"}]
		}`))
	}))
	defer cleanup()

	log, err := s.GuildAuditLogsComplex("g1", &GuildAuditLogFilter{
		UserID:        "u1",
		AfterID:       "500",
		ApplicationID: "app1",
		ActionType:    1,
		Limit:         25,
	})
	if err != nil {
		t.Fatal(err)
	}

	if gotQuery == "" {
		t.Fatal("expected query parameters on audit log request")
	}
	for _, want := range []string{"user_id=u1", "after=500", "application_id=app1", "action_type=1", "limit=25"} {
		if !strings.Contains(gotQuery, want) {
			t.Fatalf("query %q missing %q", gotQuery, want)
		}
	}
	if len(log.AuditLogEntries) != 1 || log.AuditLogEntries[0].ActionType == nil || *log.AuditLogEntries[0].ActionType != 1 {
		t.Fatalf("unexpected audit log entries: %+v", log.AuditLogEntries)
	}
	if len(log.Users) != 1 || log.Users[0].Username != "actor" {
		t.Fatalf("unexpected audit log users: %+v", log.Users)
	}
}

func TestGuildAuditLogsComplexNilFilter(t *testing.T) {
	var gotQuery string
	s, cleanup := newFeatureTestSession(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		_, _ = w.Write([]byte(`{"audit_log_entries": [], "users": []}`))
	}))
	defer cleanup()

	if _, err := s.GuildAuditLogsComplex("g1", nil); err != nil {
		t.Fatal(err)
	}
	if gotQuery != "" {
		t.Fatalf("expected no query for nil filter, got %q", gotQuery)
	}
}

func TestGuildAuditLogFilterQuery(t *testing.T) {
	tests := []struct {
		name   string
		filter *GuildAuditLogFilter
		want   string
	}{
		{
			name:   "nil filter",
			filter: nil,
			want:   "",
		},
		{
			name:   "empty filter",
			filter: &GuildAuditLogFilter{},
			want:   "",
		},
		{
			name:   "limit clamped to 100",
			filter: &GuildAuditLogFilter{Limit: 500},
			want:   "limit=100",
		},
		{
			name:   "before filter",
			filter: &GuildAuditLogFilter{BeforeID: "42"},
			want:   "before=42",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.filter.query(); got != tt.want {
				t.Fatalf("query() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestGuildMemberVerification(t *testing.T) {
	var gotMethod string
	s, cleanup := newFeatureTestSession(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/guilds/g1/member-verification" {
			http.NotFound(w, r)
			return
		}
		gotMethod = r.Method
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"version": "2026-11-07T00:00:00.000000+00:00",
			"form_fields": [
				{
					"field_type": "TERMS",
					"label": "Rules",
					"values": ["Be nice."],
					"required": true,
					"values_type": "RULES"
				}
			],
			"description": "Read the rules before joining."
		}`))
	}))
	defer cleanup()

	verification, err := s.GuildMemberVerification("g1")
	if err != nil {
		t.Fatal(err)
	}
	if gotMethod != http.MethodGet {
		t.Fatalf("expected GET request, got %s", gotMethod)
	}
	if verification.Version == "" || verification.Description != "Read the rules before joining." {
		t.Fatalf("unexpected verification payload: %+v", verification)
	}
	if len(verification.FormFields) != 1 {
		t.Fatalf("expected 1 form field, got %d", len(verification.FormFields))
	}
	field := verification.FormFields[0]
	if field.FieldType != "TERMS" || !field.Required || field.ValuesType != "RULES" || len(field.Values) != 1 {
		t.Fatalf("unexpected form field: %+v", field)
	}
}

func TestGuildMemberVerificationEdit(t *testing.T) {
	var (
		gotMethod string
		gotBody   map[string]interface{}
	)
	s, cleanup := newFeatureTestSession(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/guilds/g1/member-verification" {
			http.NotFound(w, r)
			return
		}
		gotMethod = r.Method
		gotBody = decodeFeatureTestBody(t, r)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"version": "2026-11-07T01:00:00.000000+00:00",
			"form_fields": [
				{"field_type": "TERMS", "label": "Updated", "required": true}
			],
			"description": "Updated description."
		}`))
	}))
	defer cleanup()

	description := "Updated description."
	verification, err := s.GuildMemberVerificationEdit("g1", &MemberVerificationParams{
		FormFields: []*MemberVerificationField{
			{FieldType: "TERMS", Label: "Updated", Required: true},
		},
		Description: &description,
	})
	if err != nil {
		t.Fatal(err)
	}

	if gotMethod != http.MethodPatch {
		t.Fatalf("expected PATCH request, got %s", gotMethod)
	}
	fields, ok := gotBody["form_fields"].([]interface{})
	if !ok || len(fields) != 1 {
		t.Fatalf("expected one form field in request body, got %v", gotBody)
	}
	if gotBody["description"] != "Updated description." {
		t.Fatalf("unexpected description in request body: %v", gotBody["description"])
	}
	if verification.FormFields[0].Label != "Updated" {
		t.Fatalf("unexpected response payload: %+v", verification)
	}
}

func TestGuildMemberVerificationEditNilFieldsOmitted(t *testing.T) {
	var gotBody map[string]interface{}
	s, cleanup := newFeatureTestSession(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotBody = decodeFeatureTestBody(t, r)
		_, _ = w.Write([]byte(`{"version": "v", "form_fields": []}`))
	}))
	defer cleanup()

	if _, err := s.GuildMemberVerificationEdit("g1", &MemberVerificationParams{}); err != nil {
		t.Fatal(err)
	}
	if _, ok := gotBody["form_fields"]; ok {
		t.Fatalf("expected form_fields to be omitted, got %s", mustJSON(t, gotBody))
	}
	if _, ok := gotBody["description"]; ok {
		t.Fatalf("expected description to be omitted, got %s", mustJSON(t, gotBody))
	}
}
