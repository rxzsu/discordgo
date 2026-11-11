// This example synchronizes the application (slash) commands declared in
// this file with Discord using ApplicationCommandsSync. Unlike
// ApplicationCommandBulkOverwrite (a single PUT that counts against the
// daily application-command limits and resets command permissions), the sync
// compares the desired commands with the registered ones by normalized
// content and issues only the minimum number of REST calls: one POST per new
// command, one PATCH per changed command and, when asked, one DELETE per
// removed command. Unchanged commands are never re-sent.
//
// Usage:
//
//	DISCORD_TOKEN=... DISCORD_APP_ID=... go run .            # apply
//	DISCORD_TOKEN=... DISCORD_APP_ID=... DRY_RUN=1 go run .  # plan only
//	go run . -token ... -app ... -guild <id>                 # guild-scoped
//	go run . -delete-removed                                 # also delete removed commands
//
// The gateway is never opened: the sync is pure REST.
package main

import (
	"flag"
	"log"
	"os"

	"github.com/rxzsu/discordgo"
)

// desiredCommands is the wanted command set, declared right in the code.
// Renaming or editing an entry here makes the next sync create, update or
// (with -delete-removed) delete exactly what is needed.
var desiredCommands = []*discordgo.ApplicationCommand{
	{
		Name:        "hello",
		Description: "Say hello",
	},
	{
		Name:        "echo",
		Description: "Repeat what you say",
		Options: []*discordgo.ApplicationCommandOption{
			{
				Type:        discordgo.ApplicationCommandOptionString,
				Name:        "text",
				Description: "Text to repeat",
				Required:    true,
			},
		},
	},
	{
		Name:        "coinflip",
		Description: "Flip a coin",
	},
}

func main() {
	var (
		token         = flag.String("token", os.Getenv("DISCORD_TOKEN"), "Bot access token (defaults to DISCORD_TOKEN)")
		appID         = flag.String("app", os.Getenv("DISCORD_APP_ID"), "Application ID (defaults to DISCORD_APP_ID)")
		guildID       = flag.String("guild", os.Getenv("DISCORD_GUILD_ID"), "Guild ID for guild-scoped commands (defaults to DISCORD_GUILD_ID; empty = global)")
		dryRun        = flag.Bool("dry-run", os.Getenv("DRY_RUN") == "1", "Print the plan without modifying anything (defaults to DRY_RUN=1)")
		deleteRemoved = flag.Bool("delete-removed", false, "Delete registered commands missing from the desired set")
	)
	flag.Parse()

	if *token == "" {
		log.Fatal("No bot token: set DISCORD_TOKEN or pass -token")
	}
	if *appID == "" {
		log.Fatal("No application ID: set DISCORD_APP_ID or pass -app")
	}

	s, err := discordgo.New("Bot " + *token)
	if err != nil {
		log.Fatalf("Invalid bot parameters: %v", err)
	}

	report, err := s.ApplicationCommandsSync(*appID, desiredCommands, &discordgo.ApplicationCommandSyncOptions{
		GuildID:       *guildID,
		DryRun:        *dryRun,
		DeleteRemoved: *deleteRemoved,
	})
	if err != nil {
		log.Fatalf("Command sync failed: %v", err)
	}

	printReport(report)
}

func printReport(report *discordgo.ApplicationCommandSyncReport) {
	scope := "global commands"
	if report.GuildID != "" {
		scope = "guild " + report.GuildID
	}
	if report.DryRun {
		log.Printf("Command sync (dry run) for %s:", scope)
	} else {
		log.Printf("Command sync for %s:", scope)
	}

	plan := report.Plan
	log.Printf("Plan: %d create(s), %d update(s), %d delete(s), %d unchanged",
		len(plan.Creates), len(plan.Updates), len(plan.Deletes), len(plan.Unchanged))

	for _, c := range plan.Creates {
		log.Printf("  + create  %q", c.Name)
	}
	for _, u := range plan.Updates {
		log.Printf("  ~ update  %q (registered command %s)", u.Desired.Name, u.ID)
	}
	for _, d := range plan.Deletes {
		log.Printf("  - delete  %q (registered command %s)", d.Name, d.ID)
	}
	for _, c := range plan.Unchanged {
		log.Printf("  = ok      %q", c.Name)
	}

	if len(plan.Deletes) > 0 && !report.DryRun && len(report.Deleted) == 0 {
		log.Println("Deletes were reported but not applied: pass -delete-removed to remove them.")
	}

	if report.DryRun {
		log.Println("Dry run: nothing was modified.")
		return
	}
	log.Printf("Applied: %d created, %d updated, %d deleted",
		len(report.Created), len(report.Updated), len(report.Deleted))
}
