// This file implements content-based synchronization of application
// (slash) commands.
//
// ApplicationCommandBulkOverwrite replaces the whole command set with a single
// PUT request, which counts against Discord's daily application-command
// limits and resets command permissions every time it is used. The sync logic
// implemented here instead compares the desired commands with the currently
// registered ones by normalized content (a SHA-256 hash over the comparison
// payload) and issues the minimum number of REST calls: one POST per new
// command, one PATCH per changed command and (optionally) one DELETE per
// removed command. Commands whose content is unchanged are not touched.
//
// For offline drift detection (e.g. in CI) the registered commands can be
// captured into a JSON snapshot (ApplicationCommandSnapshot), saved to disk
// and diffed against the desired set without any API traffic.
//
// https://discord.com/developers/docs/interactions/application-commands

package discordgo

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// Errors returned by the application command sync helpers.
var (
	// ErrApplicationCommandSyncMissingAppID is returned when an application
	// ID is required but empty.
	ErrApplicationCommandSyncMissingAppID = errors.New("application command sync: application ID must not be empty")
	// ErrApplicationCommandSyncNilCommand is returned when the desired
	// command set contains a nil entry.
	ErrApplicationCommandSyncNilCommand = errors.New("application command sync: nil command in desired set")
	// ErrApplicationCommandSyncDuplicateName is returned when the desired
	// command set contains two commands with the same type and name.
	ErrApplicationCommandSyncDuplicateName = errors.New("application command sync: duplicate command name in desired set")
	// ErrApplicationCommandSyncNilSnapshot is returned when a nil snapshot is
	// passed where one is required.
	ErrApplicationCommandSyncNilSnapshot = errors.New("application command sync: snapshot must not be nil")
)

// ApplicationCommandUpdate describes a single registered command whose
// normalized content differs from the desired command with the same type and
// name.
type ApplicationCommandUpdate struct {
	// ID is the command ID of the registered command.
	ID string
	// Registered is the command as currently registered with Discord.
	Registered *ApplicationCommand
	// Desired is the wanted command content.
	Desired *ApplicationCommand
}

// ApplicationCommandSyncPlan is the outcome of comparing a set of registered
// commands with the desired set. It is produced by CompareApplicationCommands
// and ApplicationCommandSnapshot.DiffCommands; applying it is the job of
// Session.ApplicationCommandsSync.
type ApplicationCommandSyncPlan struct {
	// GuildID the plan belongs to; empty for global commands.
	GuildID string

	// Creates are desired commands that are not registered yet.
	Creates []*ApplicationCommand
	// Updates are registered commands whose content differs from the desired
	// command with the same type and name.
	Updates []*ApplicationCommandUpdate
	// Deletes are registered commands missing from the desired set. They are
	// only executed when ApplicationCommandSyncOptions.DeleteRemoved is set.
	Deletes []*ApplicationCommand
	// Unchanged are registered commands whose content already matches the
	// desired set; they are never re-sent.
	Unchanged []*ApplicationCommand
}

// Empty reports whether the plan requires no changes at all.
func (p *ApplicationCommandSyncPlan) Empty() bool {
	return p == nil || (len(p.Creates) == 0 && len(p.Updates) == 0 && len(p.Deletes) == 0)
}

// ApplicationCommandsEqual reports whether two commands have identical
// content. Both commands are compared by their normalized content hash, so
// server-assigned fields (ID, application ID, version, guild ID) and
// default-valued optional fields (e.g. default_permission:true) do not affect
// the result.
func ApplicationCommandsEqual(a, b *ApplicationCommand) bool {
	equal, _ := appcmdContentsEqual(a, b)
	return equal
}

// ApplicationCommandContentHash returns a stable SHA-256 hash (hex encoded)
// of the command's normalized content. Two commands compare equal with
// ApplicationCommandsEqual if and only if they produce the same hash. The
// hash is usable for caching and drift detection because identical command
// content always hashes identically regardless of server-assigned fields.
func ApplicationCommandContentHash(cmd *ApplicationCommand) (string, error) {
	b, err := appcmdCompareBytes(cmd)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:]), nil
}

// appcmdCompareBytes marshals the normalized form of cmd for byte-wise
// comparison. A nil command marshals to "null".
func appcmdCompareBytes(cmd *ApplicationCommand) ([]byte, error) {
	if cmd == nil {
		return []byte("null"), nil
	}
	return Marshal(normalizeApplicationCommandForCompare(cmd))
}

