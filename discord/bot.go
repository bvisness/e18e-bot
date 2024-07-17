package discord

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math/rand"
	"net"
	"runtime"
	"sort"
	"sync"
	"time"

	"github.com/bvisness/e18e-bot/jobs"
	"github.com/bvisness/e18e-bot/utils"
	"github.com/gorilla/websocket"
	"github.com/jpillora/backoff"
)

type BotOptions struct {
	Events                   BotEvents
	GuildApplicationCommands []GuildApplicationCommand

	// If omitted, defaults to slog.Default()
	Logger *slog.Logger
}

type BotEvents struct {
	OnMessageCreate func(ctx context.Context, bot *BotInstance, msg *Message) error
	OnMessageUpdate func(ctx context.Context, bot *BotInstance, msg *Message) error
	OnMessageDelete func(ctx context.Context, bot *BotInstance, msgDelete MessageDelete) error
}

func RunBot(ctx context.Context, token, applicationID string, persistence Persistence, opts BotOptions) jobs.Job {
	logger := utils.Or(opts.Logger, slog.Default())

	utils.Assert(token, "bot token")
	utils.Assert(applicationID, "bot application ID")

	job := jobs.New()
	go func() {
		defer func() {
			logger.Debug("shut down Discord bot")
			job.Done()
		}()

		boff := backoff.Backoff{
			Min: 1 * time.Second,
			Max: 5 * time.Minute,
		}

		for {
			select {
			case <-ctx.Done():
				return
			default:
			}

			err := func() (retErr error) {
				defer utils.RecoverPanicAsError(&retErr)
				logger.Info("Connecting to the Discord gateway")
				bot := newBotInstance(token, applicationID, persistence, opts)
				err := bot.run(ctx)
				if err != nil {
					dur := boff.Duration()
					logger.Error("failed to run Discord bot", "err", err, "retrying after", dur)

					timer := time.NewTimer(dur)
					select {
					case <-ctx.Done():
					case <-timer.C:
					}

					return
				}

				select {
				case <-ctx.Done():
					return
				default:
				}

				// This delay satisfies the 1 to 5 second delay Discord
				// wants on reconnects, and seems fine to do every time.
				delay := time.Duration(int64(time.Second) + rand.Int63n(int64(time.Second*4)))
				logger.Info("Reconnecting to Discord", "delay", delay)
				time.Sleep(delay)

				boff.Reset()
				return nil
			}()
			if err != nil {
				logger.Error("Panicked in RunDiscordBot", "err", err)
			}
		}
	}()
	return job
}

type BotInstance struct {
	token, applicationID string

	Rest   Rest
	Logger *slog.Logger

	events        BotEvents
	guildCommands []GuildApplicationCommand
	commands      map[string]InteractionHandler

	conn        *websocket.Conn
	persistence Persistence
	sessionID   string
	resuming    bool

	heartbeatIntervalMs int
	forceHeartbeat      chan struct{}

	/*
	   Every time we send a heartbeat, we set this variable to false.
	   Whenever we ack a heartbeat, we set this variable to true.
	   If we try to send a heartbeat but the previous one was not
	   acked, then we close the connection and try to reconnect.
	*/
	didAckHeartbeat bool

	/*
		All goroutines should call this when they exit, to ensure that
		the other goroutines shut down as well.
	*/
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

func newBotInstance(token, applicationID string, persistence Persistence, opts BotOptions) *BotInstance {
	return &BotInstance{
		token:         token,
		applicationID: applicationID,

		Rest: Rest{
			BotToken: token,
			logger:   opts.Logger, // Rest handles the default case on its own
		},
		Logger: utils.Or(opts.Logger, slog.Default()),

		events:        opts.Events,
		guildCommands: opts.GuildApplicationCommands,
		commands:      make(map[string]InteractionHandler),

		persistence:     persistence,
		forceHeartbeat:  make(chan struct{}),
		didAckHeartbeat: true,
	}
}

/*
Runs a bot instance to completion. It will start up a gateway connection and return when the
connection is closed. It only returns an error when something unexpected occurs; if so, you should
do exponential backoff before reconnecting. Otherwise you can reconnect right away.
*/
func (bot *BotInstance) run(ctx context.Context) (err error) {
	defer utils.RecoverPanicAsErrorAndLog(&err, bot.Logger)

	ctx, bot.cancel = context.WithCancel(ctx)
	defer bot.cancel()

	err = bot.connect(ctx)
	if err != nil {
		return fmt.Errorf("failed to connect to Discord gateway: %w", err)
	}
	defer bot.conn.Close()

	bot.wg.Add(1)
	go bot.doSender(ctx)

	// Wait for child goroutines to exit (they will do so when context is canceled). This ensures
	// that nothing is in the middle of sending. Then close the connection, so that this goroutine
	// can finish as well.
	go func() {
		bot.wg.Wait()
		bot.conn.Close()
	}()

	for {
		msg, err := bot.receiveGatewayMessage(ctx)
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				// If the connection is closed, that's our cue to shut down the bot. Any errors
				// related to the closure will have been logged elsewhere anyway.
				return nil
			} else {
				// NOTE(ben): I don't know what events we might get in the future that we might
				// want to handle gracefully (like above). Keep an eye out.
				return fmt.Errorf("failed to receive message from the gateway: %w", err)
			}
		}

		// Update the sequence number in the db
		if msg.SequenceNumber != nil {
			utils.Assert(bot.sessionID, "session id")
			err := bot.persistence.StoreSession(ctx, Session{
				ID:             bot.sessionID,
				SequenceNumber: *msg.SequenceNumber,
			})
			if err != nil {
				return fmt.Errorf("failed to save latest sequence number: %w", err)
			}
		}

		switch msg.Opcode {
		case OpcodeDispatch:
			// Just a normal event
			bot.processEventMsg(ctx, msg)
		case OpcodeHeartbeat:
			bot.forceHeartbeat <- struct{}{}
		case OpcodeHeartbeatACK:
			bot.didAckHeartbeat = true
		case OpcodeReconnect:
			bot.Logger.Info("Discord asked us to reconnect to the gateway")
			return nil
		case OpcodeInvalidSession:
			// We tried to resume but the session was invalid.
			// Delete the session and reconnect from scratch again.
			bot.Logger.Warn("Failed to resume - invalid session")
			err := bot.persistence.DeleteSession(ctx)
			if err != nil {
				return fmt.Errorf("failed to delete invalid session: %w", err)
			}
			return nil
		}
	}
}

