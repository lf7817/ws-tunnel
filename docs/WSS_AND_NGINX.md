# 生产部署：WSS、HTTPS 与 Nginx

本文档说明如何通过 **Nginx 反向代理** 为本仓库的 **tunnel-server**（隧道中心）提供 WSS（WebSocket over TLS）和 HTTPS，以及如何做负载与高可用。设备端使用本仓库的 **tunnel-client** 连接中心即可。

---

## 1. 架构要点

- **tunnel-server 只监听 HTTP**（如 `127.0.0.1:8081`），不直接处理 TLS。
- **Nginx 对外提供 443/80**：终结 TLS，将 `https://` 和 `wss://` 反向代理到后端 HTTP/WS。
- **证书与域名**：在 Nginx 侧配置 SSL 证书与域名，便于续期和统一管理。

---

## 2. Nginx 反向代理（单实例，WSS + HTTPS）

以下配置实现：

- `https://your-domain.com/device/...` → HTTP 反代到 tunnel-server
- `wss://your-domain.com/tunnel/device` → WebSocket 反代到 tunnel-server

```nginx
# 建议：在 http 块中配置 upstream，便于复用
upstream tunnel_backend {
    server 127.0.0.1:8081;
    keepalive 32;
}

server {
    listen 443 ssl http2;
    server_name your-domain.com;

    # SSL 证书（按实际路径修改）
    ssl_certificate     /path/to/fullchain.pem;
    ssl_certificate_key /path/to/privkey.pem;

    # 可选：SSL 安全配置
    ssl_protocols TLSv1.2 TLSv1.3;
    ssl_prefer_server_ciphers on;

    # WebSocket 隧道路径：必须带 Upgrade、Connection
    location /tunnel/device {
        proxy_pass http://tunnel_backend;
        proxy_http_version 1.1;
        proxy_set_header Upgrade $http_upgrade;
        proxy_set_header Connection "upgrade";
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
        proxy_read_timeout 3600s;
        proxy_send_timeout 3600s;
    }

    # 用户访问设备：/device/{device_id}/...
    location /device/ {
        proxy_pass http://tunnel_backend;
        proxy_http_version 1.1;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
        proxy_buffering off;
        proxy_read_timeout 60s;
        proxy_send_timeout 60s;
    }

    # 健康检查（可选）
    location /healthz {
        proxy_pass http://tunnel_backend;
        proxy_http_version 1.1;
        proxy_set_header Host $host;
    }
}
```

**要点**：

- `/tunnel/device` 的 `Upgrade`、`Connection "upgrade"` 是 WebSocket 升级所必需。
- `proxy_read_timeout` / `proxy_send_timeout` 对长连接适当调大，避免隧道被中途断开。
- `X-Forwarded-Proto` 若后端需要区分 HTTPS 可保留；当前 tunnel-server 不依赖该头。

**HTTP 跳转 HTTPS（可选）**：

```nginx
server {
    listen 80;
    server_name your-domain.com;
    return 301 https://$server_name$request_uri;
}
```

---

## 3. 多实例与负载均衡（粘性路由）

tunnel-server 的**设备连接表在进程内**：每个 WebSocket 连接只存在于某一台后端。因此：

- 同一 `device_id` 的 **隧道连接** 与 **该设备的 HTTP 请求** 必须落到**同一台** tunnel-server，否则会出现「设备已连接但访问返回 503」。
- 做法：对 **路径中的 device_id 做粘性**，让同一 device 的流量固定到同一后端。

### 3.1 基于路径中 device_id 的 hash（推荐）

从路径 `/device/DEVICE_ID/...` 中取出第二段作为 device_id，用 `hash` 或 `map` 选后端。

**方式 A：Nginx 用 `split_clients` 或 `map` + `hash`**

Nginx 标准版没有按「路径第二段」直接 hash 的指令，可用 `map` 把路径映射成一个键再配合 `hash`。更简单的方式是 **用 `hash $request_uri`** 做一致性哈希，使同一 URI 前缀（同一 device）尽量落同一后端；或使用 **Nginx Plus** 的 `sticky`。

**方式 B：用路径第二段做 hash key（需 Nginx 1.18+ 或 OpenResty）**

若使用 **OpenResty** 或能写 Lua，可从 `ngx.var.uri` 解析出 device_id 再赋值给某个变量做 hash。这里仅给出**标准 Nginx 下可用的实用方案**。

**方式 C：标准 Nginx — 多 upstream 按路径前缀分流（固定映射）**

若实例数固定（如 2 台），可对 device_id 按首字母或简单规则写到不同 location：

```nginx
# 示例：2 台后端，按路径第二段（device_id）首字符奇偶分流
# 需要 Nginx 1.18+ 的 map + 变量做 hash
map $request_uri $backend_device {
    default 127.0.0.1:8081;
    # 更稳妥的方式是使用 Nginx Plus 的 sticky，或保持单实例
}
```

标准开源 Nginx 下**最稳妥的多实例方案**是：

- **方案 1**：只跑**单实例** tunnel-server，前面 Nginx 只做 TLS 终结和反向代理（见上文）。
- **方案 2**：多实例时，**同一 device_id 只连其中一台**（例如通过 DNS 或配置让设备按 region 连不同域名/端口），Nginx 按 server_name 或端口把流量转到对应后端，从架构上保证「同 device 同后端」。

### 3.2 多 upstream 配置示例（单实例可忽略）

若已通过其他方式（如 DNS、不同端口）保证「同 device 同后端」，可仅做负载与健康检查：

```nginx
upstream tunnel_backend {
    server 127.0.0.1:8081 max_fails=2 fail_timeout=10s;
    # server 127.0.0.1:8082 max_fails=2 fail_timeout=10s;  # 仅当流量已按 device 分流时启用
    keepalive 32;
}
```

---

## 4. 后端直连 TLS（不经过 Nginx）

若不想用 Nginx，可由 tunnel-server 直接监听 TLS。当前仓库**未实现**该逻辑，可自行在 `main.go` 中改为：

```go
// 示例：需从环境变量或配置文件读取证书路径
err := srv.ListenAndServeTLS("/path/to/fullchain.pem", "/path/to/privkey.pem")
```

缺点：证书续期、多域名/SNI 需自行处理，一般更推荐用 Nginx/Caddy 做 TLS 终结。

---

## 5. 小结

| 需求           | 做法 |
|----------------|------|
| WSS / HTTPS    | 使用 Nginx 反向代理，在 Nginx 配置 SSL，代理到后端 `http://127.0.0.1:8081`。 |
| 单实例部署     | 一个 tunnel-server，Nginx 只做 TLS + 反代（见第 2 节配置）。 |
| 多实例/高可用  | 保证「同一 device_id 的隧道与 HTTP 请求到同一后端」：单实例或按 region/域名分流；标准 Nginx 下多实例负载需粘性路由（或 Nginx Plus / OpenResty）。 |
| 健康检查       | 使用 tunnel-server 的 `/healthz`，在 Nginx `upstream` 中配置 `max_fails`/`fail_timeout`。 |

协议与安全建议见 [REMOTE_ACCESS_TUNNEL_SERVER_SPEC.md](REMOTE_ACCESS_TUNNEL_SERVER_SPEC.md) 中的「安全与生产建议」一节。