// normalizeApplicationCommandForCompare returns a copy of cmd suitable for
// content comparison:
//
//   - server-assigned fields (ID, ApplicationID, Version, GuildID) are cleared,
//   - an unset command type (0) is normalized to ChatApplicationCommand,
//   - for slash commands, whose names Discord lowercases, the command, option
//     and choice names are lowercased,
//   - optional pointers equal to their default value are collapsed to nil
//     (default_permission:true, dm_permission:true, nsfw:false,
//     default_member_permissions:0),
//   - empty slices and maps are collapsed to nil (so a nil option list
//     compares equal to an empty one).
func normalizeApplicationCommandForCompare(cmd *ApplicationCommand) *ApplicationCommand {
	n := new(ApplicationCommand)
	*n = *cmd

	n.ID = ""
	n.ApplicationID = ""
	n.Version = ""
	n.GuildID = ""

	if n.Type == 0 {
		n.Type = ChatApplicationCommand
	}

	lowerNames := n.Type == ChatApplicationCommand
	if lowerNames {
		n.Name = strings.ToLower(n.Name)
	}

	n.DefaultPermission = appcmdCollapseBoolPtr(n.DefaultPermission, true)
	n.DMPermission = appcmdCollapseBoolPtr(n.DMPermission, true)
	n.NSFW = appcmdCollapseBoolPtr(n.NSFW, false)
	if n.DefaultMemberPermissions != nil && *n.DefaultMemberPermissions == 0 {
		n.DefaultMemberPermissions = nil
	}

	n.NameLocalizations = appcmdCollapseLocaleMapPtr(n.NameLocalizations)
	n.DescriptionLocalizations = appcmdCollapseLocaleMapPtr(n.DescriptionLocalizations)
	if n.Contexts != nil && len(*n.Contexts) == 0 {
		n.Contexts = nil
	}
	if n.IntegrationTypes != nil && len(*n.IntegrationTypes) == 0 {
		n.IntegrationTypes = nil
	}
	n.Options = appcmdNormalizeCommandOptions(n.Options, lowerNames)

	return n
}

// appcmdNormalizeCommandOptions normalizes an option tree recursively. When
// lowerNames is set (slash commands), option and choice names are lowercased.
func appcmdNormalizeCommandOptions(opts []*ApplicationCommandOption, lowerNames bool) []*ApplicationCommandOption {
	if len(opts) == 0 {
		return nil
	}
	out := make([]*ApplicationCommandOption, len(opts))
	for i, o := range opts {
		if o == nil {
			continue
		}
		c := new(ApplicationCommandOption)
		*c = *o
		if lowerNames {
			c.Name = strings.ToLower(c.Name)
		}
		c.NameLocalizations = appcmdCollapseLocaleMap(c.NameLocalizations)
		c.DescriptionLocalizations = appcmdCollapseLocaleMap(c.DescriptionLocalizations)
		if len(c.ChannelTypes) == 0 {
			c.ChannelTypes = nil
		}
		c.Options = appcmdNormalizeCommandOptions(c.Options, lowerNames)
		if len(c.Choices) == 0 {
			c.Choices = nil
		} else {
			choices := make([]*ApplicationCommandOptionChoice, len(c.Choices))
			for j, ch := range c.Choices {
				if ch == nil {
					continue
				}
				cc := new(ApplicationCommandOptionChoice)
				*cc = *ch
				if lowerNames {
					cc.Name = strings.ToLower(cc.Name)
				}
				cc.NameLocalizations = appcmdCollapseLocaleMap(cc.NameLocalizations)
				choices[j] = cc
			}
			c.Choices = choices
		}
		out[i] = c
	}
	return out
}

// appcmdCollapseBoolPtr collapses a *bool whose value equals def into nil.
func appcmdCollapseBoolPtr(p *bool, def bool) *bool {
	if p != nil && *p == def {
		return nil
	}
	return p
}

// appcmdCollapseLocaleMapPtr collapses a pointer to an empty map into nil.
func appcmdCollapseLocaleMapPtr(p *map[Locale]string) *map[Locale]string {
	if p != nil && len(*p) == 0 {
		return nil
	}
	return p
}

// appcmdCollapseLocaleMap collapses an empty map into nil.
func appcmdCollapseLocaleMap(m map[Locale]string) map[Locale]string {
	if len(m) == 0 {
		return nil
	}
	return m
}

// appcmdCommandKey returns the identity of a command within its scope: its
// type plus name. Slash command names are case-insensitive on Discord (the
// API lowercases them), context menu command names are case-sensitive.
func appcmdCommandKey(cmd *ApplicationCommand) string {
	t := cmd.Type
	if t == 0 {
		t = ChatApplicationCommand
	}
	name := cmd.Name
	if t == ChatApplicationCommand {
		name = strings.ToLower(name)
	}
	return strconv.Itoa(int(t)) + "\x00" + name
}

