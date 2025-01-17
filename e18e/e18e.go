package e18e

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"

	"github.com/bvisness/e18e-bot/config"
	"github.com/bvisness/e18e-bot/discord"
	"github.com/bvisness/e18e-bot/jobs"
	"github.com/bvisness/e18e-bot/npm"
)

var npmClient = npm.Client{C: http.DefaultClient}

func Run() {
	fmt.Println("Hello, e18e!")

	h := slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		Level: slog.LevelDebug,
	})
	slog.SetDefault(slog.New(h))

	OpenDB()
	MigrateDB()

	ctx, shutdown := context.WithCancel(context.Background())
	allJobs := jobs.Zip(
		// TODO: real persistence
		discord.RunBot(ctx, config.Config.Discord.BotToken, config.Config.Discord.BotUserID, &discord.DummyPersistence{}, discord.BotOptions{
			GuildApplicationCommands: []discord.GuildApplicationCommand{
				PRCommandGroup,
				PackageCommandGroup,
			},
		}),
		PackageStatsCron(ctx, conn),
	)

	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt)
	go func() {
		<-signals
		slog.Info("Shutting down the e18e bot")
		shutdown()

		<-signals
		slog.Warn("Forcibly killed the e18e bot")
		os.Exit(1)
	}()

	<-allJobs.C
}
