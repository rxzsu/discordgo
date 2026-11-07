// This file extends the Guild Audit Log REST API with the full set of
// supported query filters. The legacy GuildAuditLog method only exposes
// user_id, before, action_type and limit; this file adds the after and
// application_id filters without changing the existing method.
//
// https://discord.com/developers/docs/resources/audit-log#list-audit-log-entries

package discordgo

import (
	"net/url"
	"strconv"
)

// GuildAuditLogFilter stores the optional filters for
// GuildAuditLogsComplex. All fields are optional; leave them at their zero
// values to skip the corresponding filter.
type GuildAuditLogFilter struct {
	// UserID filters entries by the user (or webhook/application actor)
	// that performed the action.
	UserID string
	// BeforeID returns entries before this entry ID.
	BeforeID string
	// AfterID returns entries after this entry ID.
	AfterID string
	// ApplicationID filters entries for actions performed by the given
	// application. For example, entries where an application added a
	// member to a role.
	ApplicationID string
	// ActionType filters entries by audit log action type. Zero skips
	// the filter.
	ActionType int
	// Limit limits the number of returned entries (1-100). Values below
	// one use the API default of 50; values above 100 are clamped to 100.
	Limit int
}

// GuildAuditLogsComplex returns the audit log entries of a guild with the
// full set of supported filters. Requires the VIEW_AUDIT_LOG permission.
//
// guildID : The ID of the guild.
// filter  : The filters to apply, or nil for an unfiltered request.
func (s *Session) GuildAuditLogsComplex(guildID string, filter *GuildAuditLogFilter, options ...RequestOption) (st *GuildAuditLog, err error) {
	endpoint := EndpointGuildAuditLogs(guildID)
	query := filter.query()

	if query != "" {
		endpoint = endpoint + "?" + query
	}

	var body []byte
	body, err = s.RequestWithBucketID("GET", endpoint, nil, BucketIDFromURL(endpoint), options...)
	if err != nil {
		return
	}

	err = unmarshal(body, &st)
	return
}

// query encodes the filter as URL query values. It returns an empty string
// when no filter is set.
func (f *GuildAuditLogFilter) query() string {
	if f == nil {
		return ""
	}

	v := url.Values{}
	if f.UserID != "" {
		v.Set("user_id", f.UserID)
	}
	if f.BeforeID != "" {
		v.Set("before", f.BeforeID)
	}
	if f.AfterID != "" {
		v.Set("after", f.AfterID)
	}
	if f.ApplicationID != "" {
		v.Set("application_id", f.ApplicationID)
	}
	if f.ActionType != 0 {
		v.Set("action_type", strconv.Itoa(f.ActionType))
	}
	if f.Limit > 0 {
		limit := f.Limit
		if limit > 100 {
			limit = 100
		}
		v.Set("limit", strconv.Itoa(limit))
	}

	return v.Encode()
}
