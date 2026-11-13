# Minecraft 加密 TCP 入口

链路：Minecraft → 电脑本地 sing-box → Trojan/TLS（服务器 443）→ 原有 ZBProxy → mc.hypixel.net。

保留原来的 ZBProxy 配置、网页和玩家名白名单。使用 sing-box v1.14.4，不改写加密算法；电脑端验证域名证书，并使用 Chrome uTLS ClientHello。普通 HTTPS 请求转到静态页面。这里只改变电脑到代理的传输形式，不保证无法识别，也不保证降低延迟或修复线路丢包。没有开启多路复用，游戏连接各自使用独立 TCP 连接。

服务端需要 Linux、Docker Compose、Python 3，以及一个能直接解析到服务器的域名。以下命令以现有 ZBProxy 的宿主机游戏端口 **55565** 为例，网页管理端口不参与转发。

## 1. 域名和端口

将 `game.example.com` 的 A 记录指向 `64.83.0.57`，使用 DNS 解析模式，不经过普通 HTTP CDN 代理。如果服务器没有配置 IPv6，不要为该域名添加 AAAA 记录。

在云防火墙和系统防火墙放行 TCP 80、443。80 用于申请和续期证书，443 用于加密游戏连接。确认 80、443 未被其他程序占用，宿主机回环端口 18088 也需空闲：

```bash
ss -lntp '( sport = :80 or sport = :443 or sport = :18088 )'
```

原有 ZBProxy 应继续运行，并能通过宿主机 `127.0.0.1:55565` 访问。如果现有 Docker 只绑定了公网 IP，需要将其游戏端口映射改为包含回环地址，例如 `55565:25565`，或仅供隧道使用的 `127.0.0.1:55565:25565`。容器内 ZBProxy 的 Bind 仍需允许容器网卡访问；不要将容器内服务 Bind 改成回环地址来代替宿主机端口映射。

## 2. 服务器上生成配置

以下命令在服务器运行。替换域名和邮箱；如果实际游戏端口不同，修改 `--upstream-port`。

```bash
cd /penguin/ZBProxy
git pull --ff-only
cd deploy/trojan
python3 prepare.py --domain game.example.com --email you@example.com \
  --server-ip 64.83.0.57 --upstream-port 55565
```

脚本生成随机密码，并将 `runtime/server.json`、`runtime/client.json` 和 `.env` 设为仅当前用户可读写。已有配置时拒绝覆盖，以免更换密码导致客户端失效。这些文件、证书和 ACME 数据已加入 Git 和 Docker 构建忽略列表。`--output-dir` 仅更改生成目录；自行指定时还需将 compose.yaml、nginx.conf 和 site 复制到该目录。

申请证书，校验并启动：

```bash
docker compose --profile certificate run --rm --service-ports certbot
docker compose run --rm --no-deps tunnel -c /etc/sing-box/server.json check
docker compose up -d website tunnel
docker compose logs --tail=50 tunnel
```

证书通过实际域名在此服务器申请；仓库没有附带证书。可通过浏览器访问 `https://game.example.com` 检查证书和静态页面。只有已通过 Trojan 密码验证的游戏流量被强制转到本机 ZBProxy；客户端不能选择其他出口目的地。

## 3. 玩家 Linux 电脑

以下命令在你自己的电脑运行，适用于截图中的 Linux amd64。其他系统从 [sing-box 官方发布页](https://github.com/SagerNet/sing-box/releases/tag/v1.14.4) 下载对应版本。

```bash
mkdir -p ~/zbproxy-client
cd ~/zbproxy-client
scp root@64.83.0.57:/penguin/ZBProxy/deploy/trojan/runtime/client.json ./client.json
chmod 600 client.json
curl -fL -o sing-box.tar.gz \
  https://github.com/SagerNet/sing-box/releases/download/v1.14.4/sing-box-1.14.4-linux-amd64.tar.gz
tar -xzf sing-box.tar.gz
./sing-box-1.14.4-linux-amd64/sing-box -c client.json check
./sing-box-1.14.4-linux-amd64/sing-box -c client.json run
```

保持客户端运行，在 Minecraft 添加服务器：**127.0.0.1:25565**。不要在 MC 中直接连接域名的 443 端口。无需安装 MC Mod，也无需改电脑全局代理。客户端仅监听回环地址，不向局域网开放。

`client.json` 内含隧道密码，私下传给需要使用的电脑。该密码与网页管理密码无关；不要公开上传。玩家名仍须通过原有白名单。因为 ZBProxy 接收到的是本机隧道连接，它看到的来源是回环地址，原有来源 IP 规则和日志不能继续当作玩家真实 IP 使用。

## 4. 日常维护和对照测试

先用一个玩家分别测试原来的 `64.83.0.57:55565` 和加密入口 `127.0.0.1:25565`，比较手动菜单、聊天和持续游戏的表现。TCP 握手耗时无法单独证明游戏长连接正常。若加密入口依然卡，应继续抓包定位重传、线路或上游问题。

证书需要续期。先赋予脚本执行权限：

```bash
cd /penguin/ZBProxy/deploy/trojan
chmod +x renew.sh
```

在服务器 root 的 `crontab -e` 添加每日检查（机器需有 cron 服务）：

```cron
15 3 * * * /penguin/ZBProxy/deploy/trojan/renew.sh >> /var/log/zbproxy-tls-renew.log 2>&1
```

脚本仅在证书实际更新时重启 tunnel，更新时现有加密连接会断开，需要重新连接。TCP 80 必须能继续用于续期。也可以手动执行 `./renew.sh`。查看日志使用 `docker compose logs -f tunnel`；停止加密入口使用 `docker compose down`，不会停止另一个 Compose 项目的原有 ZBProxy。

确认加密入口稳定后，可以将原有 Docker 的游戏发布端口收紧到 `127.0.0.1:55565:25565`；保持隧道目标端口一致。网页管理端口单独配置。

## 验证与参考

本地使用上述版本验证服务端和客户端配置，并通过临时证书、真实 ZBProxy 和模拟 Minecraft 上游验证了 HTTPS 页面、玩家名白名单、Hypixel 握手改写、固定出口和 200 KiB 数据往返。公网 DNS、正式证书及实际线路表现需要部署后验证。

配置字段参考 [Trojan 入站](https://sing-box.sagernet.org/configuration/inbound/trojan/)、[Trojan 出站](https://sing-box.sagernet.org/configuration/outbound/trojan/)、[TLS](https://sing-box.sagernet.org/configuration/shared/tls/)、[路由规则](https://sing-box.sagernet.org/configuration/route/rule/) 和 [Certbot Docker 文档](https://eff-certbot.readthedocs.io/en/stable/install.html#running-with-docker)。
