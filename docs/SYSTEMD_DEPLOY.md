# Ubuntu 24.04 使用 systemd 部署隧道中心（tunnel-server）

本仓库提供 **tunnel-server**（隧道中心）与 **tunnel-client**（隧道客户端）。本文档仅描述在服务器上部署 **tunnel-server**；设备端将 **tunnel-client** 部署在设备上并配置 `SERVER_WS`、`DEVICE_ID`、`TOKEN`、`TARGET_BASE` 即可。

## 1. 推荐路径（保证能正确读到配置）

| 内容         | 路径 |
|--------------|------|
| 可执行文件   | `/usr/local/bin/tunnel-server` |
| 设备 token 文件 | `/etc/ws-tunnel/devices.conf` |

环境变量 **必须用绝对路径** 指定 `DEVICE_TOKENS_FILE`，否则 systemd 的 WorkingDirectory 可能不是你以为的目录，相对路径会读错或读不到。

---

## 2. 部署步骤

### 2.1 创建目录与配置文件

```bash
sudo mkdir -p /etc/ws-tunnel
sudo nano /etc/ws-tunnel/devices.conf
```

在 `devices.conf` 里按行写 `device_id=token`，例如：

```text
# 每行: device_id=token
my-device=dev-token
RTK001=your-secret-token
```

### 2.2 安装可执行文件

在开发机（或 CI）上交叉编译：

```bash
GOOS=linux GOARCH=amd64 go build -o tunnel-server ./cmd/tunnel-server
```

拷贝到服务器并放到 `/usr/local/bin`：

```bash
sudo cp tunnel-server /usr/local/bin/
sudo chmod 755 /usr/local/bin/tunnel-server
```

### 2.3 安装 systemd 服务

拷贝示例 unit 并启用：

```bash
sudo cp docs/tunnel-server.service.example /etc/systemd/system/tunnel-server.service
# 若可执行文件或配置路径不同，先编辑 unit
# sudo nano /etc/systemd/system/tunnel-server.service
sudo systemctl daemon-reload
sudo systemctl enable tunnel-server
sudo systemctl start tunnel-server
sudo systemctl status tunnel-server
```

### 2.4 若可执行文件/配置不在默认路径

编辑 `/etc/systemd/system/tunnel-server.service`，修改：

- `ExecStart=`：改为你实际的可执行文件路径（如 `/opt/ws-tunnel/tunnel-server`）。
- `Environment=DEVICE_TOKENS_FILE=`：改为你实际的 `devices.conf` 绝对路径（如 `/etc/ws-tunnel/devices.conf`）。

---

## 3. 常用命令

```bash
sudo systemctl start tunnel-server    # 启动
sudo systemctl stop tunnel-server      # 停止
sudo systemctl restart tunnel-server   # 重启
sudo systemctl status tunnel-server    # 状态与最近日志
journalctl -u tunnel-server -f         # 实时看日志
```

修改 `/etc/ws-tunnel/devices.conf` 后**无需重启服务**，新建立的设备连接会按文件 mtime 自动读到最新配置。

---

## 4. 可选：专用用户运行

若希望不用 root 跑进程：

```bash
sudo useradd -r -s /usr/sbin/nologin tunnel-server
sudo chown -R tunnel-server:tunnel-server /etc/ws-tunnel
sudo chown tunnel-server:tunnel-server /usr/local/bin/tunnel-server
```

在 unit 里取消注释并保存：

```ini
User=tunnel-server
Group=tunnel-server
```

然后：

```bash
sudo systemctl daemon-reload
sudo systemctl restart tunnel-server
```

---

## 5. 与 Nginx 配合（WSS/HTTPS）

若前面有 Nginx 做 TLS 与反代，tunnel-server 只需监听本机，例如：

```ini
Environment=LISTEN_ADDR=127.0.0.1:8081
```

Nginx 配置与多实例注意点见 [WSS_AND_NGINX.md](WSS_AND_NGINX.md)。
