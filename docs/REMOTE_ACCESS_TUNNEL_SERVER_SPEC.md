# 隧道中心协议与实现说明

本文档描述**隧道中心（tunnel-server）**的功能与协议，用于与设备端（隧道客户端）对接。本仓库提供隧道中心与隧道客户端（tunnel-client）；若自研客户端或服务端，按本文档即可与兼容协议的另一端对接。

> 目标效果：用户在浏览器打开 `https://your-server.com/device/RTK001/` 即可看到设备 RTK001 的配置页，无需与设备在同一局域网。

---

## 1. 目标与角色

- **设备端**：在 4G/NAT 后，主动用 WebSocket 连接中心，将「用户通过中心发来的 HTTP 请求」在设备本地转发到 `http://127.0.0.1:8080`（或配置的 base URL），并把响应通过 WebSocket 发回。本仓库提供 **tunnel-client** 作为通用客户端。
- **隧道中心**：
  1. 提供 WebSocket 隧道接入：接受设备连接，按 `device_id` 注册并维护连接表。
  2. 提供 HTTP 反向代理：用户访问 `https://your-server.com/device/{device_id}/...` 时，将请求通过对应设备的 WebSocket 隧道转发，并把设备返回的响应原样返回给用户。

---

## 2. 隧道接入（WebSocket）

### 2.1 端点与连接方式

- **URL**：`wss://your-server.com/tunnel/device`（或等价 path，如 `/tunnel/device`）。
- **鉴权与注册**：设备在连接时通过 **URL Query 参数** 传递：
  - `device_id`：设备唯一标识（如 `RTK001`）
  - `token`：鉴权令牌（由部署方在设备与服务器侧配置一致；可选，若不校验则可不传）
- 示例连接 URL：`wss://your-server.com/tunnel/device?device_id=RTK001&token=your-device-secret-token`

### 2.2 服务端行为

1. **握手与鉴权**
  - 在 WebSocket 握手阶段（或连接建立后首帧）从 URL query 读取 `device_id`、`token`。
  - 若需鉴权：校验 `token`（如与预存/数据库中的该 device 的 token 一致）；校验失败则拒绝握手并返回 4xx，或建立后立刻关闭连接。
  - 若不需要鉴权：可忽略 `token`，仅用 `device_id` 注册。

2. **连接表**
  - 以 `device_id` 为 key，保存该设备当前对应的 WebSocket 连接与其状态。
  - **单连接策略**：同一 `device_id` 的新连接建立后，必须关闭旧连接，再写入新连接，避免一机多连导致请求发送到错误连接。

3. **心跳与超时**
  - 建议对每条隧道连接做 Ping/Pong 或读超时；超时未活动则关闭连接并从表中移除该 `device_id`，避免僵死连接占用资源。

4. **消息类型**
  - 设备连接后，除必要的 WebSocket 控制帧外，设备只响应服务器发来的「HTTP 请求」消息。
  - 服务器向设备发送「HTTP 请求」封装（JSON），设备会回复「HTTP 响应」封装（JSON）。
  - 所有业务帧均为 **WebSocket Text 帧**，内容为 **UTF-8 JSON**。

---

## 3. 请求/响应封装协议（与设备端严格一致）

设备端与服务器之间通过 **JSON over WebSocket（Text 帧）** 交换「用户 HTTP 请求」与「设备 HTTP 响应」。字段名与类型必须严格一致。

### 3.1 服务器 → 设备：HTTP 请求封装（TunnelRequest）

服务器发给设备的单条 WebSocket Text 消息为如下 JSON：

```json
{
  "type": "request",
  "id": "<唯一请求 ID，UUID 或其它字符串>",
  "method": "GET",
  "path": "/api/gnss",
  "headers": {
    "Host": "your-server.com",
    "Accept": "text/html,application/json"
  },
  "body_base64": ""
}
```

| 字段 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `type` | string | 是 | 固定为 `"request"` |
| `id` | string | 是 | 本请求唯一标识；设备回复时必须原样带回，用于匹配请求-响应 |
| `method` | string | 是 | HTTP 方法：`GET`、`POST`、`PUT`、`DELETE` 等 |
| `path` | string | 是 | 请求路径（建议包含 query：`/path?x=1`），设备将访问 `http://127.0.0.1:8080` + `path` |
| `headers` | object | 否 | 请求头，key-value 字符串。可传用户请求的部分或全部 Header（见“实现约定”） |
| `body_base64` | string | 否 | 请求体 Base64；无体为 `""` |

> 路径建议：用户访问 `https://server/device/RTK001/api/gnss` 时，`path` 填去掉 `/device/{device_id}` 前缀后的路径：`/api/gnss`（如有 query 也应保留）。

### 3.2 设备 → 服务器：HTTP 响应封装（TunnelResponse）

设备回复的单条 WebSocket Text 消息为如下 JSON：

