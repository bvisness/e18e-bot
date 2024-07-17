package e18e

import (
	"context"
	"fmt"

	"github.com/bvisness/e18e-bot/db"
	"github.com/bvisness/e18e-bot/discord"
)

var PackageCommandGroup = discord.GuildApplicationCommand{
	Type:        discord.ApplicationCommandTypeChatInput,
	Name:        "package",
	Description: "Commands related to npm packages.",
	Subcommands: []discord.GuildApplicationCommand{
		{
			Name:        "list",
			Description: "List the npm packages currently being tracked.",
			Func:        ListPackages,
		},
		{
			Name:        "track",
			Description: "Track an npm package",
			Options: []discord.ApplicationCommandOption{
				{
					Type:        discord.ApplicationCommandOptionTypeString,
					Name:        "package",
					Description: "The name of the NPM package",
					Required:    true,
				},
			},
			Func: TrackPR,
		},
	},
}

func ListPackages(ctx context.Context, rest discord.Rest, i *discord.Interaction, opts discord.InteractionOptions) error {
	packages, err := db.Query[Package](ctx, conn, `SELECT $columns FROM package`)
	if err != nil {
		return ReportError(ctx, rest, i.ID, i.Token, "Failed to load packages", err)
	}

	var msg string
	if len(packages) == 0 {
		msg = "No packages are currently being tracked."
	} else {
		msg = "The following packages are currently being tracked:\n"
		for _, p := range packages {
			msg += fmt.Sprintf("- [%s](<%s>)\n", p.Name, p.Url())
		}
	}

	return rest.CreateInteractionResponse(ctx, i.ID, i.Token, discord.InteractionResponse{
		Type: discord.InteractionCallbackTypeChannelMessageWithSource,
		Data: &discord.InteractionCallbackData{
			Content: msg,
		},
	})
}

func TrackPackage(ctx context.Context, rest discord.Rest, i *discord.Interaction, opts discord.InteractionOptions) error {
	npmPackage := opts.MustGet("package").Value.(string)

	// ok now fetch it or whatever I dunno
}
