package main

import (
	"bufio"
	"fmt"
	"io"
	"strconv"
	"sync"

	"golang.org/x/crypto/ssh"
)

// logTailLines 是开始跟踪日志时先拉取的历史行数，给一点上下文。
const logTailLines = 200

// streamServerLogs 通过给定 SSH 连接执行 `kubectl logs -f` 实时跟踪某游戏服日志。
// 关闭该 SSH 连接（client.Close）即可终止远端 kubectl，从而使本函数返回。
func streamServerLogs(client *ssh.Client, cfg *Config, serverID int, log func(string)) error {
	session, err := client.NewSession()
	if err != nil {
		return fmt.Errorf("创建 SSH 会话失败: %w", err)
	}
	defer session.Close()

	dep := cfg.Kube.DeploymentPrefix + strconv.Itoa(serverID)
	cmd := fmt.Sprintf("kubectl -n %s logs -f --tail=%d deployments/%s",
		cfg.Kube.Namespace, logTailLines, dep)
	log("▶ $ " + cmd)

	stdout, err := session.StdoutPipe()
	if err != nil {
		return err
	}
	stderr, err := session.StderrPipe()
	if err != nil {
		return err
	}

	if err := session.Start(cmd); err != nil {
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

	// 连接被关闭或远端结束时 Wait 返回；跟踪日志被主动停止属正常，忽略其错误。
	_ = session.Wait()
	return nil
}
