# 隧道客户端（设备端）实现说明

本文档面向**设备端（隧道客户端）**的开发者：如何实现与 [tunnel-server](REMOTE_ACCESS_TUNNEL_SERVER_SPEC.md) 对接的客户端，使设备在 4G/NAT 后能通过出站 WebSocket 提供「本地 HTTP 服务」的远程访问能力。

---

## 1. 角色与整体思路

- **设备端**：运行在设备上（嵌入式、工控机、树莓派等），主动用 WebSocket 连接中心服务器，并注册本机的 `device_id`。
- **数据流**：用户访问 `https://server/device/{device_id}/...` → 中心把请求通过 WebSocket 发给设备 → 设备在本地请求 `http://127.0.0.1:8080 + path`（或你配置的 base URL）→ 把本地响应通过 WebSocket 发回中心 → 中心再返回给用户。
- **实现要点**：  
  1. 建立并维持一条到中心的 WebSocket 连接（带 `device_id`、`token`）。  
  2. 单线程/单协程读取 WebSocket，收到 `type: "request"` 的 JSON 即视为一条隧道请求。  
  3. 对每条请求：向本地 HTTP 服务发请求，再把响应封装成协议规定的 JSON 通过 WebSocket 发回；若为 SSE 类请求则走流式（`response_start` → `response_chunk` → `response_end`）。  
  4. 写 WebSocket 必须串行化（多请求并发时用锁或写队列），避免并发写导致帧交错或库报错。

---

## 2. 连接与鉴权

### 2.1 连接 URL

- **端点**：`wss://your-server.com/tunnel/device`（生产用 TLS；本地调试可为 `ws://host:port/tunnel/device`）。
- **鉴权**：在 URL Query 中携带：
  - `device_id`：设备唯一标识，与中心配置及用户访问路径一致（如 `https://server/device/my-device/` 中的 `my-device`）。
  - `token`：鉴权令牌，与中心侧为该设备配置的 token 一致（若中心未开启鉴权可省略）。

示例：

```
wss://your-server.com/tunnel/device?device_id=my-device&token=your-secret-token
```

### 2.2 连接策略

- **单连接**：每个进程/设备只维持一条到中心的 WebSocket 连接；中心对同一 `device_id` 会「新连踢旧连」。
- **断线重连**：连接断开后应带相同 `device_id` 与 `token` 重连；重连成功后即恢复能力，无需额外注册。
- **心跳**：中心会发 WebSocket Ping，客户端按库默认或配置回 Pong 即可，一般无需自己发业务层心跳。

---

## 3. 协议：收到的消息（服务器 → 设备）

设备只接收一种业务消息：**HTTP 请求封装（TunnelRequest）**，WebSocket **Text 帧**，UTF-8 JSON。

### 3.1 TunnelRequest 结构

```json
{
  "type": "request",
  "id": "<唯一请求 ID>",
  "method": "GET",
  "path": "/api/status",
  "headers": { "Accept": "application/json" },
  "body_base64": ""
}
```

| 字段 | 类型 | 说明 |
|------|------|------|
| `type` | string | 固定 `"request"` |
| `id` | string | 请求唯一 ID，回复时必须原样带回，用于匹配请求-响应 |
| `method` | string | HTTP 方法：GET、POST、PUT、DELETE 等 |
| `path` | string | 路径（含 query），如 `/api/status?foo=1`。设备请求本地时 = 本地 base URL + path |
| `headers` | object | 请求头，key-value；可选，需过滤 hop-by-hop（见下） |
| `body_base64` | string | 请求体 Base64；无体为空字符串 |

- **本地请求 URL**：若本地服务 base 为 `http://127.0.0.1:8080`，则请求地址为 `http://127.0.0.1:8080` + `path`（path 以 `/` 开头，直接拼接即可；注意 base 末尾不要重复 `/`）。

---

## 4. 协议：发出的消息（设备 → 服务器）

设备向服务器发送的也为 WebSocket **Text 帧**，UTF-8 JSON。分两种模式：**普通一次性响应** 与 **流式响应（SSE）**。

### 4.1 普通响应（绝大多数请求）

一条消息即可，`type` 为 `"response"`：

```json
{
  "type": "response",
  "id": "<对应请求的 id>",
  "status_code": 200,
  "headers": { "Content-Type": "application/json" },
  "body_base64": "<响应体 Base64>"
}
```

- `id` 必须与 TunnelRequest 的 `id` 一致。  
- 本地请求失败（超时、连接失败等）可返回 `status_code: 502`，body 可为空或简短错误信息。

### 4.2 流式响应（SSE / 长连接）

当**请求头**中 `Accept` 包含 `text/event-stream` 时，中心会按流式处理，设备端应走流式回复，顺序发送三类消息：

| type | 含义 | JSON 内容 |
|------|------|-----------|
| `response_start` | 响应开始 | `id`、`status_code`、`headers`，无 body |
| `response_chunk` | 一块 body | `id`、`body_base64`（可多次） |
| `response_end` | 流结束 | 仅 `id`（SSE 长连接可能永不结束，可不发或流关闭时再发） |

