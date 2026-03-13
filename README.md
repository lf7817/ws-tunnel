# WS Tunnel

基于 WebSocket 的 **ws-tunnel**：含**隧道中心**（tunnel-server）与**隧道客户端**（tunnel-client）。设备通过客户端出站连到中心，中心对外提供按设备 ID 的 HTTP 反向代理，使用户可通过浏览器访问设备本地的 Web 服务（如配置页、API），而无需与设备在同一局域网。

适用于 4G/NAT 后的嵌入式设备、工控机、边缘网关等需要远程访问其本地 HTTP 服务的场景。

---

## 简介

- **隧道中心（tunnel-server）**：设备主动用 WebSocket 连到本服务并注册 `device_id`，用户访问 `https://your-server/device/{device_id}/...` 时，请求经隧道转发到设备本地（如 `http://127.0.0.1:8080`），响应原样回传。
- **隧道客户端（tunnel-client）**：通用客户端，连接中心并将请求转发到设备本地 HTTP 服务，与业务解耦；实现思路与技术细节见 [docs/CLIENT_IMPLEMENTATION.md](docs/CLIENT_IMPLEMENTATION.md)。
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

使用本仓库提供的 **tunnel-server**（隧道中心）与 **tunnel-client**（隧道客户端）。可从 GitHub Release 下载对应平台二进制，或从源码编译（见「如何编译」）。以下示例假设可执行文件在当前目录。

1. **启动“设备本地服务”**（模拟设备上的 `127.0.0.1:8080`）：

   ```bash
   python3 -m http.server 8080
   ```

2. **启动隧道中心**（二选一）：

   - **方式 A：环境变量**（适合少量设备）

   ```bash
   LISTEN_ADDR=:8081 \
   DEVICE_TOKENS="my-device=dev-token" \
   ./tunnel-server
   ```

   - **方式 B：配置文件**（推荐多设备；改文件保存即可，新连接按文件 mtime 自动加载，无需重启）

   ```bash
   # 新建 devices.conf，每行: device_id=token
   echo 'my-device=dev-token' > devices.conf
   LISTEN_ADDR=:8081 DEVICE_TOKENS_FILE=devices.conf ./tunnel-server
   ```

3. **启动隧道客户端**（连接中心并转发到本地 8080）：

   ```bash
   SERVER_WS="ws://127.0.0.1:8081/tunnel/device" \
   DEVICE_ID=my-device \
   TOKEN=dev-token \
   TARGET_BASE="http://127.0.0.1:8080" \
   ./tunnel-client
   ```

4. **访问**：浏览器打开 `http://127.0.0.1:8081/device/my-device/`，应看到 8080 服务的响应。

更多步骤与内网 IP 访问见 [docs/LOCAL_HTTP_QUICKSTART.md](docs/LOCAL_HTTP_QUICKSTART.md)。

### 环境变量（隧道中心 `tunnel-server`）

| 变量 | 默认 | 说明 |
|------|------|------|
| `LISTEN_ADDR` | `:8081` | 服务监听地址 |
| `DEVICE_PREFIX` | `/device/` | 用户访问路径前缀 |
| `TUNNEL_DEVICE_PATH` | `/tunnel/device` | WebSocket 隧道路径 |
| `DEVICE_TOKENS_FILE` | （空） | 设备 token 文件路径；设置后设备列表从该文件加载，**忽略** `DEVICE_TOKENS` |
| `DEVICE_TOKENS` | （空） | 设备鉴权（仅当未设置 `DEVICE_TOKENS_FILE` 时生效），格式 `id=token,id2=token2` |
| `REQUEST_TIMEOUT` | 30s | 单次转发请求超时 |
| `WS_PING_INTERVAL` / `WS_PONG_WAIT` | 25s / 60s | 隧道心跳 |
| `WS_ALLOW_ALL_ORIGINS` | true | 是否允许任意 WebSocket Origin |
| `MAX_BODY_BYTES` | 20MiB | 请求/响应 body 上限 |

**设备 token 文件格式**（`DEVICE_TOKENS_FILE`）：纯文本，每行一条 `device_id=token`，`#` 开头为注释。示例：

```text
# 设备 ID=token，每行一个
my-device=dev-token
RTK001=secret-token-1
RTK002=secret-token-2
```

修改并保存该文件后，**新建立的设备连接**会按文件 mtime 自动读到最新配置，无需重启服务、无需发信号。

### 环境变量（隧道客户端 `tunnel-client`）

| 变量 | 默认 | 说明 |
|------|------|------|
| `SERVER_WS` | `ws://127.0.0.1:8081/tunnel/device` | 中心 WebSocket 地址 |
| `DEVICE_ID` | `RTK001` | 设备唯一标识 |
| `TOKEN` | `dev-token` | 鉴权令牌，与中心配置一致 |
| `TARGET_BASE` | `http://127.0.0.1:8080` | 本地 HTTP 服务基地址 |
| `REQUEST_TIMEOUT` | 30s | 单次本地请求超时 |
| `RECONNECT_INITIAL` | 1s | 断线后首次重连间隔 |
| `RECONNECT_MAX` | 60s | 重连间隔上限 |

