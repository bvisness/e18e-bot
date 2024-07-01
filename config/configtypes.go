package config

type E18EConfig struct {
	Db      SqliteConfig
	Discord DiscordConfig
}

type SqliteConfig struct {
	DSN string
	// TODO: Username and stuff, maybe construct DSN dynamically
}

type DiscordConfig struct {
	BotToken  string
	BotUserID string

	GuildID string
}