/*
The connection process in short:
- Gateway sends Hello, asking the client to heartbeat on some interval
- Client sends Identify and starts heartbeat process
- Gateway sends Ready, client is now connected to gateway

Or, if we have an existing session:
  - Gateway sends Hello, asking the client to heartbeat on some interval
  - Client sends Resume and starts heartbeat process
  - Gateway sends all missed events followed by a RESUMED event, or an Invalid Session if the
    session is ded

Note that some events probably won't be received until the Guild Create message is received.

It's a little annoying to handle resumes since we want to handle the missed messages as if we were
receiving them in real time. But we're kind of in a different state from from when we're normally
receiving messages, because we are expecting a RESUMED event at the end, and the first message we
receive might be an Invalid Session. So, unfortunately, we just have to handle the Invalid Session
and RESUMED messages in our main message receiving loop instead of here.

(Discord could have prevented this if they send a "Resume ACK" message before replaying events.
That way, we could receive exactly one message after sending Resume, either a Resume ACK or an
Invalid Session, and from there it would be crystal clear what to do. Alas!)
*/
func (bot *BotInstance) connect(ctx context.Context) (err error) {
	res, err := bot.Rest.GetGatewayBot(ctx)
	if err != nil {
		return fmt.Errorf("failed to get gateway URL: %w", err)
	}

	conn, _, err := websocket.DefaultDialer.Dial(fmt.Sprintf("%s/?v=9&encoding=json", res.URL), nil)
	if err != nil {
		return fmt.Errorf("failed to connect to the Discord gateway: %w", err)
	}
	bot.conn = conn

	helloMessage, err := bot.receiveGatewayMessage(ctx)
	if err != nil {
		return fmt.Errorf("failed to read Hello message: %w", err)
	}
	if helloMessage.Opcode != OpcodeHello {
		return fmt.Errorf("expected a Hello (opcode %d), but got opcode %d", OpcodeHello, helloMessage.Opcode)
	}
	helloData := HelloFromMap(helloMessage.Data)
	bot.heartbeatIntervalMs = helloData.HeartbeatIntervalMs

	// Now that the gateway has said hello, we need to establish a new session, either resuming
	// an old one or starting a new one.

	shouldResume := true
	session, ok, err := bot.persistence.LoadSession(ctx)
	if err != nil {
		return fmt.Errorf("failed to get current session from database: %w", err)
	}
	if !ok {
		// No session yet! Just identify and get on with it
		shouldResume = false
	}

	bot.Logger.Info("Connected to Discord gateway")
	if shouldResume {
		bot.Logger.Info("Resuming", "session id", session.ID)
		// Reconnect to the previous session
		bot.resuming = true
		err := bot.sendGatewayMessage(ctx, GatewayMessage{
			Opcode: OpcodeResume,
			Data: Resume{
				Token:          bot.token,
				SessionID:      session.ID,
				SequenceNumber: session.SequenceNumber,
			},
		})
		if err != nil {
			return fmt.Errorf("failed to send Resume message: %w", err)
		}

		bot.sessionID = session.ID

		return nil
	} else {
		// Start a new session
		err := bot.sendGatewayMessage(ctx, GatewayMessage{
			Opcode: OpcodeIdentify,
			Data: Identify{
				Token: bot.token,
				Properties: IdentifyConnectionProperties{
					OS:      runtime.GOOS,
					Browser: "HandmadeDiscord",
					Device:  "HandmadeDiscord",
				},
				Intents: IntentGuilds | IntentGuildMessages,
			},
		})
		if err != nil {
			return fmt.Errorf("failed to send Identify message: %w", err)
		}

		readyMessage, err := bot.receiveGatewayMessage(ctx)
		if err != nil {
			return fmt.Errorf("failed to read Ready message: %w", err)
		}
		if readyMessage.Opcode != OpcodeDispatch {
			return fmt.Errorf("expected a READY event, but got a message with opcode %d: %w", readyMessage.Opcode, err)
		}
		if *readyMessage.EventName != "READY" {
			return fmt.Errorf("expected a READY event, but got a %s event: %w", *readyMessage.EventName, err)
		}
		readyData := ReadyFromMap(readyMessage.Data)

		err = bot.persistence.StoreSession(ctx, Session{
			ID:             readyData.SessionID,
			SequenceNumber: *readyMessage.SequenceNumber,
		})
		if err != nil {
			return fmt.Errorf("failed to save new bot session in the database: %w", err)
		}

		bot.sessionID = readyData.SessionID
	}

	return nil
}

