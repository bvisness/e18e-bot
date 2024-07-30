package e18e

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"time"

	"github.com/bvisness/e18e-bot/db"
	"github.com/bvisness/e18e-bot/discord"
	"github.com/bvisness/e18e-bot/jobs"
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

type PackageVersionDesc struct {
	Package, Version string
}

func (p PackageVersionDesc) String() string {
	return fmt.Sprintf("%s@%s", p.Package, p.Version)
}

func (p PackageVersionDesc) LogValue() slog.Value {
	return slog.StringValue(p.String())
}

func PackageStatsCron(ctx context.Context, conn db.ConnOrTx) jobs.Job {
	job := jobs.New()

	go func() {
		defer job.Done()
		defer slog.Info("Stopped package stats job")
		slog.Info("Starting package stats job")

		t := time.NewTicker(5 * time.Second)
		for {
			select {
			case <-t.C:
				var versionsWithoutStats []PackageVersionDesc
				for _, c := range GetPackageVersionStatCounts(ctx, conn) {
					if c.Count == 0 {
						versionsWithoutStats = append(versionsWithoutStats, PackageVersionDesc{
							Package: c.Package,
							Version: c.Version,
						})
					}
				}

				for _, pv := range versionsWithoutStats {
					slog.Info("Gathering initial stats for package", "package", pv)
					SavePackageVersionStats(ctx, pv)
				}
			case <-ctx.Done():
				return
			}
		}
	}()

	return job
}

type PackageVersionThingCounts struct {
	Package string `db:"package"`
	Version string `db:"version"`
	Count   int    `db:"count"`
}

func GetPackageVersionStatCounts(ctx context.Context, conn db.ConnOrTx) []*PackageVersionThingCounts {
	return utils.Must1(db.Query[PackageVersionThingCounts](ctx, conn, `
		WITH counts AS (
			SELECT
				pv.package AS package,
				pv.version AS version,
				COUNT(pvs.package) AS count
			FROM
				package_version AS pv
				LEFT JOIN package_version_stats AS pvs ON
					pv.package = pvs.package
					AND pv.version = pvs.version
			GROUP BY pv.package, pv.version
		)
		SELECT $columns FROM counts
	`))
}

func SavePackageVersionStats(ctx context.Context, pv PackageVersionDesc) {
	allVersionsInfo, err := npmClient.GetPackage(pv.Package)
	if err != nil {
		slog.Error("Failed to get npm package info", "package", pv, "err", err)
		return
	}
	versionInfo := allVersionsInfo.Versions[pv.Version]

	var numDirectDeps, numDirectDepsDev int
	var numTransitiveDeps, numTransitiveDepsDev int
	var selfSize uint64
	var transitiveSize, transitiveSizeDev uint64

	// Basic info we can get without installing anything
	numDirectDeps = len(versionInfo.Dependencies)
	numDirectDepsDev = len(versionInfo.Dependencies) + len(versionInfo.DevDependencies)

	// Install package as normal dependency (non-dev)
	{
		tmpdir, err := installPackageToTmpDir(ctx, pv)
		defer tmpdir.Remove()
		if err != nil {
			slog.Error("Failed to install package", "err", err)
			return
		}
		slog.Info("Installed package to tmpdir", "package", pv, "path", tmpdir)

		var errs []error

		selfSize = tryDirSize(&errs, filepath.Join(string(tmpdir), "node_modules", pv.Package))
		transitiveSize = tryDirSize(&errs, filepath.Join(string(tmpdir), "node_modules"))
		if lockfile, err := readLockfile(filepath.Join(string(tmpdir), "node_modules", ".package-lock.json")); err == nil {
			numTransitiveDeps = utils.Max(0, len(lockfile.Packages)-1)
		} else {
			errs = append(errs, err)
		}

		err = errors.Join(errs...)
		if err != nil {
			slog.Error("Failed to calculate stats", "err", err)
			return
		}
	}

	// Install package and dev dependencies
	{
		var packages []PackageVersionDesc
		for p, v := range versionInfo.Dependencies {
			packages = append(packages, PackageVersionDesc{
				Package: p,
				Version: v,
			})
		}
		for p, v := range versionInfo.DevDependencies {
			packages = append(packages, PackageVersionDesc{
				Package: p,
				Version: v,
			})
		}

		tmpdir, err := installPackagesToTmpDir(ctx, packages)
		defer tmpdir.Remove()
		if err != nil {
			slog.Error("Failed to install package", "err", err)
			return
		}
		slog.Info("Installed packages to tmpdir", "package", pv, "path", tmpdir)

		var errs []error

		transitiveSizeDev = tryDirSize(&errs, filepath.Join(string(tmpdir), "node_modules"))
		if lockfile, err := readLockfile(filepath.Join(string(tmpdir), "node_modules", ".package-lock.json")); err == nil {
			numTransitiveDepsDev = utils.Max(0, len(lockfile.Packages)-1)
		} else {
			errs = append(errs, err)
		}

		err = errors.Join(errs...)
		if err != nil {
			slog.Error("Failed to calculate stats", "err", err)
			return
		}
	}

	_, err = db.Exec(ctx, conn, `
		INSERT INTO package_version_stats (
			package, version,
			date,
			num_direct_dependencies, num_direct_dependencies_dev,
			num_transitive_dependencies, num_transitive_dependencies_dev,
			self_size_bytes,
			transitive_size_bytes, transitive_size_bytes_dev
		) VALUES (
			?, ?,
			?,
			?, ?,
			?, ?,
			?,
			?, ?
		)
	`,
		pv.Package, pv.Version,
		time.Now(),
		numDirectDeps, numDirectDepsDev,
		numTransitiveDeps, numTransitiveDepsDev,
		selfSize,
		transitiveSize, transitiveSizeDev,
	)
	if err != nil {
		slog.Error("Failed to save stats", "err", err)
		return
	}

	slog.Info("Saved package stats to db", "package", pv)
}

