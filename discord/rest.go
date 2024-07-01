package discord

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httputil"
	"net/textproto"
	"strconv"

	"github.com/bvisness/e18e-bot/utils"
)

const UserAgent = "HandmadeDiscord (https://github.com/HandmadeNetwork/discord, 1.0)"
const baseUrl = "https://discord.com/api/v9"

type Rest struct {
	BotToken string
	logger   *slog.Logger
}

func (r *Rest) Logger() *slog.Logger {
	if r.logger != nil {
		return r.logger
	}
	return slog.Default()
}

var NotFound = errors.New("not found")

var httpClient = &http.Client{}

func buildUrl(path string) string {
	return fmt.Sprintf("%s%s", baseUrl, path)
}

func (r *Rest) makeRequest(ctx context.Context, method string, path string, body []byte) *http.Request {
	var bodyReader io.Reader
	if body != nil {
		bodyReader = bytes.NewBuffer(body)
	}

	req := utils.Must1(http.NewRequestWithContext(ctx, method, buildUrl(path), bodyReader))
	req.Header.Add("Authorization", fmt.Sprintf("Bot %s", r.BotToken))
	req.Header.Add("User-Agent", UserAgent)

	return req
}

type GetGatewayBotResponse struct {
	URL string `json:"url"`
	// We don't care about shards or session limit stuff; we will never hit those limits
}

func (r *Rest) GetGatewayBot(ctx context.Context) (*GetGatewayBotResponse, error) {
	const name = "Get Gateway Bot"

	res, err := r.doWithRateLimiting(ctx, name, func(ctx context.Context) *http.Request {
		return r.makeRequest(ctx, http.MethodGet, "/gateway/bot", nil)
	})
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()

	if res.StatusCode != 200 {
		r.logErrorResponse(ctx, name, res, "")
		return nil, fmt.Errorf("received error from Discord")
	}

	body, err := io.ReadAll(res.Body)
	if err != nil {
		panic(err)
	}

	var result GetGatewayBotResponse
	err = json.Unmarshal(body, &result)
	if err != nil {
		return nil, fmt.Errorf("failed to unmarshal Discord response: %w", err)
	}

	return &result, nil
}

func (r *Rest) GetGuildRoles(ctx context.Context, guildID string) ([]Role, error) {
	const name = "Get Guild Roles"

	path := fmt.Sprintf("/guilds/%s/roles", guildID)
	res, err := r.doWithRateLimiting(ctx, name, func(ctx context.Context) *http.Request {
		return r.makeRequest(ctx, http.MethodGet, path, nil)
	})
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()

	if res.StatusCode >= 400 {
		r.logErrorResponse(ctx, name, res, "")
		return nil, fmt.Errorf("received error from Discord")
	}

	bodyBytes, err := io.ReadAll(res.Body)
	if err != nil {
		panic(err)
	}

	var roles []Role
	err = json.Unmarshal(bodyBytes, &roles)
	if err != nil {
		return nil, fmt.Errorf("failed to unmarshal Discord message: %w", err)
	}

	return roles, nil
}

func (r *Rest) GetGuildChannels(ctx context.Context, guildID string) ([]Channel, error) {
	const name = "Get Guild Channels"

	path := fmt.Sprintf("/guilds/%s/channels", guildID)
	res, err := r.doWithRateLimiting(ctx, name, func(ctx context.Context) *http.Request {
		return r.makeRequest(ctx, http.MethodGet, path, nil)
	})
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()

	if res.StatusCode >= 400 {
		r.logErrorResponse(ctx, name, res, "")
		return nil, fmt.Errorf("received error from Discord")
	}

	bodyBytes, err := io.ReadAll(res.Body)
	if err != nil {
		panic(err)
	}

	var channels []Channel
	err = json.Unmarshal(bodyBytes, &channels)
	if err != nil {
		return nil, fmt.Errorf("failed to unmarshal Discord message: %w", err)
	}

	return channels, nil
}

func (r *Rest) GetGuildMember(ctx context.Context, guildID, userID string) (*GuildMember, error) {
	const name = "Get Guild Member"

	path := fmt.Sprintf("/guilds/%s/members/%s", guildID, userID)
	res, err := r.doWithRateLimiting(ctx, name, func(ctx context.Context) *http.Request {
		return r.makeRequest(ctx, http.MethodGet, path, nil)
	})
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()

	if res.StatusCode == http.StatusNotFound {
		return nil, NotFound
	} else if res.StatusCode >= 400 {
		r.logErrorResponse(ctx, name, res, "")
		return nil, fmt.Errorf("received error from Discord")
	}

	bodyBytes, err := io.ReadAll(res.Body)
	if err != nil {
		panic(err)
	}

	var msg GuildMember
	err = json.Unmarshal(bodyBytes, &msg)
	if err != nil {
		return nil, fmt.Errorf("failed to unmarshal Discord message: %w", err)
	}

	return &msg, nil
}