/*
Sends outgoing gateway messages and channel messages. Handles heartbeats. This function should be
run as its own goroutine.
*/
func (bot *BotInstance) doSender(ctx context.Context) {
	defer bot.wg.Done()
	defer bot.cancel()

	logger := bot.Logger.With("discord goroutine", "sender")

	defer logger.Info("shutting down Discord sender")

	/*
		The first heartbeat is supposed to occur at a random time within
		the first heartbeat interval.

		https://discord.com/developers/docs/topics/gateway#heartbeating
	*/
	dur := time.Duration(bot.heartbeatIntervalMs) * time.Millisecond
	firstDelay := time.NewTimer(time.Duration(rand.Int63n(int64(dur))))
	heartbeatTicker := &time.Ticker{} // this will start never ticking, and get initialized after the first heartbeat

	// Returns false if the heartbeat failed
	sendHeartbeat := func() bool {
		if !bot.didAckHeartbeat {
			logger.Error("did not receive a heartbeat ACK in between heartbeats")
			return false
		}
		bot.didAckHeartbeat = false

		session, ok, err := bot.persistence.LoadSession(ctx)
		if !ok || err != nil {
			logger.Error("failed to fetch latest sequence number from the db", "err", err)
			return false
		}

		err = bot.sendGatewayMessage(ctx, GatewayMessage{
			Opcode: OpcodeHeartbeat,
			Data:   session.SequenceNumber,
		})
		if err != nil {
			logger.Error("failed to send heartbeat", "err", err)
			return false
		}

		return true
	}

	/*
		Start a goroutine to fetch outgoing messages from the db. We do this in a separate goroutine
		to ensure that issues talking to the database don't prevent us from sending heartbeats.
	*/
	messages := make(chan OutgoingMessage)
	bot.wg.Add(1)
	go func(ctx context.Context) {
		defer bot.wg.Done()
		defer bot.cancel()

		logger := bot.Logger.With("discord goroutine", "sender db reader")

		defer logger.Info("stopping db reader")

		// We will poll the database just in case the notification mechanism doesn't work.
		ticker := time.NewTicker(time.Second * 5)

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			case <-outgoingMessagesReady:
			}

			func() {
				var msgIDs []string
				msgs, err := bot.persistence.LoadOutgoingMessages(ctx)
				if err != nil {
					logger.Error("failed to fetch outgoing Discord messages", "err", err)
					return
				}

				sort.SliceStable(msgs, func(i, j int) bool {
					return msgs[i].ID < msgs[j].ID
				})
				for _, msg := range msgs {
					msgIDs = append(msgIDs, msg.ID)
					if time.Now().After(msg.ExpiresAt) {
						continue
					}
					messages <- msg
				}

				err = bot.persistence.DeleteOutgoingMessages(ctx, msgIDs)
				if err != nil {
					logger.Error("failed to delete outgoing messages", "err", err)
					return
				}

				if len(msgs) > 0 {
					logger.Debug("Sent and deleted outgoing messages", "num messages", len(msgs))
				}
			}()
		}
	}(ctx)

	/*
		Whenever we want to send a gateway message, we must receive a value from
		this channel first. A goroutine continuously fills the channel at a rate
		that respects Discord's gateway rate limit.

		Don't use this for heartbeats; heartbeats should go out immediately.
		Don't forget that the server can request a heartbeat at any time.

		See the docs for more details. The capacity of this channel is chosen to
		always leave us overhead for heartbeats and other shenanigans.

		https://discord.com/developers/docs/topics/gateway#rate-limiting
	*/
	rateLimiter := make(chan struct{}, 100)
	go func() {
		for {
			rateLimiter <- struct{}{}
			time.Sleep(500 * time.Millisecond)
		}
	}()
	/*
		NOTE(ben): This rate limiter is actually not used right now
		because we're not actually sending any meaningful gateway
		messages. But in the future, if we end up sending presence
		updates or other gateway commands, we need to make sure to
		put this limiter on all of those outgoing commands.
	*/

	for {
		select {
		case <-ctx.Done():
			return
		case <-firstDelay.C:
			if ok := sendHeartbeat(); !ok {
				return
			}
			heartbeatTicker = time.NewTicker(dur)
		case <-heartbeatTicker.C:
			if ok := sendHeartbeat(); !ok {
				return
			}
		case <-bot.forceHeartbeat:
			if ok := sendHeartbeat(); !ok {
				return
			}
			heartbeatTicker.Reset(dur)
		case msg := <-messages:
			_, err := bot.Rest.CreateMessage(ctx, msg.ChannelID, msg.Req)
			if err != nil {
				bot.Logger.Error("failed to send Discord message", "err", err)
			}
		}
	}
}

