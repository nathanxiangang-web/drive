# drive
解决网盘下载需要客户端问题，可使用115云下载，ui操作接近真实网盘，支持302直链下载，播放视频。

## CloudDrive

面向桌面和手机浏览器的网盘文件管理器。目录、文件大小和云下载状态来自 115，播放和下载使用 OpenList 返回的短期 CDN 地址。CloudDrive 使用独立账号和 PostgreSQL 数据库。

| 模块 | 职责 |
| --- | --- |
| `internal/cloud` | 调用 115 读取目录和提交任务。独立的 OpenList 适配器校验文件和 CDN 地址。 |
| `internal/download` | 校验链接、提交一次、核对任务归属并确认结果已进入目标目录。 |
| `internal/store` | 保存账号、会话和任务状态，约束重复提交。 |
| `internal/site` | 服务端目录授权与 HTTP 接口，以及桌面和手机界面。 |

文件列表最多缓存 20 秒。工具栏「刷新」会重新向 115 读取当前页，并合并短时间内的重复刷新。每个账号有自己的 `DriveSpaces/user-…` 目录，每个下载有独立目标目录。状态不明的任务保留记录并等待核对，避免重复向 115 提交。

### 构建与部署

```sh
go test ./...
go vet ./...
node --check internal/site/web/app.js
CGO_ENABLED=0 go build -trimpath -o deploy/drive ./cmd/drive
docker build -t cloudsite-drive:local deploy
```

`deploy/compose.yaml` 适用于现有 Traefik、PostgreSQL 和 OpenList 网络。通过环境变量配置 HTTPS 域名、115 文件根目录、OpenList 路径和精确的 CDN 主机名。为应用创建独立数据库。数据库 URL、OpenList 读取令牌和 115driver TOML 作为只读文件挂载，须由管理员放置在服务器的私有目录。OpenList 服务账号根目录须与 115 的公开根目录对应，并允许读取应用创建的用户目录。

将单独的密码文件挂载给一次性容器，用 `drive provision <login> <password-file>` 创建账号。TLS 终止后由反向代理转发到容器端口 8098。健康检查仅核对程序与数据库连接。对接测试需要登录后核验目录、CDN 响应和云下载结果。
