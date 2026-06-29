# ROH5 一键发布工具

把原来的 `main.go` 命令行脚本改造成了一个**桌面工具**：双击 exe → 自动开浏览器 → 在网页上点几下完成发布。开发机不需要 `kubectl/helm/mysql/redis` 任何命令行客户端，只要能 SSH 到那台 Ubuntu 跳板机即可。

> **连接信息（SSH / MySQL / Redis 凭据）由 `config.yaml` 固定写死，界面上看不到也改不了。** 开发人员在网页上只能做两件事：填镜像 TAG、勾选要清档的服。这样防止乱填，也避免密码暴露在网页上。

## 它做了什么

1. **必更新服**（每次都更新镜像 tag）：`10004 10005 10006 10007 10008 10009` + 战斗服 `10989`
2. **可选清档**：界面勾选要清档并重置开服天数的服（从 `10004~10009` 中选）。对每个勾选的服 `S`：
   1. `kubectl -n roh5 delete deployments.apps game-roh5-server-<S>`
   2. 等 3 秒 → flush 对应 Redis db（**db 号 = 服号末位**，如 `10008` → db `8`）
   3. 批量 `UPDATE T_SERVER SET open_time = NOW() WHERE id IN (...勾选的服...)`
3. 界面输入镜像 tag（如 `202606280348-513`）→ `UPDATE T_SERVER SET tag = ?`（必更新服）
4. 远端执行 `./makeconf update` / `./makeconf make` → `helmfile sync`
5. **helmfile sync 等所有远端日志实时显示在网页上**

## 架构

- 纯 Go，前端用 `embed` 打进**单个 exe**，离线可用。
- **MySQL / Redis 经 SSH 隧道**：Go 客户端的 TCP 连接通过 `ssh.Dial` 转发到跳板机再到内网 DB，开发机本身访问不到 DB 也没关系。
- `kubectl / makeconf / helmfile` 通过 SSH 会话在跳板机上执行，stdout/stderr 按行经 WebSocket 实时回传。
- 同一时刻只允许一次发布（共享全局 SSH 隧道）。
- **凭据只在服务端**：`config.yaml` 加载进内存后，前端发布只传 `tag` + 勾选的服；`/api/meta` 仅返回服列表，绝不下发任何密码。

## 使用

**给开发人员（不碰配置）**

1. 把 `roh5-tools.exe` **和填好的 `config.yaml`** 一起发给开发人员，放同一目录。
2. 双击 `roh5-tools.exe`，会自动打开浏览器；没自动打开就手动访问控制台里打印的地址。
3. 在网页上填镜像 TAG、勾选要清档的服、点「开始发布」，弹确认框确认后，右侧看实时日志。

> 关闭运行 exe 的那个黑窗口即退出工具。界面上**不显示**任何连接信息。

**维护者（准备 config.yaml）**

- 首次运行时，若同目录没有 `config.yaml`，会自动生成一份带默认值的模板。把 SSH 主机/用户名/密码、Redis 密码等按实际环境填死，再连同 exe 一起分发。
- `config.yaml` 含明文密码，**只发给该看到的人**，不要提交到代码库。

## 构建

```bash
go build -o roh5-tools.exe .
```

交叉编译给 Linux 开发机（如果有人直接在 Linux 桌面上跑）：

```bash
GOOS=linux GOARCH=amd64 go build -o roh5-tools .
```

## 配置项（config.yaml）

| 段 | 说明 |
|----|------|
| `ssh` | 跳板机 host/port/user/password（密码认证） |
| `mysql` | 经隧道连接的 MySQL（默认 `10.10.0.204:30016` / `db_ro3_server`） |
| `redis` | 经隧道连接的 Redis（默认 `10.10.0.204:6379`，用户名/密码按需填） |
| `paths.makeconf` / `paths.helmfile` | 跳板机上的远端目录 |
| `kube.namespace` / `kube.deployment_prefix` | 默认 `roh5` / `game-roh5-server-` |
| `servers.always` / `servers.selectable` | 必更新服 / 可清档服列表 |
| `web.port` | 本地网页端口（被占用或落在系统保留段时自动换随机空闲端口） |

> ⚠️ `config.yaml` 含明文密码，注意不要提交到代码库 / 不要随便外发。
