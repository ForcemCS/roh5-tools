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

## SSH 技术详解

整套发布流程只依赖**一条** SSH 连接（到那台 Ubuntu 跳板机），它同时承担两种完全不同的用途：

- **远程命令执行**：在跳板机上跑 `kubectl / makeconf / helmfile`；
- **隧道转发（端口转发）**：把开发机连不到的内网 MySQL / Redis「借道」跳板机访问。

所有实现都在 `ssh.go`（连接、命令、隧道入口）和 `db.go`（MySQL/Redis 如何走隧道）两个文件里，底层用的是官方库 `golang.org/x/crypto/ssh`。

### 1. 建立连接（密码认证）

`dialSSH`（`ssh.go`）用**用户名 + 密码**拨号到跳板机，超时 15 秒：

```go
clientCfg := &ssh.ClientConfig{
    User:            cfg.SSH.User,
    Auth:            []ssh.AuthMethod{ssh.Password(cfg.SSH.Password)},
    HostKeyCallback: ssh.InsecureIgnoreHostKey(), // 内网工具，跳过 host key 校验
    Timeout:         15 * time.Second,
}
ssh.Dial("tcp", addr, clientCfg)
```

拨号成功后，这个 `*ssh.Client` 会被存进一个带读写锁的全局变量 `currentSSH`（`setCurrentSSH`），因为下面 MySQL/Redis 的自定义拨号器是**全局注册一次**的，需要一个可替换的位置来拿到「当前这次发布」的连接。发布结束时 `defer setCurrentSSH(nil)` 把它清空。

> ⚠️ `HostKeyCallback` 用了 `InsecureIgnoreHostKey()`——不校验服务器指纹，因此**只适合可信内网**。若要在不可信网络使用，应改成 `ssh.FixedHostKey(...)` 或 `knownhosts` 校验，否则有中间人风险。

### 2. 远程命令执行（SSH 会话 / exec 通道）

`runRemote`（`ssh.go`）在已建立的连接上开一个 **session 通道**执行命令——这就是 `ssh user@host "命令"` 的程序化等价物：

```go
session, _ := client.NewSession()
// 需要指定目录时拼成: cd "dir" && 命令
session.Start(full)            // 异步启动远端命令
// 同时起两个 goroutine 逐行读取 stdout / stderr，经回调实时回传到网页
session.Wait()                 // 等待退出；非 0 退出码 => 返回 error
```

要点：

- stdout、stderr 各用一个 `goroutine` + `bufio.Scanner` **按行读取**，每读到一行就立刻通过 `log` 回调推给前端的 WebSocket，所以 `helmfile sync` 这种长任务能在网页上**实时滚动**，而不是跑完才一次性出结果。
- Scanner 缓冲区放大到 4MB，避免某些超长行（如 helm 的长输出）触发 `token too long`。
- 命令以非 0 退出码结束时返回 `error`，发布流程随即中止。

发布流程里 `kubectl delete deployment`、`./makeconf update/make`、`helmfile sync` 都是通过它执行的（见 `deploy.go`）。

### 3. SSH 隧道（核心：把内网 DB「借道」访问）

这是本项目最关键的一点：**开发机本身连不到内网的 MySQL / Redis，也不需要装任何客户端**。我们用 SSH 连接的 `Dial` 能力，让数据库的 TCP 流量「钻」过 SSH 通道，由跳板机代为连到内网目标：

```go
// ssh.go —— 通过当前 SSH 连接，把一个 TCP 连接转发到内网目标
func sshDial(network, addr string) (net.Conn, error) {
    c := currentSSH            // 取当前活动连接（加读锁）
    return c.Dial(network, addr)
}
```

`client.Dial` 在 SSH 协议里开的是 **direct-tcpip** 通道，效果等同于命令行的本地端口转发 `ssh -L`，但**不需要在本地监听任何端口**——它直接返回一个 `net.Conn`，谁需要就把谁的流量塞进去。

**MySQL 怎么用上它**（`db.go`）：`go-sql-driver` 支持注册「自定义网络」。我们注册一个名为 `sshtun` 的网络，它的拨号实现就是走 `sshDial`，然后让 DSN 使用这个网络：

```go
mysql.RegisterDialContext("sshtun", func(ctx context.Context, addr string) (net.Conn, error) {
    return sshDial("tcp", addr)          // 所有 MySQL 连接都经隧道
})
dsnCfg.Net  = "sshtun"
dsnCfg.Addr = "10.10.0.204:30016"        // 这是【跳板机视角】的内网地址
```

**Redis 怎么用上它**（`db.go`）：`go-redis` 直接支持自定义 `Dialer`，把它指向 `sshDial` 即可：

```go
redis.NewClient(&redis.Options{
    Addr: "10.10.0.204:6379",            // 同样是跳板机能解析的内网地址
    Dialer: func(ctx context.Context, network, addr string) (net.Conn, error) {
        return sshDial("tcp", addr)
    },
})
```

> 注意：`mysql`/`redis` 配置里填的 host 是**从跳板机出发**能访问到的地址（内网 IP），不是开发机视角的地址。解析与连接都发生在跳板机那一端。

因为隧道里的每条连接都比较「重」，MySQL 连接池被限制为 `SetMaxOpenConns(2)`，连接最长存活 5 分钟。

### 数据流示意

```
开发机 (roh5-tools.exe)
   │
   │  ① 一条 SSH 连接（密码认证）
   ▼
跳板机 (Ubuntu)
   ├─ ② session 通道  ──►  kubectl / makeconf / helmfile   （stdout/stderr 逐行回传）
   │
   └─ ③ direct-tcpip 通道（隧道）
            ├─►  内网 MySQL  10.10.0.204:30016
            └─►  内网 Redis  10.10.0.204:6379
```

一句话总结：**只要能 SSH 到跳板机，就既能在它上面跑命令，又能借它的网络访问内网数据库**——这就是本工具「开发机零依赖」的根本原因。

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
