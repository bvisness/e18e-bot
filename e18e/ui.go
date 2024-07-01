package e18e

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/bvisness/e18e-bot/discord"
)

func ReportSuccess(ctx context.Context, rest discord.Rest, id, token string, msg string) error {
	return rest.CreateInteractionResponse(ctx, id, token, discord.InteractionResponse{
		Type: discord.InteractionCallbackTypeChannelMessageWithSource,
		Data: &discord.InteractionCallbackData{
			Content: "✅ " + msg,
		},
	})
}

func ReportError(ctx context.Context, rest discord.Rest, id, token string, msg string, err error) error {
	slog.Error(msg, "err", err)
	return ReportProblem(ctx, rest, id, token, msg+" (see logs)")
}

func ReportProblem(ctx context.Context, rest discord.Rest, id, token string, msg string) error {
	msg = fmt.Sprintf("❌ %s", msg)
	rest.CreateInteractionResponse(ctx, id, token, discord.InteractionResponse{
		Type: discord.InteractionCallbackTypeChannelMessageWithSource,
		Data: &discord.InteractionCallbackData{
			Content: msg,
			Flags:   discord.FlagEphemeral,
		},
	})
	return nil
}

func ReportProblems(ctx context.Context, rest discord.Rest, id, token string, msg string, problems ...string) error {
	msg = fmt.Sprintf("❌ %s:\n", msg)
	for _, p := range problems {
		msg += fmt.Sprintf("- %s\n", p)
	}
	rest.CreateInteractionResponse(ctx, id, token, discord.InteractionResponse{
		Type: discord.InteractionCallbackTypeChannelMessageWithSource,
		Data: &discord.InteractionCallbackData{
			Content: msg,
			Flags:   discord.FlagEphemeral,
		},
	})
	return nil
}
