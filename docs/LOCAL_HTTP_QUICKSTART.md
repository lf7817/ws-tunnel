# 本地/内网 IP 快速验证（HTTP，无需 Nginx/域名）

本文档用于在**允许 HTTP** 的情况下，用最少步骤验证本仓库 **tunnel-server（隧道中心）+ tunnel-client（隧道客户端）** 的 WebSocket 隧道与 /device 反代是否端到端可用。

## 前置条件

- 已准备好 **tunnel-server** 与 **tunnel-client** 可执行文件（从 [README 如何编译](../README.md#如何编译) 编译，或从 Release 下载）
- 以下示例假设可执行文件在当前目录；若从源码运行，可使用 `go run ./cmd/tunnel-server` / `go run ./cmd/tunnel-client`

## 端到端最短链路（在同一台机器上）

### 1）启动“设备本地服务”（模拟设备上的 `127.0.0.1:8080`）

用 Python 启动一个静态文件服务即可：

```bash
python3 -m http.server 8080
```

保持该终端窗口运行。

### 2）启动 tunnel-server（隧道中心，HTTP）

```bash
LISTEN_ADDR=:8081 \
DEVICE_TOKENS="RTK001=dev-token" \
./tunnel-server
```

说明：
- `LISTEN_ADDR`：服务监听地址
- `DEVICE_TOKENS`：设备鉴权静态映射，格式：`device_id=token,device_id2=token2`

### 3）启动 tunnel-client（隧道客户端，连接中心并转发到本地）

```bash
SERVER_WS="ws://127.0.0.1:8081/tunnel/device" \
DEVICE_ID=RTK001 \
TOKEN=dev-token \
TARGET_BASE="http://127.0.0.1:8080" \
./tunnel-client
```

说明：
- `SERVER_WS`：中心服务器的 WS 地址（HTTP 下用 `ws://`）
- `TARGET_BASE`：设备本地 HTTP 服务的 base URL（隧道转发目标）

### 4）访问验证

浏览器打开：
- `http://127.0.0.1:8081/device/RTK001/`

或用 curl：

```bash
curl -i "http://127.0.0.1:8081/device/RTK001/"
```

你应当看到来自步骤 1 的 `http.server` 的响应内容（目录列表/文件内容）。

## 从另一台机器/手机访问（内网 IP）

如果你想让同一局域网的其它设备访问，把上面 URL 中的 `127.0.0.1` 换成运行隧道中心（tunnel-server）那台机器的 **内网 IP**：

- `http://<LAN_IP>:8081/device/RTK001/`

示例：
- `http://192.168.1.10:8081/device/RTK001/`

注意：
- 需要确保防火墙/安全组放行 `8081` 端口
- `tunnel-client` 与“设备本地服务”在同一台机器时，`TARGET_BASE` 仍然保持 `http://127.0.0.1:8080`

## 常见问题

### 1）返回 503（device offline）
- 说明该 `device_id` 没有在线的 WS 连接
- 检查 `tunnel-client` 是否已连接成功，以及 `DEVICE_ID` 是否与访问路径一致（如都是 `RTK001`）

### 2）返回 401（unauthorized）
- 检查 `DEVICE_TOKENS` 中是否配置了该 `device_id`，且 `TOKEN` 是否匹配

### 3）返回 504（gateway timeout）
- 设备端没能在超时时间内返回响应
- 检查 `TARGET_BASE` 是否可访问、`8080` 服务是否在运行

