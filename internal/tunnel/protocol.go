package tunnel

// TunnelRequest 是服务器发往设备的 HTTP 请求封装。
type TunnelRequest struct {
	Type       string            `json:"type"`
	ID         string            `json:"id"`
	Method     string            `json:"method"`
	Path       string            `json:"path"`
	Headers    map[string]string `json:"headers,omitempty"`
	BodyBase64 string            `json:"body_base64,omitempty"`
}

// TunnelResponse 是设备发往服务器的响应封装。
// Type 取值：
//   - "response"：普通一次性响应，含 status_code、headers、body_base64
//   - "response_start"：流式响应开始，含 id、status_code、headers，无 body
//   - "response_chunk"：流式 body 块，含 id、body_base64
//   - "response_end"：流式结束，仅含 id（SSE 长连接可能永不发送）
type TunnelResponse struct {
	Type       string            `json:"type"`
	ID         string            `json:"id"`
	StatusCode int               `json:"status_code"`
	Headers    map[string]string `json:"headers,omitempty"`
	BodyBase64 string            `json:"body_base64,omitempty"`
}