- 先发 `response_start`，再按需发多帧 `response_chunk`，最后可选发 `response_end`。  
- 同一 `id` 的帧顺序必须与本地响应顺序一致；写 WebSocket 时仍须串行化，避免与其他请求的响应帧交错。

---

## 5. 技术实现要点

### 5.1 读模型：单协程读、按请求分发

- **单协程**：只在一个 goroutine/线程里循环 `ReadMessage()`，不并发读同一连接。  
- 收到 Text 帧后解析 JSON，若 `type != "request"` 或 `id` 为空则忽略。  
- 对每条合法 TunnelRequest，**投递到业务处理**（如起 goroutine 或投到 channel），由业务层向本地 HTTP 发起请求并组包回写；读协程不阻塞在本地 HTTP 上。

### 5.2 写模型：串行化

- 多条隧道请求可能并发完成，但 **WebSocket 写必须串行**：同一连接上同时只允许一个写操作。  
- 实现方式二选一：  
  - **写锁**：所有发送 `response` / `response_start` / `response_chunk` / `response_end` 的地方在写前抢同一把锁。  
  - **单写协程**：业务层把要发送的 JSON 投到一个 channel，由单独一个协程从 channel 取并 `WriteMessage`。  
- 流式响应中多帧 `response_chunk` 之间也不得插入其他请求的帧，即同一连接上所有类型的响应帧都走同一串行写路径。

### 5.3 本地 HTTP 请求

- **URL**：`targetBase + path`（targetBase 如 `http://127.0.0.1:8080`，path 以 `/` 开头）。  
- **Method / Body**：与 TunnelRequest 的 `method`、`body_base64`（解码后）一致。  
- **Headers**：可把 TunnelRequest 的 `headers` 设到请求上，但需**过滤 hop-by-hop**（见下），并注意 `Host` 通常改为本地服务 host 或省略。  
- **超时**：建议为本地请求设置超时（如 30s），超时则回 502。

### 5.4 Header 过滤（请求与响应）

以下 header 不应转发到本地请求，也不应在响应封装里回传给中心（hop-by-hop 或由代理重写）：

- `Connection`, `Proxy-Connection`, `Keep-Alive`, `TE`, `Trailer`, `Transfer-Encoding`, `Upgrade`

响应里的 `Content-Length` 若由你组包写回，应按解码后的 body 长度重算，不要盲目透传。

### 5.5 流式响应的实现（SSE）

- 若请求头中 `Accept` 包含 `text/event-stream`：  
  - 向本地发 HTTP 请求后，先读状态码与响应头，立即发一条 `response_start`（id、status_code、headers）。  
  - 然后按块读 body（如 4KB～32KB），每块 Base64 后发一条 `response_chunk`（id、body_base64）。  
  - 读完后发一条 `response_end`（仅 id）；若本地是长连接不关闭，可只在流关闭或超时时发 `response_end`。  
- 所有上述发送都经过同一「写串行化」路径，保证帧顺序。

### 5.6 错误与边界

- 本地服务连接失败、超时、读错误：建议回 `type: "response"`, `status_code: 502`，`id` 一致。  
- 收到非法 JSON 或缺少必填字段：可忽略该帧，不回复（中心会超时返回 504）。  
- Body 过大：可对请求/响应 body 做大小限制，超限回 413/502，避免内存与 WebSocket 帧过大。

---

## 6. 与中心协议的对应关系

| 项目 | 说明 |
|------|------|
| 连接 | `wss://host/tunnel/device?device_id=xxx&token=yyy`，Text 帧，UTF-8 JSON |
| 收 | 仅处理 `type: "request"`，含 `id`, `method`, `path`, `headers`, `body_base64` |
| 发（普通） | 单条 `type: "response"`，同 `id`，`status_code`, `headers`, `body_base64` |
| 发（流式） | `response_start` → 若干 `response_chunk` → 可选 `response_end`，同 `id` |
| 流式触发 | 请求头 `Accept` 包含 `text/event-stream` 时走流式，否则走普通 |

中心侧协议与实现约定见 [REMOTE_ACCESS_TUNNEL_SERVER_SPEC.md](REMOTE_ACCESS_TUNNEL_SERVER_SPEC.md)。

---

## 7. 参考实现

本仓库中的 **`cmd/mock-device`** 是一个最简隧道客户端示例：

- 从环境变量读取 `SERVER_WS`、`DEVICE_ID`、`TOKEN`、`TARGET_BASE`，拼出带 query 的 WebSocket URL 并连接。  
- 单协程读 WebSocket，解析 TunnelRequest，对每条请求起 goroutine 调本地 HTTP，组 TunnelResponse 后写回。  
- **注意**：当前 mock-device 未做**写串行化**（多请求同时完成时并发 `WriteMessage` 可能有问题），也未实现**流式响应**；仅适合本地联调。正式实现请按上文加上写锁/写队列与 SSE 流式逻辑。

运行示例见 [LOCAL_HTTP_QUICKSTART.md](LOCAL_HTTP_QUICKSTART.md)。
