package discord

import (
	"context"
	"fmt"
)

type GuildApplicationCommand struct {
	Type              ApplicationCommandType
	Name, Description string
	Options           []ApplicationCommandOption
	Func              InteractionHandler
}

type InteractionHandler func(ctx context.Context, rest Rest, i *Interaction) error

func GetInteractionOption(opts []ApplicationCommandInteractionDataOption, name string) (ApplicationCommandInteractionDataOption, bool) {
	for _, opt := range opts {
		if opt.Name == name {
			return opt, true
		}
	}

	return ApplicationCommandInteractionDataOption{}, false
}

func MustGetInteractionOption(opts []ApplicationCommandInteractionDataOption, name string) ApplicationCommandInteractionDataOption {
	opt, ok := GetInteractionOption(opts, name)
	if !ok {
		panic(fmt.Errorf("failed to get interaction option with name '%s'", name))
	}
	return opt
}