// CompareApplicationCommands compares the registered commands with the
// desired set and returns the minimal change plan. Commands are matched by
// type and name; the content of each match is compared by normalized hash.
//
// Renaming a command is therefore reported as a create (new name) plus a
// delete (old name), matching how Discord identifies commands by ID.
//
// A nil entry or a duplicate type+name pair in desired results in an error.
func CompareApplicationCommands(registered, desired []*ApplicationCommand) (*ApplicationCommandSyncPlan, error) {
	desiredByKey := make(map[string]*ApplicationCommand, len(desired))
	for _, d := range desired {
		if d == nil {
			return nil, ErrApplicationCommandSyncNilCommand
		}
		k := appcmdCommandKey(d)
		if _, dup := desiredByKey[k]; dup {
			return nil, fmt.Errorf("%w: %q", ErrApplicationCommandSyncDuplicateName, d.Name)
		}
		desiredByKey[k] = d
	}

	registeredByKey := make(map[string]*ApplicationCommand, len(registered))
	for _, r := range registered {
		if r == nil {
			continue
		}
		registeredByKey[appcmdCommandKey(r)] = r
	}

	plan := &ApplicationCommandSyncPlan{}

	for _, d := range desired {
		r := registeredByKey[appcmdCommandKey(d)]
		if r == nil {
			plan.Creates = append(plan.Creates, d)
			continue
		}
		equal, err := appcmdContentsEqual(r, d)
		if err != nil {
			return nil, fmt.Errorf("application command sync: failed to compare %q: %w", d.Name, err)
		}
		if equal {
			plan.Unchanged = append(plan.Unchanged, r)
			continue
		}
		plan.Updates = append(plan.Updates, &ApplicationCommandUpdate{
			ID:         r.ID,
			Registered: r,
			Desired:    d,
		})
	}

	for _, r := range registered {
		if r == nil {
			continue
		}
		if desiredByKey[appcmdCommandKey(r)] == nil {
			plan.Deletes = append(plan.Deletes, r)
		}
	}

	return plan, nil
}

// appcmdContentsEqual is ApplicationCommandsEqual with error propagation for
// commands whose content cannot be marshaled (e.g. a choice value containing
// an unsupported type).
func appcmdContentsEqual(a, b *ApplicationCommand) (bool, error) {
	hb, err := appcmdCompareBytes(b)
	if err != nil {
		return false, err
	}
	ha, err := appcmdCompareBytes(a)
	if err != nil {
		return false, err
	}
	return bytes.Equal(ha, hb), nil
}

// ApplicationCommandSyncOptions configures Session.ApplicationCommandsSync.
type ApplicationCommandSyncOptions struct {
	// GuildID targets guild-specific commands when set; global commands when
	// empty.
	GuildID string

	// DryRun computes and reports the plan without issuing any modifying
	// requests (a GET of the registered commands is still performed).
	DryRun bool

	// DeleteRemoved deletes registered commands that are missing from the
	// desired set. Without it, such commands are only reported in the plan.
	DeleteRemoved bool
}

// ApplicationCommandSyncReport describes what ApplicationCommandsSync
// computed and applied.
type ApplicationCommandSyncReport struct {
	// GuildID the sync targeted; empty for global commands.
	GuildID string

	// DryRun reports whether the sync stopped after planning.
	DryRun bool

	// Plan is the full comparison result.
	Plan *ApplicationCommandSyncPlan

	// Created are the commands as returned by the API after creation
	// (empty when DryRun).
	Created []*ApplicationCommand
	// Updated are the commands as returned by the API after editing
	// (empty when DryRun).
	Updated []*ApplicationCommand
	// Deleted are the IDs of commands removed by DeleteRemoved
	// (empty when DryRun).
	Deleted []string

	// Unchanged are the registered commands that needed no changes.
	Unchanged []*ApplicationCommand
}

