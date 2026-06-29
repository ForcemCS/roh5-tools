package main

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// Config 是工具的全部连接信息与可调参数，存储在可执行文件同目录的 config.yaml。
type Config struct {
	SSH struct {
		Host     string `yaml:"host" json:"host"`
		Port     int    `yaml:"port" json:"port"`
		User     string `yaml:"user" json:"user"`
		Password string `yaml:"password" json:"password"`
	} `yaml:"ssh" json:"ssh"`

	MySQL struct {
		Host     string `yaml:"host" json:"host"`
		Port     int    `yaml:"port" json:"port"`
		User     string `yaml:"user" json:"user"`
		Password string `yaml:"password" json:"password"`
		Database string `yaml:"database" json:"database"`
	} `yaml:"mysql" json:"mysql"`

	Redis struct {
		Host     string `yaml:"host" json:"host"`
		Port     int    `yaml:"port" json:"port"`
		Username string `yaml:"username" json:"username"`
		Password string `yaml:"password" json:"password"`
	} `yaml:"redis" json:"redis"`

	Paths struct {
		Makeconf string `yaml:"makeconf" json:"makeconf"`
		Helmfile string `yaml:"helmfile" json:"helmfile"`
	} `yaml:"paths" json:"paths"`

	Kube struct {
		Namespace        string `yaml:"namespace" json:"namespace"`
		DeploymentPrefix string `yaml:"deployment_prefix" json:"deployment_prefix"`
	} `yaml:"kube" json:"kube"`

	Servers struct {
		// Always 是每次必更新 tag 的服（含战斗服 10989）。
		Always []int `yaml:"always" json:"always"`
		// Selectable 是界面上可勾选「清档+重置开服天数」的服。
		Selectable []int `yaml:"selectable" json:"selectable"`
	} `yaml:"servers" json:"servers"`

	Web struct {
		Port int `yaml:"port" json:"port"`
	} `yaml:"web" json:"web"`
}

// defaultConfig 提供来自原 main.go 的已知默认值，未知项留空待界面填写。
func defaultConfig() *Config {
	c := &Config{}

	c.SSH.Host = ""
	c.SSH.Port = 22
	c.SSH.User = "root"
	c.SSH.Password = "xxx"

	c.MySQL.Host = ""
	c.MySQL.Port = 30016
	c.MySQL.User = "root"
	c.MySQL.Password = "xxx"
	c.MySQL.Database = "xxx"

	c.Redis.Host = ""
	c.Redis.Port = 6379
	c.Redis.Username = ""
	c.Redis.Password = ""

	c.Paths.Makeconf = "/root/new_server/roh5_new"
	c.Paths.Helmfile = "/root/helmfile_roh5"

	c.Kube.Namespace = "roh5"
	c.Kube.DeploymentPrefix = "game-roh5-server-"

	c.Servers.Always = []int{10004, 10005, 10006, 10007, 10008, 10009, 10989}
	c.Servers.Selectable = []int{10004, 10005, 10006, 10007, 10008, 10009}

	c.Web.Port = 17653
	return c
}

// configPath 优先使用可执行文件所在目录，便于双击运行；失败时退回当前工作目录。
func configPath() string {
	exe, err := os.Executable()
	if err == nil {
		return filepath.Join(filepath.Dir(exe), "config.yaml")
	}
	return "config.yaml"
}

// loadConfig 读取配置；若文件不存在则写入一份默认配置并返回。
func loadConfig() (*Config, string, error) {
	path := configPath()
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		cfg := defaultConfig()
		if werr := saveConfig(cfg); werr != nil {
			return nil, path, fmt.Errorf("写入默认配置失败: %w", werr)
		}
		return cfg, path, nil
	}
	if err != nil {
		return nil, path, fmt.Errorf("读取配置失败: %w", err)
	}

	cfg := defaultConfig()
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, path, fmt.Errorf("解析配置失败: %w", err)
	}
	return cfg, path, nil
}

// saveConfig 把配置写回 config.yaml。
func saveConfig(cfg *Config) error {
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return err
	}
	return os.WriteFile(configPath(), data, 0o600)
}
