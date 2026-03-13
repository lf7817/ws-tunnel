# Tunnel Server

基于 WebSocket 的**设备隧道中心服务器**：设备通过出站 WebSocket 接入中心，中心对外提供按设备 ID 的 HTTP 反向代理，使用户可通过浏览器访问设备本地的 Web 服务（如配置页、API），而无需与设备在同一局域网。

适用于 4G/NAT 后的嵌入式设备、工控机、边缘网关等需要远程访问其本地 HTTP 服务的场景。

---

## 项目是什么

- **中心角色**：本仓库是「隧道中心服务器」的实现。设备主动用 WebSocket 连到本服务并注册 `device_id`，用户访问 `https://your-server/device/{device_id}/...` 时，请求经隧道转发到设备本地（如 `http://127.0.0.1:8080`），响应原样回传。
- **设备角色**：设备端（隧道客户端）由其他项目实现，按同一协议连接本服务器并转发 HTTP。本仓库提供 `mock-device` 用于本地联调；设备端实现思路与技术细节见 [docs/CLIENT_IMPLEMENTATION.md](docs/CLIENT_IMPLEMENTATION.md)。
- **效果**：用户在浏览器打开 `https://your-server.com/device/my-device/` 即可看到该设备的配置页，无需与设备在同一局域网或做端口映射。

---

## 适用场景

| 场景 | 说明 |
|------|------|
| **远程运维** | 现场设备在 4G/NAT 后，运维人员通过中心统一入口访问设备本地 Web 配置页或 API。 |
| **多设备分散部署** | 多台现场设备分散部署，通过隧道中心按设备 ID 访问每台设备的 Web 管理界面或状态接口。 |
| **工控/边缘设备** | 边缘网关、工控机上的本地 HTTP 服务需要被远程访问，又不方便每台做公网映射。 |
| **内网穿透替代** | 设备主动连出、中心反代，无需在设备侧暴露端口或部署 frp/ngrok 客户端。 |

---

## 技术细节

- **隧道协议**：WebSocket（Text 帧），UTF-8 JSON。设备连接 `wss://host/tunnel/device?device_id=xxx&token=yyy`，单设备单连接，新连踢旧连。
- **请求/响应封装**：服务器向设备发 `TunnelRequest`（`type: "request"`, `id`, `method`, `path`, `headers`, `body_base64`），设备回 `TunnelResponse`（`type: "response"`, `id`, `status_code`, `headers`, `body_base64`）；支持流式响应（SSE）：`response_start` → `response_chunk` → `response_end`。
- **路由**：用户访问 `/device/{device_id}/...`，路径去掉 `/device/{device_id}` 后作为 `path` 转发到设备本地。
- **实现要点**：单协程读、写串行化、Hop-by-hop Header 过滤、请求超时与断连清理、Body 大小限制。详见 [docs/REMOTE_ACCESS_TUNNEL_SERVER_SPEC.md](docs/REMOTE_ACCESS_TUNNEL_SERVER_SPEC.md)。

---

## 如何使用

### 快速开始（本地 HTTP 验证）

1. **启动“设备本地服务”**（模拟设备上的 `127.0.0.1:8080`）：

   ```bash
   python3 -m http.server 8080
   ```

2. **启动隧道中心**：

   ```bash
   LISTEN_ADDR=:8081 \
   DEVICE_TOKENS="my-device=dev-token" \
   go run ./cmd/tunnel-server
   ```

3. **启动模拟设备**（连接中心并转发到本地 8080）：

   ```bash
   SERVER_WS="ws://127.0.0.1:8081/tunnel/device" \
   DEVICE_ID=my-device \
   TOKEN=dev-token \
   TARGET_BASE="http://127.0.0.1:8080" \
   go run ./cmd/mock-device
   ```

4. **访问**：浏览器打开 `http://127.0.0.1:8081/device/my-device/`，应看到 8080 服务的响应。

更多步骤与内网 IP 访问见 [docs/LOCAL_HTTP_QUICKSTART.md](docs/LOCAL_HTTP_QUICKSTART.md)。

### 环境变量（隧道中心 `tunnel-server`）