// ApplicationCommandsSync compares the desired commands with the commands
// currently registered for the application and applies only the differences:
// it creates missing commands, edits changed ones and, when
// opts.DeleteRemoved is set, deletes registered commands missing from the
// desired set. Unchanged commands are never re-sent, which keeps the
// operation well inside the daily application-command limits compared to
// ApplicationCommandBulkOverwrite.
//
// appID  : The application ID.
// desired: The wanted command set (must not contain nil or duplicate
// type+name entries).
// opts   : May be nil for a global sync without deletions.
//
// If a request fails, the error is returned together with the report so far.
func (s *Session) ApplicationCommandsSync(appID string, desired []*ApplicationCommand, opts *ApplicationCommandSyncOptions) (report *ApplicationCommandSyncReport, err error) {
	if appID == "" {
		return nil, ErrApplicationCommandSyncMissingAppID
	}

	guildID := ""
	if opts != nil {
		guildID = opts.GuildID
	}

	registered, err := s.ApplicationCommands(appID, guildID)
	if err != nil {
		return nil, err
	}

	plan, err := CompareApplicationCommands(registered, desired)
	if err != nil {
		return nil, err
	}
	plan.GuildID = guildID

	report = &ApplicationCommandSyncReport{
		GuildID:   guildID,
		Plan:      plan,
		Unchanged: plan.Unchanged,
	}
	if plan.Empty() || (opts != nil && opts.DryRun) {
		if opts != nil && opts.DryRun {
			report.DryRun = true
		}
		return report, nil
	}

	for _, c := range plan.Creates {
		created, err := s.ApplicationCommandCreate(appID, guildID, c)
		if err != nil {
			return report, fmt.Errorf("application command sync: failed to create %q: %w", c.Name, err)
		}
		report.Created = append(report.Created, created)
	}

	for _, u := range plan.Updates {
		updated, err := s.ApplicationCommandEdit(appID, guildID, u.ID, appcmdEditPayload(u.Desired))
		if err != nil {
			return report, fmt.Errorf("application command sync: failed to update %q: %w", u.Desired.Name, err)
		}
		report.Updated = append(report.Updated, updated)
	}

	if opts != nil && opts.DeleteRemoved {
		for _, d := range plan.Deletes {
			if err = s.ApplicationCommandDelete(appID, guildID, d.ID); err != nil {
				return report, fmt.Errorf("application command sync: failed to delete %q: %w", d.Name, err)
			}
			report.Deleted = append(report.Deleted, d.ID)
		}
	}

	return report, nil
}

// appcmdEditPayload returns a copy of the desired command with server-assigned
// fields cleared, ready to be sent as an edit request body.
func appcmdEditPayload(cmd *ApplicationCommand) *ApplicationCommand {
	c := new(ApplicationCommand)
	*c = *cmd
	c.ID = ""
	c.ApplicationID = ""
	c.Version = ""
	c.GuildID = ""
	return c
}

// ApplicationCommandSnapshot is a JSON-serializable record of the commands
// registered for an application at a point in time. It is the local cache
// used for offline drift detection: save it after a sync and diff future
// desired sets against it without any API traffic.
type ApplicationCommandSnapshot struct {
	// ApplicationID the commands belong to.
	ApplicationID string `json:"application_id"`
	// GuildID the commands are registered in; empty for global commands.
	GuildID string `json:"guild_id,omitempty"`
	// FetchedAt is the UTC time the snapshot was captured.
	FetchedAt time.Time `json:"fetched_at"`
	// Commands are the registered commands.
	Commands []*ApplicationCommand `json:"commands"`
}

// ApplicationCommandSnapshot fetches the commands currently registered for
// the application and returns them as a snapshot.
//
// guildID: Guild ID for guild-specific commands; empty for global commands.
func (s *Session) ApplicationCommandSnapshot(appID, guildID string, options ...RequestOption) (*ApplicationCommandSnapshot, error) {
	if appID == "" {
		return nil, ErrApplicationCommandSyncMissingAppID
	}
	commands, err := s.ApplicationCommands(appID, guildID, options...)
	if err != nil {
		return nil, err
	}
	return &ApplicationCommandSnapshot{
		ApplicationID: appID,
		GuildID:       guildID,
		FetchedAt:     time.Now().UTC(),
		Commands:      commands,
	}, nil
}

// SaveApplicationCommandSnapshot writes the snapshot as JSON to path. Parent
// directories are not created; the file permissions are 0644.
func SaveApplicationCommandSnapshot(path string, snap *ApplicationCommandSnapshot) error {
	if snap == nil {
		return ErrApplicationCommandSyncNilSnapshot
	}
	data, err := Marshal(snap)
	if err != nil {
		return fmt.Errorf("application command sync: failed to encode snapshot: %w", err)
	}
	return os.WriteFile(path, data, 0644)
}

// LoadApplicationCommandSnapshot reads a snapshot previously saved with
// SaveApplicationCommandSnapshot.
func LoadApplicationCommandSnapshot(path string) (*ApplicationCommandSnapshot, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var snap ApplicationCommandSnapshot
	if err = unmarshal(data, &snap); err != nil {
		return nil, err
	}
	return &snap, nil
}

// DiffCommands compares the desired command set against the snapshot's
// registered commands without any API calls, returning the same plan shape as
// CompareApplicationCommands. Deleted commands can be re-created locally, but
// their IDs refer to registrations that may no longer exist, so a plan
// derived from a stale snapshot should be verified before being applied.
func (snap *ApplicationCommandSnapshot) DiffCommands(desired []*ApplicationCommand) (*ApplicationCommandSyncPlan, error) {
	if snap == nil {
		return nil, ErrApplicationCommandSyncNilSnapshot
	}
	plan, err := CompareApplicationCommands(snap.Commands, desired)
	if err != nil {
		return nil, err
	}
	plan.GuildID = snap.GuildID
	return plan, nil
}