type MentionType string

const (
	MentionTypeUsers    MentionType = "users"
	MentionTypeRoles                = "roles"
	MentionTypeEveryone             = "everyone"
)

type MessageAllowedMentions struct {
	Parse []MentionType `json:"parse"`
}

const (
	FlagSuppressEmbeds int = 1 << 2
)

// https://discord.com/developers/docs/resources/channel#create-message
type CreateMessageRequest struct {
	Content         string                  `json:"content"`
	Flags           int                     `json:"flags,omitempty"`
	AllowedMentions *MessageAllowedMentions `json:"allowed_mentions,omitempty"`
}

func (r *Rest) CreateMessage(ctx context.Context, channelID string, payload CreateMessageRequest, files ...FileUpload) (*Message, error) {
	const name = "Create Message"

	payloadJSON := utils.Must1(json.Marshal(payload))
	contentType, body := makeNewMessageBody(string(payloadJSON), files)

	path := fmt.Sprintf("/channels/%s/messages", channelID)
	res, err := r.doWithRateLimiting(ctx, name, func(ctx context.Context) *http.Request {
		req := r.makeRequest(ctx, http.MethodPost, path, body)
		req.Header.Add("Content-Type", contentType)
		return req
	})
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()

	if res.StatusCode >= 400 {
		r.logErrorResponse(ctx, name, res, "")
		return nil, fmt.Errorf("received error from Discord")
	}

	// Maybe in the future we could more nicely handle errors like "bad channel",
	// but honestly what are the odds that we mess that up...

	bodyBytes, err := io.ReadAll(res.Body)
	if err != nil {
		panic(err)
	}

	var msg Message
	err = json.Unmarshal(bodyBytes, &msg)
	if err != nil {
		return nil, fmt.Errorf("failed to unmarshal Discord message: %w", err)
	}

	return &msg, nil
}

// https://discord.com/developers/docs/resources/channel#edit-message
type EditMessageRequest struct {
	Content         string                  `json:"content"`
	Flags           int                     `json:"flags,omitempty"`
	AllowedMentions *MessageAllowedMentions `json:"allowed_mentions,omitempty"`
}

func (r *Rest) EditMessage(ctx context.Context, channelID string, messageID string, payload EditMessageRequest, files ...FileUpload) (*Message, error) {
	const name = "Edit Message"

	payloadJSON := utils.Must1(json.Marshal(payload))
	contentType, body := makeNewMessageBody(string(payloadJSON), files)

	path := fmt.Sprintf("/channels/%s/messages/%s", channelID, messageID)
	res, err := r.doWithRateLimiting(ctx, name, func(ctx context.Context) *http.Request {
		req := r.makeRequest(ctx, http.MethodPatch, path, body)
		req.Header.Add("Content-Type", contentType)
		return req
	})
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()

	if res.StatusCode >= 400 {
		r.logErrorResponse(ctx, name, res, "")
		return nil, fmt.Errorf("received error from Discord")
	}

	// Maybe in the future we could more nicely handle errors like "bad channel",
	// but honestly what are the odds that we mess that up...

	bodyBytes, err := io.ReadAll(res.Body)
	if err != nil {
		panic(err)
	}

	var msg Message
	err = json.Unmarshal(bodyBytes, &msg)
	if err != nil {
		return nil, fmt.Errorf("failed to unmarshal Discord message: %w", err)
	}

	return &msg, nil
}

func (r *Rest) DeleteMessage(ctx context.Context, channelID string, messageID string) error {
	const name = "Delete Message"

	path := fmt.Sprintf("/channels/%s/messages/%s", channelID, messageID)
	res, err := r.doWithRateLimiting(ctx, name, func(ctx context.Context) *http.Request {
		return r.makeRequest(ctx, http.MethodDelete, path, nil)
	})
	if err != nil {
		return err
	}
	defer res.Body.Close()

	if res.StatusCode != http.StatusNoContent {
		r.logErrorResponse(ctx, name, res, "")
		return fmt.Errorf("got unexpected status code when deleting message")
	}

	return nil
}