func (bot *BotInstance) receiveGatewayMessage(ctx context.Context) (*GatewayMessage, error) {
	_, msgBytes, err := bot.conn.ReadMessage()
	if err != nil {
		return nil, err
	}

	var msg GatewayMessage
	err = json.Unmarshal(msgBytes, &msg)
	if err != nil {
		return nil, fmt.Errorf("failed to unmarshal Discord gateway message: %w", err)
	}

	bot.Logger.Debug("received gateway message", "opcode", msg.Opcode, "name", utils.LogPtr(msg.EventName), "msg", msg)

	return &msg, nil
}

func (bot *BotInstance) sendGatewayMessage(ctx context.Context, msg GatewayMessage) error {
	bot.Logger.Debug("sending gateway message", "msg", msg)
	return bot.conn.WriteMessage(websocket.TextMessage, msg.ToJSON())
}

/*
Processes a single event message from Discord. Please make sure to handle panics.
*/
func (bot *BotInstance) processEventMsg(ctx context.Context, msg *GatewayMessage) {
	if msg.Opcode != OpcodeDispatch {
		panic(fmt.Sprintf("processEventMsg must only be used on Dispatch messages (opcode %d). Validate this before you call this function.", OpcodeDispatch))
	}

	if bot.resuming {
		name := ""
		if msg.EventName != nil {
			name = *msg.EventName
		}
		bot.Logger.Info("Got event while resuming", "name", name)
	}
	switch *msg.EventName {
	case "RESUMED":
		// Nothing to do, but at least we can log something
		bot.Logger.Info("Finished resuming gateway session")

		bot.resuming = false
		bot.Logger.Info("Done resuming")
	case "MESSAGE_CREATE":
		newMessage := *MessageFromMap(msg.Data, "")
		bot.messageCreate(ctx, &newMessage)
	case "MESSAGE_UPDATE":
		newMessage := *MessageFromMap(msg.Data, "")
		bot.messageUpdate(ctx, &newMessage)
	case "MESSAGE_DELETE":
		bot.messageDelete(ctx, MessageDeleteFromMap(msg.Data))
	case "MESSAGE_BULK_DELETE":
		bulkDelete := MessageBulkDeleteFromMap(msg.Data)
		for _, id := range bulkDelete.IDs {
			bot.messageDelete(ctx, MessageDelete{
				ID:        id,
				ChannelID: bulkDelete.ChannelID,
				GuildID:   bulkDelete.GuildID,
			})
		}
	case "GUILD_CREATE":
		guild := utils.Assert(GuildFromMap(msg.Data, ""), "guild")

		bot.guildCreate(ctx, *guild)
	case "INTERACTION_CREATE":
		go bot.doInteraction(ctx, InteractionFromMap(msg.Data, ""))
	}
}

