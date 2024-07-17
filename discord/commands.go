package discord

import (
	"context"
	"fmt"
)

type GuildApplicationCommand struct {
	// The base type of command. Can only be set on the top-level command, not subcommands.
	Type ApplicationCommandType

	Name, Description string

	// Options and Func are for commands that will be directly handled, and will be ignored if there are subcommands.
	Options []ApplicationCommandOption
	Func    InteractionHandler

	Subcommands []GuildApplicationCommand
}

type InteractionHandler func(
	ctx context.Context,
	rest Rest,
	i *Interaction,
	opts InteractionOptions,
) error

type InteractionOptions []ApplicationCommandInteractionDataOption

func (opts InteractionOptions) Get(name string) (ApplicationCommandInteractionDataOption, bool) {
	for _, opt := range opts {
		if opt.Name == name {
			return opt, true
		}
	}

	return ApplicationCommandInteractionDataOption{}, false
}

func (opts InteractionOptions) MustGet(name string) ApplicationCommandInteractionDataOption {
	opt, ok := opts.Get(name)
	if !ok {
		panic(fmt.Errorf("failed to get interaction option with name '%s'", name))
	}
	return opt
}