func installPackageToTmpDir(ctx context.Context, pv PackageVersionDesc) (TmpDir, error) {
	tmpdir := MakeTmpDir(fmt.Sprintf("package_%s_", sanitizeFilename(pv.String())))

	cmd := exec.CommandContext(ctx, "npm", "install", "--loglevel", "verbose", pv.String())
	cmd.Dir = string(tmpdir)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	err := cmd.Run()
	if err != nil {
		return tmpdir, fmt.Errorf("failed to install package: %w", err)
	}

	return tmpdir, nil
}

func installPackagesToTmpDir(ctx context.Context, pvs []PackageVersionDesc) (TmpDir, error) {
	tmpdir := MakeTmpDir("packages_")

	type PackageJSON struct {
		Dependencies map[string]string `json:"dependencies"`
	}

	pj := PackageJSON{
		Dependencies: make(map[string]string),
	}
	for _, pv := range pvs {
		pj.Dependencies[pv.Package] = pv.Version
	}
	pjBytes := utils.Must1(json.Marshal(pj))
	err := os.WriteFile(filepath.Join(string(tmpdir), "package.json"), pjBytes, 0644)
	if err != nil {
		return tmpdir, err
	}

	cmd := exec.CommandContext(ctx, "npm", "install", "--loglevel", "verbose")
	cmd.Dir = string(tmpdir)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	err = cmd.Run()
	if err != nil {
		return tmpdir, fmt.Errorf("failed to install packages: %w", err)
	}

	return tmpdir, nil
}

var reFilenameUnsafeChar = regexp.MustCompile(`[^a-zA-Z0-9.@_-]`)

func sanitizeFilename(name string) string {
	return reFilenameUnsafeChar.ReplaceAllString(name, "_")
}

func tryDirSize(errs *[]error, path string) uint64 {
	size, err := dirSize(path)
	*errs = append(*errs, err)
	return size
}

func dirSize(path string) (uint64, error) {
	var size uint64
	err := filepath.Walk(path, func(_ string, info os.FileInfo, err error) error {
		if err != nil {
			slog.Warn("error while calculating dir size", "path", path, "err", err)
			return err
		}

		if !info.IsDir() {
			size += uint64(info.Size())
		}

		return err
	})

	return size, err
}

func readLockfile(path string) (npm.PackageLockFile, error) {
	lockfileFile, err := os.Open(path)
	if err != nil {
		return npm.PackageLockFile{}, err
	}
	lockfileBytes, err := io.ReadAll(lockfileFile)
	if err != nil {
		return npm.PackageLockFile{}, err
	}
	var lockfile npm.PackageLockFile
	utils.Must(json.Unmarshal(lockfileBytes, &lockfile))
	return lockfile, nil
}
