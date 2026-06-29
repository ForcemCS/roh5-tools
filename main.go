package main

import (
	"fmt"
	"log"
	"net"
	"net/http"
	"os/exec"
	"runtime"
	"time"
)

func main() {
	cfg, path, err := loadConfig()
	if err != nil {
		log.Fatalf("加载配置失败: %v", err)
	}

	port := cfg.Web.Port
	if port == 0 {
		port = 8765
	}

	// 优先用配置端口；若被占用或落在系统保留段（Windows 常见），退回系统分配的空闲端口。
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		fmt.Printf("   ⚠ 端口 %d 不可用（%v），改用随机空闲端口\n", port, err)
		ln, err = net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			log.Fatalf("无法监听本地端口: %v", err)
		}
	}
	addr := ln.Addr().String()
	url := "http://" + addr

	srv := newServer(cfg)

	fmt.Println("========================================")
	fmt.Println("   🚀 ROH5 一键发布工具")
	fmt.Println("========================================")
	fmt.Printf("   配置文件: %s\n", path)
	fmt.Printf("   控制台地址: %s\n", url)
	fmt.Println("   浏览器未自动打开时，请手动访问上面的地址")
	fmt.Println("   关闭本窗口即退出工具")
	fmt.Println("========================================")

	go func() {
		time.Sleep(500 * time.Millisecond)
		openBrowser(url)
	}()

	if err := http.Serve(ln, srv.routes()); err != nil {
		log.Fatalf("HTTP 服务异常: %v", err)
	}
}

// openBrowser 尝试用系统默认浏览器打开地址（Windows/macOS/Linux）。
func openBrowser(url string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("cmd", "/c", "start", "", url)
	case "darwin":
		cmd = exec.Command("open", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	_ = cmd.Start()
}