```json
{
  "type": "response",
  "id": "<对应请求的 id>",
  "status_code": 200,
  "headers": {
    "Content-Type": "application/json"
  },
  "body_base64": "<响应体的 Base64>"
}
```

| 字段 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `type` | string | 是 | 固定为 `"response"` |
| `id` | string | 是 | 对应 TunnelRequest 的 `id` |
| `status_code` | int | 是 | HTTP 状态码（如 200、404、502） |
| `headers` | object | 否 | 响应头，key-value 字符串（见“实现约定”） |
| `body_base64` | string | 否 | 响应体 Base64；无体可为 `""` |

### 3.3 设备 → 服务器：流式响应（SSE / 长连接）

对 **SSE 或长连接**（请求头 `Accept: text/event-stream`），设备端不会一次性返回一条 `type: "response"`，而是依次发送以下三类 WebSocket 文本帧（JSON）：

| type | 说明 |
|------|------|
| `response_start` | 响应开始：`id`、`status_code`、`headers`，无 body。 |
| `response_chunk` | body 的一块：`id`、`body_base64`。可多次。 |
| `response_end` | 响应结束：仅 `id`。SSE 长连接可能永不发送，只有设备端流关闭时才发。 |

**服务端处理**（对同一 `id` 的请求）：

1. **收到 `response_start`**  
   - 根据 `id` 找到对应的用户 HTTP 的 `ResponseWriter`。  
   - 立即写状态码与响应头，并 **Flush**，让浏览器尽快收到头，避免超时。

2. **收到 `response_chunk`**  
   - 根据 `id` 找到同一 `ResponseWriter`。  
   - 将 `body_base64` 解码后写入 body，并 **再次 Flush**，使 SSE 等流式数据及时到达浏览器。

3. **收到 `response_end`**  
   - 标记该 `id` 的流结束，清理该请求的上下文。SSE 长连接可能永远收不到 `response_end`，以用户断开或隧道断开为结束即可。

4. **超时**  
   - 对「等待 response_start」可设较短超时（如 10s）；一旦收到 `response_start`，不再用短超时主动断开，否则会断掉 SSE。

**如何区分普通响应与流式响应**
- 服务端根据请求头 `Accept` 是否包含 `text/event-stream` 决定：若包含则按流式处理（等 response_start → response_chunk → 可选 response_end），否则按普通单条 `response` 等待。
- 设备端对这类请求走流式回复；其余走普通单条 `response`。

---

## 4. HTTP 反向代理（用户访问设备）

### 4.1 路由与路径约定

- **路由模式**：`https://your-server.com/device/{device_id}/` 以及子路径，例如：
  - `https://your-server.com/device/RTK001/`
  - `https://your-server.com/device/RTK001/api/gnss`
  - `https://your-server.com/device/RTK001/settings/advanced`
- `device_id` 为路径中的一段，与设备连接时提交的 `device_id` 一致。

### 4.2 处理逻辑（每次用户请求）

1. **解析**
  - 从请求 URL 中取出 `device_id`（路径中的第二段）和设备路径（去掉前缀 `/device/{device_id}` 后的部分）。
  - 示例：
    - `GET https://server/device/RTK001/api/gnss?x=1` → `device_id=RTK001`，设备路径=`/api/gnss?x=1`
    - `GET https://server/device/RTK001/` → 设备路径=`/`（或按约定映射到 `/index.html`）

2. **查隧道**
  - 按 `device_id` 查 WebSocket 连接表。
  - 若设备未连接：返回 **503 Service Unavailable**（可选 body：`{"error":"device offline"}`）。

3. **封装请求**
  - 生成唯一请求 `id`（UUID 或其它字符串）。
  - 构造 TunnelRequest JSON：
    - `type`: `"request"`
    - `id`: 上一步 id
    - `method`: 用户请求 Method
    - `path`: 设备路径（含 query）
    - `headers`: 复制/过滤/重写后的 Header（见“实现约定”）
    - `body_base64`: 用户请求体 Base64，无体 `""`
  - 通过该设备的 WebSocket 连接发送一条 Text 帧（JSON 字符串）。

4. **等待响应（两种模式）**
  - **普通请求**：等待设备回一条 `type === "response"` 的 TunnelResponse，通过 `id` 匹配。建议超时（如 30s）；超时返回 **504 Gateway Timeout**。
  - **流式请求**：当请求头含 `Accept: text/event-stream` 时，按流式处理：先等 `response_start`（建议仅对「等 start」设短超时，如 10s），收到后立即写状态码与头并 Flush；再按序收 `response_chunk`，每块解码后写回并 Flush；可选收 `response_end` 后结束。流式过程中不要用短超时断开，否则会断掉 SSE。
  - 等待过程中连接断开：返回 **503 Service Unavailable**。