func (r *Rest) CreateDM(ctx context.Context, recipientID string) (*Channel, error) {
	const name = "Create DM"

	path := "/users/@me/channels"
	body := []byte(fmt.Sprintf(`{"recipient_id":"%s"}`, recipientID))
	res, err := r.doWithRateLimiting(ctx, name, func(ctx context.Context) *http.Request {
		req := r.makeRequest(ctx, http.MethodPost, path, body)
		req.Header.Add("Content-Type", "application/json")
		return req
	})
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()

	if res.StatusCode >= 400 {
		r.logErrorResponse(ctx, name, res, "")
		return nil, fmt.Errorf("received error from Discord")
	}

	bodyBytes, err := io.ReadAll(res.Body)
	if err != nil {
		panic(err)
	}

	var channel Channel
	err = json.Unmarshal(bodyBytes, &channel)
	if err != nil {
		return nil, fmt.Errorf("failed to unmarshal Discord channel: %w", err)
	}

	return &channel, nil
}

func (r *Rest) AddGuildMemberRole(ctx context.Context, guildID, userID, roleID string) error {
	const name = "Add Guild Member Role"

	path := fmt.Sprintf("/guilds/%s/members/%s/roles/%s", guildID, userID, roleID)
	res, err := r.doWithRateLimiting(ctx, name, func(ctx context.Context) *http.Request {
		return r.makeRequest(ctx, http.MethodPut, path, nil)
	})
	if err != nil {
		return err
	}
	defer res.Body.Close()

	if res.StatusCode != http.StatusNoContent {
		r.logErrorResponse(ctx, name, res, "")
		return fmt.Errorf("got unexpected status code when adding role")
	}

	return nil
}

func (r *Rest) RemoveGuildMemberRole(ctx context.Context, guildID, userID, roleID string) error {
	const name = "Remove Guild Member Role"

	path := fmt.Sprintf("/guilds/%s/members/%s/roles/%s", guildID, userID, roleID)
	res, err := r.doWithRateLimiting(ctx, name, func(ctx context.Context) *http.Request {
		return r.makeRequest(ctx, http.MethodDelete, path, nil)
	})
	if err != nil {
		return err
	}
	defer res.Body.Close()

	if res.StatusCode != http.StatusNoContent {
		r.logErrorResponse(ctx, name, res, "")
		return fmt.Errorf("got unexpected status code when removing role")
	}

	return nil
}

func (r *Rest) GetChannelMessage(ctx context.Context, channelID, messageID string) (*Message, error) {
	const name = "Get Channel Message"

	path := fmt.Sprintf("/channels/%s/messages/%s", channelID, messageID)
	res, err := r.doWithRateLimiting(ctx, name, func(ctx context.Context) *http.Request {
		return r.makeRequest(ctx, http.MethodGet, path, nil)
	})
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()

	if res.StatusCode == http.StatusNotFound {
		return nil, NotFound
	} else if res.StatusCode >= 400 {
		r.logErrorResponse(ctx, name, res, "")
		return nil, fmt.Errorf("received error from Discord")
	}

	bodyBytes, err := io.ReadAll(res.Body)
	if err != nil {
		panic(err)
	}

	var msg Message
	err = json.Unmarshal(bodyBytes, &msg)
	if err != nil {
		return nil, fmt.Errorf("failed to unmarshal Discord message: %w", err)
	}

	return &msg, nil
}

type GetChannelMessagesInput struct {
	Around string
	Before string
	After  string
	Limit  int
}

func (r *Rest) GetChannelMessages(ctx context.Context, channelID string, in GetChannelMessagesInput) ([]Message, error) {
	const name = "Get Channel Messages"

	path := fmt.Sprintf("/channels/%s/messages", channelID)
	res, err := r.doWithRateLimiting(ctx, name, func(ctx context.Context) *http.Request {
		req := r.makeRequest(ctx, http.MethodGet, path, nil)
		q := req.URL.Query()
		if in.Around != "" {
			q.Add("around", in.Around)
		}
		if in.Before != "" {
			q.Add("before", in.Before)
		}
		if in.After != "" {
			q.Add("after", in.After)
		}
		if in.Limit != 0 {
			q.Add("limit", strconv.Itoa(in.Limit))
		}
		req.URL.RawQuery = q.Encode()

		return req
	})
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()

	if res.StatusCode >= 400 {
		r.logErrorResponse(ctx, name, res, "")
		return nil, fmt.Errorf("received error from Discord")
	}

	bodyBytes, err := io.ReadAll(res.Body)
	if err != nil {
		panic(err)
	}

	var msgs []Message
	err = json.Unmarshal(bodyBytes, &msgs)
	if err != nil {
		return nil, fmt.Errorf("failed to unmarshal Discord message: %w", err)
	}

	return msgs, nil
}

// See https://discord.com/developers/docs/interactions/application-commands#create-guild-application-command-json-params
type CreateGuildApplicationCommandRequest struct {
	Name              string                     `json:"name"`               // 1-32 character name
	Description       string                     `json:"description"`        // 1-100 character description
	Options           []ApplicationCommandOption `json:"options"`            // the parameters for the command
	DefaultPermission *bool                      `json:"default_permission"` // whether the command is enabled by default when the app is added to a guild
	Type              ApplicationCommandType     `json:"type"`               // the type of command, defaults 1 if not set
}

