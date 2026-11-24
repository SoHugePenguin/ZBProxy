# ZBProxy
[![FOSSA Status](https://app.fossa.com/api/projects/git%2Bgithub.com%2Flayou233%2FZBProxy.svg?type=small)](https://app.fossa.com/projects/git%2Bgithub.com%2Flayou233%2FZBProxy?ref=badge_small)
[![Go Reference](https://pkg.go.dev/badge/github.com/layou233/zbproxy/v3.svg)](https://pkg.go.dev/github.com/layou233/zbproxy/v3)
[![Go Report Card](https://goreportcard.com/badge/github.com/layou233/zbproxy/v3)](https://goreportcard.com/report/github.com/layou233/zbproxy/v3)

[**English**](README.md) | **简体中文**

🚀 一个简单、快速、高性能的多用途 TCP 中继，主要为搭建 Hypixel 加速 IP 而开发。

一键搭建Minecraft加速IP软件，作者[B站@贴吧蜡油](https://space.bilibili.com/404017926 "点我前往空间")。

## Feature Highlights

- [x] ☝ 一键部署
- [x] 📋 高可自定义的配置
- [x] 🔌 在 Linux 上使用 `splice(2)` 进行零拷贝转发, 以及其它两种转发模式
- [x] 👮 在 IP 和 Minecraft 玩家名 上启用黑/白名单 (访问控制)
- [x] 🔄 配置文件热重载 列表 和 Minecraft MOTD
- [x] 📦 定制的轻量高性能 Minecraft 网络协议框架
- [x] 💻 干净且多彩的日志输出，易于跟踪每一个连接
- [x] 🔮 多平台和 CPU 架构支持
- 以及更多...

#### **[加入 Telegram 群](https://t.me/launium)** 
#### **[文档 (开发中)](https://launium.com/doc/ZBProxy)**

## 本程序可以做什么？
在大多数情况下，你可以使用Nginx的```proxy_pass```来中转Minecraft数据。 
完整代码如下:
```
stream {
    server {
        listen 25565;
        proxy_pass TARGET_SERVER_ADDRESS;
    }
}
```
但从2020年开始，Hypixel会验证玩家的登录地址.
如果你没有从Hypixel官方地址```mc.hypixel.net:25565```登录, 你将无法加入游戏.
最初的方法是通过修改```hosts```文件来欺骗服务器.  
但这对于很多玩家来说太复杂了. 
我们研究了它的工作原理, 在技术层面通过修改客户端发送的数据, 成功地绕过了检测.
这项研究的成果就是你现在看到的 ZBProxy.  
对于玩家来说,**直接**输入代理服务器地址便可以加入游戏.

**在最新版本，你甚至可以修改加速IP的图标和MOTD**

## 它安全吗?
完全不需要担心隐私问题，我们的代码是完全开源的，所以你可以自由检查是否有后门。

## 如何使用？
完整的文档已迁移至
https://launium.com/doc/ZBProxy

## 鸣谢
[![JetBrains logo.](https://resources.jetbrains.com/storage/products/company/brand/logos/jetbrains.svg)](https://jb.gg/OpenSource)  
使用 JetBrains IDE 开发。

## 许可证
[![FOSSA Status](https://app.fossa.com/api/projects/git%2Bgithub.com%2Flayou233%2FZBProxy.svg?type=large)](https://app.fossa.com/projects/git%2Bgithub.com%2Flayou233%2FZBProxy?ref=badge_large)
## 自用白名单管理页

管理页现支持直接配置入口端口、出口代理与认证信息、按玩家名或 UUID 分流，以及单条/批量 TCP 和 Minecraft Ping 测试。操作与生效规则见 [网站配置说明](deploy/CONSOLE.md)。

管理页使用 Go 标准库和内嵌的原生 HTML/CSS/JavaScript，随代理一起运行；无需 Node.js、数据库或外部静态资源。页面不轮询，名单仅在登录时检查，不参与游戏数据转发。

```bash
go run ./cmd/zbproxy
```

启动后访问 **http://127.0.0.1:8080**。也可编译成单个可执行文件：

```bash
go build -trimpath -ldflags="-s -w" -o zbproxy ./cmd/zbproxy
./zbproxy
```

页面支持批量添加、搜索、分页、删除和启用/关闭保护。名单人数没有配额限制；每页显示 50 人，单次 API 请求最多 1 MB，大名单可以分批添加。玩家名按大小写不敏感匹配。

首次启动白名单保护默认关闭。先添加玩家，再在页面启用；启用后空名单会拒绝所有新登录。修改立即生效，已连接的玩家不会被踢出。名单在修改成功后原子保存到 `ZBProxy.whitelist.json`，重启会恢复。此文件独立于 `ZBProxy.json`，修改名单不会重载代理或重建监听端口。运行中请通过页面修改名单；手工修改名单文件需重启才会读取。

管理的玩家名限制适用于 **Minecraft 出站和旧版 Minecraft 入口**，与原有 `NameAccess` 等配置共同生效。纯 TCP 转发无法识别玩家名，不应用此限制。这里只检查客户端提交的玩家名，正版身份仍由目标服务器验证。

网页与游戏入口均在 `ZBProxy.json` 配置绑定地址和端口：

```json
{
    "Web": { "Enabled": true, "Bind": "127.0.0.1", "Port": 8080 },
    "Services": [
        { "Name": "Hypixel-in", "Bind": "0.0.0.0", "Listen": 25565 }
    ]
}
```

以上是配置片段，保留原有 Router、Outbounds 等字段。`127.0.0.1` 仅本机可连接，`0.0.0.0` 监听所有 IPv4 网卡，`::` 监听 IPv6。游戏入口未填写 Bind 时保留原来的通配监听行为。网页配置未填写时默认监听 `127.0.0.1:8080`；`Web.Enabled: false` 关闭网页，但仍检查已有白名单。网页配置在启动时读取，修改后需重启程序。

常用参数：

| 参数 | 默认值 | 用途 |
| --- | --- | --- |
| `-web` | 读取 `Web.Bind` / `Web.Port` | 显式传入时临时覆盖文件配置；传 `-web ""` 关闭页面，名单检查仍有效 |
| `-config` | `ZBProxy.json` | 代理配置文件路径 |
| `-whitelist` | `ZBProxy.whitelist.json` | 管理页白名单文件路径 |

默认仅本机访问，可通过 SSH 隧道从自己的电脑打开：

```bash
ssh -L 8080:127.0.0.1:8080 user@your-server
```

如果需要监听其他地址，在 `ZBProxy.json` 的 `Web.Password` 中填写管理密码；登录用户名在 `Web.Username` 中设置，未填写时默认为 `admin`。也可使用环境变量 `ZBPROXY_ADMIN_PASSWORD`，非空环境变量优先于配置文件。用户名和密码配置修改后重启生效。HTTP Basic 登录本身不加密，远程访问请使用 SSH 隧道或 HTTPS 反向代理。

```bash
ZBPROXY_ADMIN_PASSWORD='替换为自己的密码' ./zbproxy -web 0.0.0.0:8080
```

网站日志直接输出到终端：启动/停止信息始终显示；访问日志默认启用，包含请求方法、路径、状态码、来源地址和耗时。网站日志独立于代理的 `Log.Level`，因此代理设为 `warn` 时也能看到网站正常访问。将 `Web.AccessLog` 设为 `false` 可关闭逐请求日志，减少终端输出；修改后重启生效。

## Docker / GHCR 镜像

支持通过 `Services[].Encryption.Enabled` 开关内置 Trojan/TLS 加密，详见 [JSON 配置与同镜像客户端](deploy/ENCRYPTION.md)。需要 Chrome uTLS 和网站回落时，也可使用 [外置 sing-box 部署](deploy/trojan/README.md)。加密不能保证修复线路丢包。

仓库的 `Docker image` 工作流会在推送 `master`、`dev-next`、推送 `v*` 标签或手动运行时，测试代码并构建 `linux/amd64` 和 `linux/arm64` 镜像。拉取请求只验证构建，不发布。镜像地址为 `ghcr.io/sohugepenguin/zbproxy`，包含分支名、完整提交 SHA 和版本标签；`latest` 由 `master` 更新。

镜像从本次仓库源码编译，使用精简的非 root 运行环境，不包含本地密码、名单、Git 元数据或预编译文件。容器内游戏端口为 `25565`，网页端口为 `8080`，配置和白名单均保存在 `/data`。

发布完成后可使用仓库中的 `compose.yaml`：

```bash
cp .env.example .env
# 编辑 .env，填入自己的管理密码
# 首次发布的 GHCR 包可能是私有的；需要先 docker login ghcr.io，或由所有者将包设为公开。
docker compose up -d
```

游戏连接地址为服务器的 `25565` 端口，网页默认仅映射到宿主机 `http://127.0.0.1:35565`。用户名默认 `admin`，密码来自 `.env`。`Web.Username`、`Web.Bind`、`Web.Port` 等设置仍在 `/data/ZBProxy.json` 中配置；环境变量密码优先于配置文件。发布端口不同于容器端口，修改端口时注意同步 `compose.yaml` 中的映射。

命名卷 `zbproxy-data` 会在首次运行时自动初始化默认配置，并持续保存白名单。查看网站与代理日志：

```bash
docker compose logs -f zbproxy
```

如需在宿主机直接编辑配置，可使用目录挂载代替命名卷：

```bash
mkdir -p data
cp deploy/ZBProxy.example.json data/ZBProxy.json
# 编辑 data/ZBProxy.json 中的 Username、Password、Bind、Port 等配置
# 容器以当前用户身份运行，确保它能保存白名单文件
docker run -d --name zbproxy --restart unless-stopped \
  --user "$(id -u):$(id -g)" \
  -p 25565:25565 -p 127.0.0.1:35565:8080 \
  -v "$PWD/data:/data" ghcr.io/sohugepenguin/zbproxy:latest
```

配置目录包含密码和名单，不要提交到 Git。首次在 fork 中使用 Actions 时，需启用工作流；工作流使用自带的 `GITHUB_TOKEN` 发布 GHCR，无需单独添加发布密码。
