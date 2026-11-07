// This file implements the Guild Member Verification REST API, which lets
// bots read and update the member verification form of a community guild.
// Requires the MANAGE_GUILD permission.
//
// https://discord.com/developers/docs/resources/guild#get-guild-member-verification

package discordgo

// MemberVerificationField stores a single field of the member verification
// form of a guild.
type MemberVerificationField struct {
	// FieldType is the kind of field. Discord currently only documents
	// the "TERMS" field type.
	FieldType string `json:"field_type"`
	// Label is the text shown above the field.
	Label string `json:"label,omitempty"`
	// Values holds the rule text shown to the member.
	Values []string `json:"values,omitempty"`
	// Required is whether the member must accept the field.
	Required bool `json:"required,omitempty"`
	// ValuesType is the automation type of the field values, for
	// example "RULES".
	ValuesType string `json:"values_type,omitempty"`
}

// MemberVerification stores the member verification configuration of a
// guild.
type MemberVerification struct {
	// Version is a timestamp of the last change to the form, encoded as
	// an ISO 8601 string.
	Version string `json:"version"`
	// FormFields holds the fields of the verification form.
	FormFields []*MemberVerificationField `json:"form_fields"`
	// Description is shown below the form.
	Description string `json:"description"`
}

// MemberVerificationParams stores the member verification fields to update.
// A nil field or pointer leaves the corresponding value unchanged; an empty
// (non-nil) FormFields slice or Description string clears the current value.
type MemberVerificationParams struct {
	// FormFields replaces the verification form when non-nil.
	FormFields []*MemberVerificationField
	// Description replaces the form description when non-nil.
	Description *string
}

// MarshalJSON encodes the params, omitting nil fields so untouched values
// stay unchanged.
func (p MemberVerificationParams) MarshalJSON() ([]byte, error) {
	v := struct {
		FormFields  []*MemberVerificationField `json:"form_fields,omitempty"`
		Description string                     `json:"description,omitempty"`
	}{}

	if p.FormFields != nil {
		v.FormFields = p.FormFields
	}
	if p.Description != nil {
		v.Description = *p.Description
	}

	return Marshal(v)
}

// GuildMemberVerification returns the member verification configuration of
// a guild. Requires the MANAGE_GUILD permission.
//
// guildID : The ID of the guild.
func (s *Session) GuildMemberVerification(guildID string, options ...RequestOption) (verification *MemberVerification, err error) {
	endpoint := EndpointGuildMemberVerification(guildID)

	var body []byte
	body, err = s.RequestWithBucketID("GET", endpoint, nil, BucketIDFromURL(endpoint), options...)
	if err != nil {
		return
	}

	err = unmarshal(body, &verification)
	return
}

// GuildMemberVerificationEdit updates the member verification configuration
// of a guild. Requires the MANAGE_GUILD permission.
//
// guildID : The ID of the guild.
// params  : The verification form fields to update.
func (s *Session) GuildMemberVerificationEdit(guildID string, params *MemberVerificationParams, options ...RequestOption) (verification *MemberVerification, err error) {
	endpoint := EndpointGuildMemberVerification(guildID)

	var body []byte
	body, err = s.RequestWithBucketID("PATCH", endpoint, params, BucketIDFromURL(endpoint), options...)
	if err != nil {
		return
	}

	err = unmarshal(body, &verification)
	return
}
