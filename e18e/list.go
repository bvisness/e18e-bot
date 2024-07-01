package e18e

import (
	"context"
	"fmt"

	"github.com/bvisness/e18e-bot/db"
	"github.com/bvisness/e18e-bot/discord"
)

var ListCommand = discord.GuildApplicationCommand{
	Type:        discord.ApplicationCommandTypeChatInput,
	Name:        "list",
	Description: "List all the PRs currently being tracked.",
	Func:        List,
}

func List(ctx context.Context, rest discord.Rest, i *discord.Interaction) error {
	prs, err := db.Query[PR](ctx, conn, `SELECT $columns FROM pr`)
	if err != nil {
		return ReportError(ctx, rest, i.ID, i.Token, "Failed to load PRs", err)
	}

	var msg string
	if len(prs) == 0 {
		msg = "No PRs are currently being tracked."
	} else {
		msg = "The following PRs are currently being tracked:\n"
		for _, pr := range prs {
			msg += fmt.Sprintf("- [%s/%s#%d](<%s>)\n", pr.Owner, pr.Repo, pr.PullNumber, pr.Url())
		}
	}

	return rest.CreateInteractionResponse(ctx, i.ID, i.Token, discord.InteractionResponse{
		Type: discord.InteractionCallbackTypeChannelMessageWithSource,
		Data: &discord.InteractionCallbackData{
			Content: msg,
		},
	})
}
