package main

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"
)

// currentSSH 保存当前已建立的 SSH 连接，供 MySQL/Redis 客户端通过隧道拨号使用。
// 因为 go-sql-driver 的自定义网络是全局注册一次的，这里用一个可替换的变量承载实际连接。
var (
	currentSSH   *ssh.Client
	currentSSHMu sync.RWMutex
)

func setCurrentSSH(c *ssh.Client) {
	currentSSHMu.Lock()
	currentSSH = c
	currentSSHMu.Unlock()
}

// sshDial 通过当前 SSH 连接把一个 TCP 连接转发到内网目标（即 SSH 隧道）。
func sshDial(network, addr string) (net.Conn, error) {
	currentSSHMu.RLock()
	c := currentSSH
	currentSSHMu.RUnlock()
	if c == nil {
		return nil, fmt.Errorf("SSH 尚未连接")
	}
	return c.Dial(network, addr)
}

// dialSSH 使用用户名+密码建立到 Ubuntu 跳板机的 SSH 连接。
func dialSSH(cfg *Config) (*ssh.Client, error) {
	if cfg.SSH.Host == "" {
		return nil, fmt.Errorf("SSH 主机不能为空")
	}
	clientCfg := &ssh.ClientConfig{
		User:            cfg.SSH.User,
		Auth:            []ssh.AuthMethod{ssh.Password(cfg.SSH.Password)},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(), // 内网开发工具，跳过 host key 校验
		Timeout:         15 * time.Second,
	}
	addr := fmt.Sprintf("%s:%d", cfg.SSH.Host, cfg.SSH.Port)
	return ssh.Dial("tcp", addr, clientCfg)
}

// runRemote 在远端执行一条命令（可指定工作目录），并把 stdout/stderr 按行实时回传给 log。
// 命令以非 0 退出码结束时返回错误。
func runRemote(client *ssh.Client, dir, cmdline string, log func(string)) error {
	session, err := client.NewSession()
	if err != nil {
		return fmt.Errorf("创建 SSH 会话失败: %w", err)
	}
	defer session.Close()

	full := cmdline
	if dir != "" {
		full = fmt.Sprintf("cd %q && %s", dir, cmdline)
	}
	log("▶ $ " + full)

	stdout, err := session.StdoutPipe()
	if err != nil {
		return err
	}
	stderr, err := session.StderrPipe()
	if err != nil {
		return err
	}

	if err := session.Start(full); err != nil {
		return fmt.Errorf("启动远端命令失败: %w", err)
	}

	var wg sync.WaitGroup
	stream := func(r io.Reader) {
		defer wg.Done()
		sc := bufio.NewScanner(r)
		sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
		for sc.Scan() {
			log(sc.Text())
		}
	}
	wg.Add(2)
	go stream(stdout)
	go stream(stderr)
	wg.Wait()

	if err := session.Wait(); err != nil {
		return fmt.Errorf("远端命令执行失败: %w", err)
	}
	return nil
}
