package e18e

import (
	"context"
	"fmt"
	"regexp"
	"strconv"

	"github.com/bvisness/e18e-bot/db"
	"github.com/bvisness/e18e-bot/discord"
	"github.com/bvisness/e18e-bot/utils"
)

var TrackCommand = discord.GuildApplicationCommand{
	Type:        discord.ApplicationCommandTypeChatInput,
	Name:        "track",
	Description: "Track an e18e PR",
	Options: []discord.ApplicationCommandOption{
		{
			Type:        discord.ApplicationCommandOptionTypeString,
			Name:        "package",
			Description: "The name of the NPM package being PR'd",
			Required:    true,
		},
		{
			Type:        discord.ApplicationCommandOptionTypeString,
			Name:        "url",
			Description: "The URL of the PR (e.g. https://github.com/...)",
			Required:    true,
		},
	},
	Func: Track,
}

var REGitHubPR = regexp.MustCompile(`https?://github\.com/([^/]+)/([^/]+)/pull/(\d+)`)

func Track(ctx context.Context, rest discord.Rest, i *discord.Interaction) error {
	npmPackage := discord.MustGetInteractionOption(i.Data.Options, "package").Value.(string)
	url := discord.MustGetInteractionOption(i.Data.Options, "url").Value.(string)

	var problems []string
	matches := REGitHubPR.FindStringSubmatch(url)
	if matches == nil {
		problems = append(problems, "The provided URL was not a valid GitHub PR.")
	}

	if len(problems) > 0 {
		return ReportProblems(ctx, rest, i.ID, i.Token, "Couldn't track that PR", problems...)
	}

	owner, repo, pn := matches[1], matches[2], utils.Must1(strconv.Atoi(matches[3]))
	pr, _, err := githubClient.PullRequests.Get(ctx, owner, repo, pn)
	if IsGitHubResponse(err, 404) {
		return ReportProblem(ctx, rest, i.ID, i.Token, "Could not find that PR (is the URL correct?)")
	} else if err != nil {
		return ReportError(ctx, rest, i.ID, i.Token, "Failed to load GitHub PR", err)
	}

	tx := utils.Must1(conn.BeginTx(ctx, nil))
	defer tx.Rollback()
	{
		alreadyExists, err := db.QueryOneScalar[bool](ctx, tx,
			`
			SELECT COUNT(*) > 0 FROM pr
			WHERE owner = ? AND repo = ? AND pull_number = ?
			`,
			owner, repo, pn,
		)
		if err != nil {
			return ReportError(ctx, rest, i.ID, i.Token, "Failed to check for duplicate PR", err)
		}

		if alreadyExists {
			return ReportSuccess(ctx, rest, i.ID, i.Token, "That PR is already tracked.")
		}

		_, err = db.Exec(ctx, tx,
			`
		INSERT INTO pr (owner, repo, pull_number, opened_at, npm_package)
		VALUES (?, ?, ?, ?, ?)
		`,
			owner, repo, pn, pr.CreatedAt.Time, npmPackage,
		)
		if err != nil {
			return ReportError(ctx, rest, i.ID, i.Token, "Failed to save PR", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return ReportError(ctx, rest, i.ID, i.Token, "Failed to save PR", err)
	}

	return ReportSuccess(ctx, rest, i.ID, i.Token, fmt.Sprintf(
		"[%s/%s #%d](%s) is now being tracked!",
		owner, repo, pn, url,
	))
}
