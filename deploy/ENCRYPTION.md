# JSON 开关：内置加密入口

ZBProxy 内置 TCP-only Trojan/TLS。无需额外运行 sing-box 或 nginx。加密在已有服务监听端口上启用，解密和密码认证后继续执行原来的 Minecraft 路由和玩家名白名单。密码与网页管理密码独立。

## 服务端

在 `ZBProxy.json` 的对应 `Services` 项里添加：

```json
{
    "Name": "Hypixel-in",
    "Bind": "0.0.0.0",
    "Listen": 25565,
    "Encryption": {
        "Enabled": true,
        "CertFile": "/certs/live/game.example.com/fullchain.pem",
        "KeyFile": "/certs/live/game.example.com/privkey.pem",
        "Password": "替换为自己的随机长密码"
    }
}
```

这是一个服务项示例，不是完整根配置。其余 `Router`、`Outbounds`、`Web` 保留原值。`Enabled: false` 或省略整个 `Encryption` 使用原来的明文入口，关闭时不读取证书。开启后该端口只接受加密连接，不能同时供原版 MC 直接连接。证书或密码缺失会启动失败，不会自动退回明文。

使用自己域名的有效证书。现有 `deploy/trojan` 的 Certbot 配置仍可申请证书；也可以使用已有证书。需要将完整证书目录只读挂载进容器，因为 Let's Encrypt 的 live 路径包含指向 archive 的符号链接：

```bash
docker run -d --name zbproxy --restart unless-stopped \
  --user 0:0 --cap-drop ALL --security-opt no-new-privileges:true \
  -p 443:25565 -p 127.0.0.1:35565:8080 \
  -v /penguin/ZBProxy/data:/data \
  -v /etc/letsencrypt:/certs:ro \
  ghcr.io/sohugepenguin/zbproxy:latest
```

该例假设现有配置和白名单位于宿主机 `/penguin/ZBProxy/data`，现有证书位于 `/etc/letsencrypt`。如果证书来自 `deploy/trojan`，挂载其 `certs` 目录。替换成你实际使用的数据目录或命名卷，保留现有白名单。**不要与当前同名容器同时执行**；先确认并保存现有挂载，再更新原有容器的配置、证书挂载和端口映射。`443:25565` 表示公网 443 对应 JSON 中容器内 `Listen: 25565`。

镜像默认非 root 用户。示例为读取 root 所有的 Certbot 私钥而使用容器 UID 0，同时移除 Linux capabilities。也可为专用非 root UID 配置证书读取权限后使用该 UID；不要将私钥设置为所有用户可读。

修改加密开关、端口和证书后建议重启容器；内置配置重载也会重建监听器并读取证书，已建立连接继续使用旧证书。每次证书续期后执行 `docker restart zbproxy`，现有连接会断开并需重连。旧的外置隧道 `renew.sh` 只负责它自己的 tunnel，不能代替此命令。

## 玩家电脑

复制 `deploy/ZBProxy.client.example.json` 为 `ZBProxy.client.json`：

```json
{
    "EncryptionClient": {
        "Enabled": true,
        "Bind": "127.0.0.1",
        "Listen": 25565,
        "ServerAddress": "64.83.0.57",
        "ServerPort": 443,
        "ServerName": "game.example.com",
        "Password": "与服务端 Encryption.Password 相同"
    }
}
```

本机用同一个 ZBProxy 可执行文件启动：

```bash
./zbproxy -client -config ZBProxy.client.json
```

或在 Linux 电脑用同一个 Docker 镜像，无需单独下载客户端二进制：

```bash
docker run --rm --network host \
  --user "$(id -u):$(id -g)" \
  -v "$PWD/ZBProxy.client.json:/data/client.json:ro" \
  ghcr.io/sohugepenguin/zbproxy:latest \
  -client -config /data/client.json
```

随后 MC 连接 **127.0.0.1:25565**。保持客户端运行。配置文件含隧道密码，请限制文件权限并私下传输。客户端校验服务器证书链和 `ServerName`，没有跳过校验的选项；自建 CA 测试可额外配置 `CACertFile`，Docker 使用时还需挂载该文件。客户端配置修改后重启客户端生效。`EncryptionClient.Enabled` 必须为 true，false 会拒绝启动该模式。

也兼容支持标准 Trojan/TLS TCP 的客户端，例如之前的 sing-box 配置。服务端只支持 CONNECT，不支持 UDP，且忽略客户端请求的目的地址；实际上游仍由已有 ZBProxy 配置决定。客户端不改变系统全局代理，多个游戏连接使用独立 TLS 流。

TLS 会隐藏明文 MC 握手和游戏内容，但内置客户端使用 Go 标准 TLS，不伪装 Chrome 指纹，不提供普通 HTTPS 网站回落，不承诺无法识别。需要 Chrome uTLS 或网站回落时，仍可选择 [外置 sing-box 方案](trojan/README.md)。加密不能修复网络拥塞、物理丢包或不良路由，需用实际游戏操作做对照测试。

实现参考 [Trojan 协议](https://trojan-gfw.github.io/trojan/protocol) 和 [Go crypto/tls](https://pkg.go.dev/crypto/tls)。