5. **回写用户**
  - **普通**：解析 TunnelResponse（`type === "response"` 且 `id` 匹配），用 `status_code`、`headers`、解码后的 `body_base64` 写回。
  - **流式**：见 3.3 节，按 response_start → response_chunk → response_end 写回并逐次 Flush。
  - 若 JSON 格式错误或字段缺失：返回 **502 Bad Gateway**。

### 4.3 并发与顺序

- 同一设备隧道连接可能同时承载多条用户请求（多 tab、多资源）。
- 设备端会并发处理多条 TunnelRequest，并按 `id` 回写 TunnelResponse。
- 服务器必须为每个用户请求维护等待上下文（如 `map[id]chan`），并在收到响应后按 `id` 唤醒对应请求处理流程。

---

## 5. 实现约定（强烈建议，避免踩坑）

### 5.1 WebSocket 并发读写模型（必遵循）

- **单协程读**：每个设备连接只能有一个读循环持续读取来自设备的消息，解析为 TunnelResponse 后按 `id` 分发给等待者。
- **写串行化**：向同一 WebSocket 连接写入必须串行（写锁或写队列）。禁止并发写导致帧交错或库报错。

### 5.2 Header 处理与过滤（必遵循）

- **过滤 hop-by-hop headers**（请求与响应都要处理）：
  - `Connection`、`Proxy-Connection`、`Keep-Alive`、`TE`、`Trailer`、`Transfer-Encoding`、`Upgrade`
- **`Content-Length`**：
  - 不要盲目透传；如果 body 解码后由框架写回，建议让框架自动设置长度或显式重算。
- **多值 Header（重点）**：
  - HTTP Header 允许同名多值，但协议 `headers` 为 `object(map[string]string)` 会丢失多值。
  - 若保持协议不变，必须明确策略：例如“仅取第一值”，并且**不允许用逗号合并 `Set-Cookie`**（会导致浏览器解析异常）。
  - 如果需要支持 session/cookie，建议后续升级协议以支持多值（如 `map[string][]string`），或在实现中对 `Set-Cookie` 做特殊通道（不建议临时 hack，优先协议升级）。

### 5.3 Path 与 Query（建议明确）

- `TunnelRequest.path` 建议包含 query（例如 `/api/gnss?x=1`），以保持与用户请求一致。
- `path` 不包含 scheme/host，仅包含以 `/` 开头的 path 与可选 query。

### 5.4 超时与资源清理（必遵循）

- **每请求超时**：等待响应超时后，必须清理 `pending[id]`，避免内存泄漏。
- **断连清理**：设备连接断开时，必须移除连接表条目，并让所有等待中的请求失败返回（通常 503）。

### 5.5 Body 大小限制（建议）

- Base64 会膨胀约 33%，且 JSON 帧会额外开销。
- 建议对请求/响应 body 设置上限（可配置），超限返回 413/502，避免大文件导致内存放大或 WebSocket 消息超限。

---

## 6. 安全与生产建议

- **TLS**：生产环境隧道与用户访问均应使用 TLS（`wss://`、`https://`）。
- **设备鉴权**：建议校验 `token`，拒绝未授权设备注册。
- **用户鉴权（可选）**：对 `/device/...` 做登录或 API Key 校验，避免任意人访问设备配置页。
- **限流与日志**：对隧道连接数、每设备请求 QPS 做限流；对连接/断开、用户访问做审计日志（注意不要记录 token/敏感信息）。

---

## 7. 协议小结（对照表）

| 项目 | 说明 |
|------|------|
| 设备建连 | `wss://host/tunnel/device?device_id=xxx&token=yyy`，Text 帧，UTF-8 JSON |
| 服务器 → 设备 | 单帧 JSON：`type:"request"`, `id`, `method`, `path`, `headers`, `body_base64` |
| 设备 → 服务器（普通） | 单帧 JSON：`type:"response"`, `id`, `status_code`, `headers`, `body_base64` |
| 设备 → 服务器（流式） | 多帧：`response_start`（id, status_code, headers）→ 若干 `response_chunk`（id, body_base64）→ 可选 `response_end`（id） |
| 用户访问 | `https://host/device/{device_id}/...` → 查隧道 → 发 TunnelRequest → 收 TunnelResponse（普通或流式）→ 回写用户 |
| 无隧道时 | 返回 503（设备离线） |

---

## 8. 设备端行为摘要（调试参考）

- 设备连接后只收 TunnelRequest、只发 TunnelResponse；不会主动发“请求”给服务器。
- 设备收到 TunnelRequest 后，会向 `http://127.0.0.1:8080` + `path` 发 HTTP 请求（method、headers、body 解码后使用），然后把状态码、响应头、响应体 Base64 后封装成 TunnelResponse 按 `id` 发回。
- 设备会断线重连；重连时仍用同一 `device_id`（和 token），服务器侧应踢掉旧连接、登记新连接。