func (bot *BotInstance) messageCreate(ctx context.Context, msg *Message) {
	if msg.OriginalHasFields("author") && msg.Author.ID == bot.applicationID {
		// Don't process your own messages
		return
	}

	if bot.events.OnMessageCreate != nil {
		err := bot.events.OnMessageCreate(ctx, bot, msg)
		if err != nil {
			bot.Logger.ErrorContext(ctx, "error in OnMessageCreate", "err", err)
		}
	}
}

func (bot *BotInstance) messageUpdate(ctx context.Context, msg *Message) {
	if msg.OriginalHasFields("author") && msg.Author.ID == bot.applicationID {
		// Don't process your own messages
		return
	}

	if bot.events.OnMessageUpdate != nil {
		err := bot.events.OnMessageUpdate(ctx, bot, msg)
		if err != nil {
			bot.Logger.ErrorContext(ctx, "error in OnMessageUpdate", "err", err)
		}
	}
}

func (bot *BotInstance) messageDelete(ctx context.Context, msgDelete MessageDelete) {
	if bot.events.OnMessageDelete != nil {
		err := bot.events.OnMessageDelete(ctx, bot, msgDelete)
		if err != nil {
			bot.Logger.ErrorContext(ctx, "error in OnMessageDelete", "err", err)
		}
	}
}

func (bot *BotInstance) guildCreate(ctx context.Context, guild Guild) {
	bot.createApplicationCommands(ctx, guild.ID)
}

func (bot *BotInstance) createApplicationCommands(ctx context.Context, guildID string) {
	// Register guild application commands. This looks crazy and ugly because there is
	// no elegant symmetry to subcommands in Discord.
	for _, cmd := range bot.guildCommands {
		req := CreateGuildApplicationCommandRequest{
			Type:        cmd.Type,
			Name:        cmd.Name,
			Description: cmd.Description,
		}

		if len(cmd.Subcommands) > 0 {
			for _, subcmd := range cmd.Subcommands {
				subpath := cmd.Name + "/" + subcmd.Name
				subreq := ApplicationCommandOption{
					Name:        subcmd.Name,
					Description: subcmd.Description,
				}
				if len(subcmd.Subcommands) > 0 {
					subreq.Type = ApplicationCommandOptionTypeSubCommandGroup
					for _, subsubcmd := range subcmd.Subcommands {
						subsubpath := subpath + "/" + subsubcmd.Name
						subreq.Options = append(subreq.Options, ApplicationCommandOption{
							Type:        ApplicationCommandOptionTypeSubCommand,
							Name:        subsubcmd.Name,
							Description: subsubcmd.Description,
							Options:     subsubcmd.Options,
						})
						bot.commands[subsubpath] = subsubcmd.Func
					}
				} else {
					subreq.Type = ApplicationCommandOptionTypeSubCommand
					subreq.Options = subcmd.Options
					bot.commands[subpath] = subcmd.Func
				}
				req.Options = append(req.Options, subreq)
			}
		} else {
			req.Options = cmd.Options
			bot.commands[cmd.Name] = cmd.Func
		}

		err := bot.Rest.CreateGuildApplicationCommand(ctx, bot.applicationID, guildID, req)
		if err != nil {
			bot.Logger.Error("failed to create application command", "err", err)
		}
	}
}

func (bot *BotInstance) doInteraction(ctx context.Context, i *Interaction) (err error) {
	defer utils.RecoverPanicAsErrorAndLog(&err, bot.Logger)

	handlerPath := i.Data.Name
	handlerOpts := i.Data.Options
	for _, opt := range i.Data.Options {
		switch opt.Type {
		case ApplicationCommandOptionTypeSubCommand:
			handlerPath += "/" + opt.Name
			handlerOpts = opt.Options
		case ApplicationCommandOptionTypeSubCommandGroup:
			utils.Assert(len(opt.Options) == 1, "expected a single subcommand in the group")
			subopt := opt.Options[0]
			handlerPath += "/" + opt.Name + "/" + subopt.Name
			handlerOpts = subopt.Options
		}
	}

	if cmd, ok := bot.commands[handlerPath]; ok {
		err := cmd(ctx, bot.Rest, i, handlerOpts)
		if err != nil {
			bot.Logger.Error("error in application command handler", "err", err)
		}
	} else {
		bot.Logger.Warn("unrecognized application command", "name", handlerPath)
	}

	return nil
}