| 变量 | 默认 | 说明 |
|------|------|------|
| `LISTEN_ADDR` | `:8081` | 服务监听地址 |
| `DEVICE_PREFIX` | `/device/` | 用户访问路径前缀 |
| `TUNNEL_DEVICE_PATH` | `/tunnel/device` | WebSocket 隧道路径 |
| `DEVICE_TOKENS` | （空） | 设备鉴权，格式 `id=token,id2=token2` |
| `REQUEST_TIMEOUT` | 30s | 单次转发请求超时 |
| `WS_PING_INTERVAL` / `WS_PONG_WAIT` | 25s / 60s | 隧道心跳 |
| `WS_ALLOW_ALL_ORIGINS` | true | 是否允许任意 WebSocket Origin |
| `MAX_BODY_BYTES` | 20MiB | 请求/响应 body 上限 |

### 协议与实现约定

协议与实现细节见 [docs/REMOTE_ACCESS_TUNNEL_SERVER_SPEC.md](docs/REMOTE_ACCESS_TUNNEL_SERVER_SPEC.md)。

### 设备端（客户端）如何实现

设备端需主动用 WebSocket 连接中心、接收 `TunnelRequest`、向本地 HTTP 服务转发请求并把响应封装回传（含普通响应与 SSE 流式）。实现思路、协议字段、读/写模型、Header 过滤与流式响应等见 [docs/CLIENT_IMPLEMENTATION.md](docs/CLIENT_IMPLEMENTATION.md)。

### 生产部署：WSS、HTTPS 与 Nginx

- **WSS/HTTPS**：推荐用 Nginx（或 Caddy）做 TLS 终结，反向代理到 tunnel-server 的 HTTP/WS，无需改本仓库代码。
- **负载与多实例**：设备连接表在进程内，同一 `device_id` 的隧道与 HTTP 请求须落到同一实例；多实例时需粘性路由或按 region/域名分流。

配置示例、WebSocket 代理要点与多实例注意点见 [docs/WSS_AND_NGINX.md](docs/WSS_AND_NGINX.md)。

---

## 如何编译

**要求**：Go 1.26+

### Linux / macOS

```bash
# 克隆仓库后进入项目目录
cd tunnel-server

# 下载依赖
go mod download

# 编译隧道中心（产物：当前目录或指定输出）
go build -o tunnel-server ./cmd/tunnel-server

# 编译模拟设备（用于联调）
go build -o mock-device ./cmd/mock-device
```

指定输出路径示例：

```bash
go build -o bin/tunnel-server ./cmd/tunnel-server
go build -o bin/mock-device ./cmd/mock-device
```

### Windows

在 **PowerShell** 或 **CMD** 中：

```powershell
# 进入项目目录
cd tunnel-server

# 下载依赖
go mod download

# 编译隧道中心（生成 tunnel-server.exe）
go build -o tunnel-server.exe ./cmd/tunnel-server

# 编译模拟设备（生成 mock-device.exe）
go build -o mock-device.exe ./cmd/mock-device
```

若希望输出到子目录：

```powershell
go build -o bin/tunnel-server.exe ./cmd/tunnel-server
go build -o bin/mock-device.exe ./cmd/mock-device
```

### 交叉编译示例

- Linux 下编译 Windows 可执行文件：

  ```bash
  GOOS=windows GOARCH=amd64 go build -o tunnel-server.exe ./cmd/tunnel-server
  ```

- Windows 下编译 Linux 可执行文件（需安装 Go 并配置好环境变量）：

  ```powershell
  $env:GOOS="linux"; $env:GOARCH="amd64"; go build -o tunnel-server ./cmd/tunnel-server
  ```

---

## 仓库结构概览

```
.
├── cmd/
│   ├── tunnel-server/   # 隧道中心服务入口
│   └── mock-device/     # 模拟设备端，用于本地联调
├── internal/
│   ├── config/          # 配置加载（环境变量）
│   ├── tunnel/          # WebSocket 隧道、设备连接表、协议
│   ├── httpproxy/       # /device/{id}/... HTTP 反代
│   ├── httpx/            # Header 过滤等工具
│   └── id/               # 请求 ID 生成
├── docs/
│   ├── REMOTE_ACCESS_TUNNEL_SERVER_SPEC.md   # 协议与实现说明
│   ├── CLIENT_IMPLEMENTATION.md              # 设备端（隧道客户端）实现说明
│   ├── LOCAL_HTTP_QUICKSTART.md              # 本地 HTTP 快速验证
│   └── WSS_AND_NGINX.md                      # WSS、HTTPS 与 Nginx 部署
├── go.mod
└── README.md
```

---

## License

见仓库根目录 LICENSE 文件（如有）。
