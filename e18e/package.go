package e18e

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/bvisness/e18e-bot/db"
	"github.com/bvisness/e18e-bot/discord"
	"github.com/bvisness/e18e-bot/npm"
	"github.com/bvisness/e18e-bot/utils"
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
					Description: "The name of the npm package",
					Required:    true,
				},
			},
			Func: TrackPackage,
		},

		// Untracking is disabled for now...we don't want to do this by accident.
		// {
		// 	Name:        "untrack",
		// 	Description: "Untrack an npm package",
		// 	Options: []discord.ApplicationCommandOption{
		// 		{
		// 			Type:        discord.ApplicationCommandOptionTypeString,
		// 			Name:        "package",
		// 			Description: "The name of the npm package",
		// 			Required:    true,
		// 		},
		// 	},
		// 	Func: UntrackPackage,
		// },
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
	name := opts.MustGet("package").Value.(string)
	p, err := npmClient.GetPackage(name)
	if err == npm.ErrNotFound {
		return ReportProblem(ctx, rest, i.ID, i.Token, "No package found with that name.")
	}

	tx := utils.Must1(conn.BeginTx(ctx, nil))
	defer tx.Rollback()
	{
		alreadyExists, err := db.QueryOneScalar[bool](ctx, tx,
			`
			SELECT COUNT(*) > 0 FROM package
			WHERE name = ?
			`,
			p.Name,
		)
		if err != nil {
			return ReportError(ctx, rest, i.ID, i.Token, "Failed to check for duplicate package", err)
		}
		if alreadyExists {
			return ReportSuccess(ctx, rest, i.ID, i.Token, "That package is already tracked.")
		}

		_, err = db.Exec(ctx, tx,
			`
			INSERT INTO package (name)
			VALUES (?)
			`,
			p.Name,
		)
		if err != nil {
			return ReportError(ctx, rest, i.ID, i.Token, "Failed to save package", err)
		}

		for _, v := range p.Versions {
			var maintainers []string
			for _, m := range v.Maintainers {
				maintainers = append(maintainers, m.Name)
			}

			_, err = db.Exec(ctx, tx,
				`
				INSERT INTO package_version (package, version, published_at, published_by, maintainers)
				VALUES (?, ?, ?, ?, ?)
				`,
				p.Name, v.Version, v.PublishedAt, v.Publisher.Name, utils.Must1(json.Marshal(maintainers)),
			)
			if err != nil {
				return ReportError(ctx, rest, i.ID, i.Token, "Failed to save package", err)
			}
		}
	}
	if err := tx.Commit(); err != nil {
		return ReportError(ctx, rest, i.ID, i.Token, "Failed to save package", err)
	}

	return ReportSuccess(ctx, rest, i.ID, i.Token, fmt.Sprintf(
		"[%s](%s) is now being tracked!",
		p.Name, p.Url(),
	))
}

func UntrackPackage(ctx context.Context, rest discord.Rest, i *discord.Interaction, opts discord.InteractionOptions) error {
	name := opts.MustGet("package").Value.(string)

	var didDelete bool

	tx := utils.Must1(conn.BeginTx(ctx, nil))
	defer tx.Rollback()
	{
		_, err := db.Exec(ctx, conn,
			`DELETE FROM package_version_downloads WHERE package = ?`,
			name,
		)
		if err != nil {
			return ReportError(ctx, rest, i.ID, i.Token, "Failed to untrack package", err)
		}

		_, err = db.Exec(ctx, conn,
			`DELETE FROM package_version WHERE package = ?`,
			name,
		)
		if err != nil {
			return ReportError(ctx, rest, i.ID, i.Token, "Failed to untrack package", err)
		}

		result, err := db.Exec(ctx, conn,
			`DELETE FROM package WHERE name = ?`,
			name,
		)
		if err != nil {
			return ReportError(ctx, rest, i.ID, i.Token, "Failed to untrack package", err)
		}

		didDelete = utils.Must1(result.RowsAffected()) > 0
	}
	if err := tx.Commit(); err != nil {
		return ReportError(ctx, rest, i.ID, i.Token, "Failed to save package", err)
	}

	if didDelete {
		return ReportSuccess(ctx, rest, i.ID, i.Token, fmt.Sprintf(
			"Package %s has been untracked.",
			name,
		))
	} else {
		return ReportNeutral(ctx, rest, i.ID, i.Token, "That package is already not tracked. So, success??")
	}
}
