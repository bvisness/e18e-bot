package e18e

import (
	"context"
	"fmt"
	"strconv"

	"github.com/bvisness/e18e-bot/db"
	"github.com/bvisness/e18e-bot/discord"
	"github.com/bvisness/e18e-bot/utils"
)

var UntrackCommand = discord.GuildApplicationCommand{
	Type:        discord.ApplicationCommandTypeChatInput,
	Name:        "untrack",
	Description: "Untrack an e18e PR",
	Options: []discord.ApplicationCommandOption{
		{
			Type:        discord.ApplicationCommandOptionTypeString,
			Name:        "url",
			Description: "The URL of the PR (e.g. https://github.com/...)",
			Required:    true,
		},
	},
	Func: Untrack,
}

func Untrack(ctx context.Context, rest discord.Rest, i *discord.Interaction) error {
	url := discord.MustGetInteractionOption(i.Data.Options, "url").Value.(string)

	var problems []string
	matches := REGitHubPR.FindStringSubmatch(url)
	if matches == nil {
		problems = append(problems, "The provided URL was not a valid GitHub PR.")
	}

	if len(problems) > 0 {
		return ReportProblems(ctx, rest, i.ID, i.Token, "Couldn't untrack that PR", problems...)
	}

	owner, repo, pn := matches[1], matches[2], utils.Must1(strconv.Atoi(matches[3]))
	result, err := db.Exec(ctx, conn,
		`DELETE FROM pr WHERE owner = ? AND repo = ? AND pull_number = ?`,
		owner, repo, pn,
	)
	if err != nil {
		return ReportError(ctx, rest, i.ID, i.Token, "Failed to untrack PR", err)
	}

	if utils.Must1(result.RowsAffected()) > 0 {
		return ReportSuccess(ctx, rest, i.ID, i.Token, fmt.Sprintf(
			"PR [%s/%s #%d](<%s>) has been untracked.",
			owner, repo, pn, url,
		))
	} else {
		return ReportNeutral(ctx, rest, i.ID, i.Token, "That PR is already not tracked. So, success??")
	}
}
