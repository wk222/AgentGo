package tools

import (
	"context"
	"path/filepath"
	"strings"

	"agentgo/internal/channels"

	"github.com/cloudwego/eino/components/tool/utils"
)

type channelManagerInput struct {
	Action     string            `json:"action" jsonschema:"required,description=Action: 'list' (list configured channel adapters)\\, 'upsert' (add or update a channel adapter: feishu/dingtalk/wecom/telegram/webhook)\\, 'remove' (delete a channel adapter)\\, 'send' (send or dry-run a message through a channel)\\, or 'parse_inbound' (test inbound webhook payload normalization)"`
	Name       string            `json:"name,omitempty" jsonschema:"description=Channel adapter identifier (e.g. 'ops_feishu'\\, 'sec_dingtalk')"`
	Kind       string            `json:"kind,omitempty" jsonschema:"description=Adapter protocol for action=upsert: 'webhook'\\, 'feishu'\\, 'dingtalk'\\, 'wecom'\\, or 'telegram'"`
	Label      string            `json:"label,omitempty" jsonschema:"description=Human-friendly display label"`
	WebhookURL string            `json:"webhook_url,omitempty" jsonschema:"description=Target webhook URL for feishu/dingtalk/wecom/webhook"`
	Secret     string            `json:"secret,omitempty" jsonschema:"description=Optional HMAC signing secret (Feishu v2 sign\\, DingTalk sign\\, or X-AgentGo-Signature)"`
	BotToken   string            `json:"bot_token,omitempty" jsonschema:"description=Telegram bot token for kind=telegram"`
	ChatID     string            `json:"chat_id,omitempty" jsonschema:"description=Telegram chat_id for kind=telegram"`
	Headers    map[string]string `json:"headers,omitempty" jsonschema:"description=Extra HTTP headers for outbound webhook calls"`
	Note       string            `json:"note,omitempty" jsonschema:"description=Usage notes for this channel"`
	Title      string            `json:"title,omitempty" jsonschema:"description=Message title for action=send"`
	Text       string            `json:"text,omitempty" jsonschema:"description=Message body for action=send, or raw inbound JSON for action=parse_inbound"`
	DryRun     bool              `json:"dry_run,omitempty" jsonschema:"description=For action=send: build and sign the envelope without sending over the network"`
}

type channelManagerOutput struct {
	Success  bool                     `json:"success"`
	Action   string                   `json:"action"`
	Channel  *channels.ChannelSpec    `json:"channel,omitempty"`
	Channels []channels.ChannelSpec   `json:"channels,omitempty"`
	Statuses []channels.ChannelStatus `json:"statuses,omitempty"`
	Send     *channels.SendResult     `json:"send,omitempty"`
	Inbound  *channels.InboundMessage `json:"inbound,omitempty"`
	Message  string                   `json:"message,omitempty"`
}

// RegisterChannelManagerTool registers `manage_channel` so the agent can add,
// configure, test and dispatch to external IM/webhook channels (Feishu, DingTalk,
// WeCom, Telegram, generic Webhooks).
func RegisterChannelManagerTool(r *Registry, workspaceRoot string) error {
	cfgPath := ""
	if strings.TrimSpace(workspaceRoot) != "" {
		cfgPath = filepath.Join(workspaceRoot, ".agentgo", "channels.json")
	}
	hub := channels.NewHub(cfgPath)

	t, err := utils.InferTool("manage_channel",
		"Modular channel adapter manager: register/update/remove external notification & webhook channels (feishu, dingtalk, wecom, telegram, webhook with HMAC signing), send messages (or dry_run to inspect signed payloads), and normalize inbound webhook events.",
		func(ctx context.Context, in channelManagerInput) (channelManagerOutput, error) {
			_ = hub.Load()
			action := strings.ToLower(strings.TrimSpace(in.Action))
			switch action {
			case "list":
				return channelManagerOutput{
					Success:  true,
					Action:   "list",
					Channels: hub.List(),
					Statuses: hub.Statuses(),
				}, nil

			case "upsert":
				spec, err := hub.Upsert(channels.ChannelSpec{
					Name:       in.Name,
					Kind:       in.Kind,
					Label:      in.Label,
					Enabled:    true,
					WebhookURL: in.WebhookURL,
					Secret:     in.Secret,
					BotToken:   in.BotToken,
					ChatID:     in.ChatID,
					Headers:    in.Headers,
					Note:       in.Note,
				})
				if err != nil {
					return channelManagerOutput{Success: false, Action: "upsert", Message: err.Error()}, nil
				}
				return channelManagerOutput{
					Success: true,
					Action:  "upsert",
					Channel: &spec,
					Message: "channel adapter saved and enabled",
				}, nil

			case "remove":
				ok, err := hub.Remove(in.Name)
				if err != nil {
					return channelManagerOutput{Success: false, Action: "remove", Message: err.Error()}, nil
				}
				msg := "channel removed"
				if !ok {
					msg = "channel not found"
				}
				return channelManagerOutput{Success: ok, Action: "remove", Message: msg}, nil

			case "send":
				res, err := hub.Send(ctx, in.Name, in.Title, in.Text, in.DryRun)
				if err != nil {
					return channelManagerOutput{Success: false, Action: "send", Message: err.Error()}, nil
				}
				return channelManagerOutput{
					Success: true,
					Action:  "send",
					Send:    res,
				}, nil

			case "parse_inbound":
				msg, err := hub.ParseInbound(in.Name, []byte(in.Text))
				if err != nil {
					return channelManagerOutput{Success: false, Action: "parse_inbound", Message: err.Error()}, nil
				}
				return channelManagerOutput{
					Success: true,
					Action:  "parse_inbound",
					Inbound: msg,
				}, nil

			default:
				return channelManagerOutput{
					Success: false,
					Action:  in.Action,
					Message: "unknown action (want list|upsert|remove|send|parse_inbound)",
				}, nil
			}
		})
	if err != nil {
		return err
	}
	r.AddTool(t)
	return nil
}
