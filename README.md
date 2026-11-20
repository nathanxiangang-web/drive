# Drive

Drive 是一个面向桌面和手机浏览器的自托管网盘入口。它针对网盘下载中常见的会员门槛、非会员分享下载限速和客户端安装要求，让用户打开网页就能下载或播放站点提供的文件，也能把下载任务交给 115 后台处理。

## 核心功能

| 功能 | 使用方式 | 当前状态 |
| --- | --- | --- |
| 文件下载 | 在站内点击「下载到设备」，浏览器通过 HTTP 302 跳转到 OpenList 返回的短期 CDN 直链。 | 已支持 |
| 视频播放 | 在网页播放器打开视频，通过 HTTP 302 跳转到 CDN，由浏览器直接播放。 | 已支持 |
| 115 云下载 | 粘贴链接并提交，115 在后台执行。完成后可在「我的空间」找到文件。 | 已支持 |
| 分享文件下载 | 在「更多」中创建短链接，选择 1、7、30 天有效期并填写 4 位提取码。接收者用提取码打开文件，下载和播放通过 HTTP 302 跳转到 CDN。分享者可在账号菜单的「我的分享」复制、查看或撤销链接。管理员改码后，接收者用新码打开原链接。 | 已支持 |

文件下载和视频播放的流量由 CDN 承载，Drive 负责解析直链并返回跳转。实际速度取决于上游网盘账号的服务权益、CDN 响应和用户网络。云下载能力取决于站点接入的 115 账号。

## 镜像安装

需要 Docker Compose、OpenSSL、一个能从 Drive 容器访问的 OpenList、其对应的 115driver 配置文件，以及站点的 HTTPS 反代。OpenList 使用的网盘目录应与 115driver 读取的文件一致。Compose 会创建 Drive 和 PostgreSQL。

```sh
cd deploy/standalone
sh prepare.sh https://drive.example.com https://pan.example.com /path/to/115driver/config.toml
docker compose up -d --wait
```

将 `https://drive.example.com` 反代到宿主机的 `127.0.0.1:8098`。使用 `admin` 和 `private/admin-password` 中的初始密码登录。首次登录会打开「网盘接入」，填写 OpenList 账号、密码和对应文件根目录，保存后即可浏览。「站点状态」可查看数据库、OpenList 和最近一次 115 目录读取结果。

默认使用 `ghcr.io/nathanxiangang-web/drive:1.0.0`，首次发布后可直接拉取。源码验证可在仓库根目录运行 `docker build -t drive:local .`，再在安装目录的 `.env` 中设置 `DRIVE_IMAGE=drive:local`。同一安装目录再次运行准备脚本会保留配置。升级时修改版本并运行 `docker compose up -d --wait`。更换服务器时备份 PostgreSQL 数据和 `private/`，其中包含网盘接入密钥。

## 项目结构

目录、文件大小和云下载状态来自 115。Drive 使用独立账号和 PostgreSQL 数据库。

| 模块 | 职责 |
| --- | --- |
| `internal/cloud` | 调用 115 读取目录和提交任务。独立的 OpenList 适配器校验文件和 CDN 地址。 |
| `internal/download` | 校验链接、提交一次、核对任务归属并确认结果已进入目标目录。 |
| `internal/store` | 保存账号、会话和任务状态，约束重复提交。 |
| `internal/site` | 服务端目录授权与 HTTP 接口，以及桌面和手机界面。 |

文件列表最多缓存 20 秒。工具栏「刷新」会重新向 115 读取当前页，并合并短时间内的重复刷新。文件名搜索支持当前目录及子目录，搜索结果的目录归属由服务端独立核对。新下载直接写入 `DriveSpace/<用户名>`，网盘目录 ID 与账号绑定。旧任务路径保留，云空间页面直接列出旧目录里的文件。状态不明的任务保留记录并等待核对，避免重复向 115 提交。

### 源码与现有生产环境

```sh
go test ./...
go vet ./...
node --check internal/site/web/app.js
CGO_ENABLED=0 go build -trimpath -o deploy/drive ./cmd/drive
docker build -t cloudsite-drive:local deploy
```

`deploy/compose.yaml` 适用于现有 Traefik、PostgreSQL 和 OpenList 网络。通过环境变量配置 HTTPS 域名、115 文件根目录、OpenList 路径和精确的 CDN 主机名。为应用创建独立数据库。数据库 URL、OpenList 读取令牌和 115driver TOML 作为只读文件挂载，须由管理员放置在服务器的私有目录。OpenList 服务账号根目录须与 115 的公开根目录对应，并允许读取应用创建的用户目录。

将单独的密码文件挂载给一次性容器，用 `drive provision-admin <login> <password-file>` 创建首个管理员。TLS 终止后由反向代理转发到容器端口 8098。健康检查仅核对程序与数据库连接。对接测试需要登录后核验目录、CDN 响应和云下载结果。


### 同站管理与初始接入

使用首个管理员账号登录后，从「管理」查看站点、PostgreSQL、OpenList、最近的 115 目录读取以及服务器资源。管理员可创建账号、分配权限和停用账号。只读账号可以浏览和下载现有文件，可云下载账号还可提交任务。管理员可操作管理页，各账号的私人目录依旧独立。

新安装请配置 `private/openlist-key` 为 32 字节随机值的十六进制文本，权限仅限应用进程。使用「管理 → OpenList 接入」验证账号、密码及服务账号可读的文件根目录。根目录填写相对于该 OpenList 账号可见的路径，一般是 `/` 或 `/115网盘/CloudSite`。验证成功才替换当前接入。密码由 AES-GCM 加密后保存在 PostgreSQL，密钥单独挂载；恢复时要同时保留密钥与数据库。已经托管 OpenList 令牌的安装可以用现有令牌启动。


部署默认开放注册，使用 `DRIVE_REGISTER=false` 可关闭。公开用户名只接受英文字母和数字，允许纯数字。密码至少 6 字符并同时含英文和数字。注册成功后使用可云下载权限。带内容指纹的静态文件可长期缓存并通过 gzip 传输，登录页根据 ETag 协商更新，账号、文件清单和临时下载地址均禁止浏览器共享缓存。

### 公网部署与备份

`deploy/stack` 是 CloudDrive、OpenList、PostgreSQL、Traefik 和备份服务的 Compose 配置。CloudDrive 使用 `cloud.netioi.com`，OpenList 使用 `pan.netioi.com`。部署目录在 `/opt/my-servers`，CloudDrive 的 Dockerfile 与 Linux amd64 可执行文件放在 `drive/artifact`，备份服务的 115driver v1.3.5 与许可证放在 `backup`。私有配置由管理员在主机保存。

备份仅收录运行所需的文件、CloudDrive 逻辑数据库和 OpenList SQLite 快照。每天北京时间 03:00 上传到 115 的 `/Backup`，迁移步骤见 [备份与恢复](deploy/stack/backup/恢复说明.md)。


云下载保留粘贴链接中的 Tracker 和其他参数，仅还原从网页复制的磁力链接分隔符。每次明确提交都交给 115 决定，重复网络请求按提交编号保持幂等。任务列表展示 115 返回的进度，允许清除单条或清空显示记录。清除记录后，已经提交的任务仍然执行，结果依旧保存在云空间。


管理页可查找、创建、编辑和删除普通网站账号。用户名、密码和权限更新会使该账号的会话失效。网盘目录名和任务归属与账号内部标识绑定。删除账号会撤销访问权限并保留网盘目录和文件，管理员账号受保护。
