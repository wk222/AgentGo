package channels

// ChannelStatus describes IM channel integration state (PyBot channels config mirror).
type ChannelStatus struct {
	Kind    string `json:"kind"`
	Enabled bool   `json:"enabled"`
	Label   string `json:"label"`
	Note    string `json:"note"`
}

func DefaultStatuses() []ChannelStatus {
	return []ChannelStatus{
		{Kind: "wechat", Enabled: false, Label: "微信", Note: "需配置 token / app_id / app_secret（PyBot channels.wechat）"},
		{Kind: "wecom", Enabled: false, Label: "企业微信", Note: "需配置 corp_id / agent_id / secret（PyBot channels.wecom）"},
	}
}

// GatewayStatus mirrors PyBot gateway.http endpoints summary.
type GatewayStatus struct {
	Enabled   bool     `json:"enabled"`
	Endpoints []string `json:"endpoints"`
	Note      string   `json:"note"`
}

func DefaultGateway() GatewayStatus {
	return GatewayStatus{
		Enabled: false,
		Endpoints: []string{
			"POST /v1/chat/completions (OpenAI SSE/JSON)",
			"GET /v1/models",
			"POST /api/v1/chat/stream (SSE)",
			"GET /api/v1/tasks/{id}/events (SSE)",
			"GET/PUT /api/v1/workflows/{id}/flowgram",
		},
		Note: "设置环境变量 AGENTGO_GATEWAY_PORT=8787 启动 HTTP SSE 网关",
	}
}