// See https://discord.com/developers/docs/interactions/application-commands#create-guild-application-command
func (r *Rest) CreateGuildApplicationCommand(ctx context.Context, applicationID, guildID string, in CreateGuildApplicationCommandRequest) error {
	const name = "Create Guild Application Command"

	if in.Type == 0 {
		in.Type = ApplicationCommandTypeChatInput
	}

	path := fmt.Sprintf("/applications/%s/guilds/%s/commands", applicationID, guildID)
	res, err := r.doWithRateLimiting(ctx, name, func(ctx context.Context) *http.Request {
		req := r.makeRequest(ctx, http.MethodPost, path, []byte(utils.Must1(json.Marshal(in))))
		req.Header.Add("Content-Type", "application/json")
		return req
	})
	if err != nil {
		return err
	}
	defer res.Body.Close()

	if res.StatusCode >= 400 {
		r.logErrorResponse(ctx, name, res, "")
		return fmt.Errorf("received error from Discord")
	}

	return nil
}

func (r *Rest) CreateInteractionResponse(ctx context.Context, interactionID, interactionToken string, in InteractionResponse) error {
	const name = "Create Interaction Response"

	payloadJSON, err := json.Marshal(in)
	if err != nil {
		return fmt.Errorf("failed to marshal request body")
	}

	path := fmt.Sprintf("/interactions/%s/%s/callback", interactionID, interactionToken)
	res, err := r.doWithRateLimiting(ctx, name, func(ctx context.Context) *http.Request {
		req := r.makeRequest(ctx, http.MethodPost, path, []byte(payloadJSON))
		req.Header.Add("Content-Type", "application/json")
		return req
	})
	if err != nil {
		return err
	}
	defer res.Body.Close()

	if res.StatusCode >= 400 {
		r.logErrorResponse(ctx, name, res, "")
		return fmt.Errorf("received error from Discord")
	}

	return nil
}

// See https://discord.com/developers/docs/interactions/receiving-and-responding#edit-original-interaction-response
func (r *Rest) EditOriginalInteractionResponse(ctx context.Context, applicationID, interactionToken string, payloadJSON string, files ...FileUpload) (*Message, error) {
	const name = "Edit Original Interaction Response"

	contentType, body := makeNewMessageBody(payloadJSON, files)

	path := fmt.Sprintf("/webhooks/%s/%s/messages/@original", applicationID, interactionToken)
	res, err := r.doWithRateLimiting(ctx, name, func(ctx context.Context) *http.Request {
		req := r.makeRequest(ctx, http.MethodPatch, path, body)
		req.Header.Add("Content-Type", contentType)
		return req
	})
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()

	if res.StatusCode >= 400 {
		r.logErrorResponse(ctx, name, res, "")
		return nil, fmt.Errorf("received error from Discord")
	}

	bodyBytes, err := io.ReadAll(res.Body)
	if err != nil {
		panic(err)
	}

	var msg Message
	err = json.Unmarshal(bodyBytes, &msg)
	if err != nil {
		return nil, fmt.Errorf("failed to unmarshal Discord message: %w", err)
	}

	return &msg, nil
}

type FileUpload struct {
	Name string
	Data []byte
}

func makeNewMessageBody(payloadJSON string, files []FileUpload) (contentType string, body []byte) {
	if len(files) == 0 {
		contentType = "application/json"
		body = []byte(payloadJSON)
	} else {
		var bodyBuffer bytes.Buffer
		w := multipart.NewWriter(&bodyBuffer)
		contentType = w.FormDataContentType()

		jsonHeader := textproto.MIMEHeader{}
		jsonHeader.Set("Content-Disposition", `form-data; name="payload_json"`)
		jsonHeader.Set("Content-Type", "application/json")
		jsonWriter, _ := w.CreatePart(jsonHeader)
		jsonWriter.Write([]byte(payloadJSON))

		for _, f := range files {
			formFile, _ := w.CreateFormFile("file", f.Name)
			formFile.Write(f.Data)
		}

		w.Close()

		body = bodyBuffer.Bytes()
	}

	if len(body) == 0 {
		panic("somehow we generated an empty body for Discord")
	}

	return
}

func (r *Rest) logErrorResponse(ctx context.Context, name string, res *http.Response, msg string) {
	dump, err := httputil.DumpResponse(res, true)
	if err != nil {
		panic(err)
	}

	r.Logger().ErrorContext(ctx, "msg", "name", name)
	fmt.Println(string(dump))
}