### 协议与实现约定

协议与实现细节见 [docs/REMOTE_ACCESS_TUNNEL_SERVER_SPEC.md](docs/REMOTE_ACCESS_TUNNEL_SERVER_SPEC.md)。

### 隧道客户端

本仓库提供 **tunnel-client**，可直接在设备端使用。若需自研实现，设备端需主动用 WebSocket 连接中心、接收 `TunnelRequest`、向本地 HTTP 服务转发并把响应回传（含普通与 SSE 流式）。协议与实现要点见 [docs/CLIENT_IMPLEMENTATION.md](docs/CLIENT_IMPLEMENTATION.md)。

### 生产部署：WSS、HTTPS 与 Nginx

- **WSS/HTTPS**：推荐用 Nginx（或 Caddy）做 TLS 终结，反向代理到 tunnel-server 的 HTTP/WS，无需改本仓库代码。
- **负载与多实例**：设备连接表在进程内，同一 `device_id` 的隧道与 HTTP 请求须落到同一实例；多实例时需粘性路由或按 region/域名分流。

配置示例、WebSocket 代理要点与多实例注意点见 [docs/WSS_AND_NGINX.md](docs/WSS_AND_NGINX.md)。

**Ubuntu / systemd 部署**：可执行文件与配置文件放置路径、systemd unit 示例与步骤见 [docs/SYSTEMD_DEPLOY.md](docs/SYSTEMD_DEPLOY.md)。

---

## 如何编译

**要求**：Go 1.26+

### Linux / macOS

```bash
# 克隆仓库后进入项目目录（目录名以实际为准，如 ws-tunnel）
cd ws-tunnel

# 下载依赖
go mod download

# 编译隧道中心（产物：当前目录或指定输出）
go build -o tunnel-server ./cmd/tunnel-server

# 编译隧道客户端（用于联调或部署在设备端）
go build -o tunnel-client ./cmd/tunnel-client
```

指定输出路径示例：

```bash
go build -o bin/tunnel-server ./cmd/tunnel-server
go build -o bin/tunnel-client ./cmd/tunnel-client
```

### Windows

在 **PowerShell** 或 **CMD** 中：

```powershell
# 进入项目目录（目录名以实际为准，如 ws-tunnel）
cd ws-tunnel

# 下载依赖
go mod download

# 编译隧道中心（生成 tunnel-server.exe）
go build -o tunnel-server.exe ./cmd/tunnel-server

# 编译隧道客户端（生成 tunnel-client.exe）
go build -o tunnel-client.exe ./cmd/tunnel-client
```

若希望输出到子目录：

```powershell
go build -o bin/tunnel-server.exe ./cmd/tunnel-server
go build -o bin/tunnel-client.exe ./cmd/tunnel-client
```

### 交叉编译示例

- Linux 下编译 Windows 可执行文件：

  ```bash
  GOOS=windows GOARCH=amd64 go build -o tunnel-server.exe ./cmd/tunnel-server
  GOOS=windows GOARCH=amd64 go build -o tunnel-client.exe ./cmd/tunnel-client
  ```

- Windows 下编译 Linux 可执行文件（需安装 Go 并配置好环境变量）：

  ```powershell
  $env:GOOS="linux"; $env:GOARCH="amd64"; go build -o tunnel-server ./cmd/tunnel-server
  $env:GOOS="linux"; $env:GOARCH="amd64"; go build -o tunnel-client ./cmd/tunnel-client
  ```

---

## 仓库结构概览

```
.
├── cmd/
│   ├── tunnel-server/   # 隧道中心服务入口
│   └── tunnel-client/   # 通用隧道客户端，连接中心并转发到本地 HTTP
├── internal/
│   ├── config/          # 配置加载（环境变量）
│   ├── tunnel/          # WebSocket 隧道、设备连接表、协议
│   ├── tunnelclient/    # 隧道客户端逻辑（写串行化、流式、重连）
│   ├── httpproxy/       # /device/{id}/... HTTP 反代
│   ├── httpx/            # Header 过滤等工具
│   └── id/               # 请求 ID 生成
├── docs/
│   ├── REMOTE_ACCESS_TUNNEL_SERVER_SPEC.md   # 协议与实现说明
│   ├── CLIENT_IMPLEMENTATION.md              # 隧道客户端实现说明
│   ├── CLIENT_AS_GENERIC_TOOL.md             # 客户端通用化说明
│   ├── LOCAL_HTTP_QUICKSTART.md              # 本地 HTTP 快速验证
│   ├── WSS_AND_NGINX.md                      # WSS、HTTPS 与 Nginx 部署
│   └── SYSTEMD_DEPLOY.md                     # systemd 部署隧道中心
├── go.mod
└── README.md
```

---

## License

见仓库根目录 LICENSE 文件（如有）。
