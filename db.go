package main

import (
	"context"
	"database/sql"
	"fmt"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/go-sql-driver/mysql"
	"github.com/redis/go-redis/v9"
)

// mysqlNet 是注册给 go-sql-driver 的自定义网络名，所有连接都经 SSH 隧道转发。
const mysqlNet = "sshtun"

var mysqlNetOnce sync.Once

// registerMySQLTunnel 全局注册一次自定义网络；拨号时取当前 SSH 连接做转发。
func registerMySQLTunnel() {
	mysqlNetOnce.Do(func() {
		mysql.RegisterDialContext(mysqlNet, func(ctx context.Context, addr string) (net.Conn, error) {
			return sshDial("tcp", addr)
		})
	})
}

// openMySQL 通过 SSH 隧道打开 MySQL 连接池。
func openMySQL(cfg *Config) (*sql.DB, error) {
	registerMySQLTunnel()

	dsnCfg := mysql.NewConfig()
	dsnCfg.User = cfg.MySQL.User
	dsnCfg.Passwd = cfg.MySQL.Password
	dsnCfg.Net = mysqlNet
	dsnCfg.Addr = fmt.Sprintf("%s:%d", cfg.MySQL.Host, cfg.MySQL.Port)
	dsnCfg.DBName = cfg.MySQL.Database
	dsnCfg.Params = map[string]string{"charset": "utf8mb4"}
	dsnCfg.ParseTime = true
	dsnCfg.Loc = time.Local
	dsnCfg.Timeout = 10 * time.Second

	db, err := sql.Open("mysql", dsnCfg.FormatDSN())
	if err != nil {
		return nil, fmt.Errorf("连接 MySQL 失败: %w", err)
	}
	// 隧道连接较重，限制并发连接数。
	db.SetMaxOpenConns(2)
	db.SetConnMaxLifetime(5 * time.Minute)

	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, fmt.Errorf("无法 ping 通 MySQL: %w", err)
	}
	return db, nil
}

// inClause 构造形如 "(?, ?, ?)" 的占位符及对应参数。
func inClause(ids []int) (string, []any) {
	marks := make([]string, len(ids))
	args := make([]any, len(ids))
	for i, id := range ids {
		marks[i] = "?"
		args[i] = id
	}
	return "(" + strings.Join(marks, ", ") + ")", args
}

// updateTag 更新必更新服列表的镜像 tag（对应原 main.go 行为）。
func updateTag(db *sql.DB, ids []int, tag string, log func(string)) error {
	in, args := inClause(ids)
	args = append([]any{tag}, args...)
	q := "UPDATE T_SERVER SET tag = ? WHERE id IN " + in
	log("   SQL> " + q + fmt.Sprintf("  [tag=%s]", tag))
	res, err := db.Exec(q, args...)
	if err != nil {
		return fmt.Errorf("更新 tag 失败: %w", err)
	}
	n, _ := res.RowsAffected()
	log(fmt.Sprintf("   📊 受影响行数: %d", n))
	return nil
}

// resetOpenTime 把被选中清档的服的开服时间重置为当前时间。
func resetOpenTime(db *sql.DB, ids []int, log func(string)) error {
	if len(ids) == 0 {
		return nil
	}
	in, args := inClause(ids)
	q := "UPDATE T_SERVER SET open_time = NOW() WHERE id IN " + in
	log("   SQL> " + q)
	res, err := db.Exec(q, args...)
	if err != nil {
		return fmt.Errorf("重置 open_time 失败: %w", err)
	}
	n, _ := res.RowsAffected()
	log(fmt.Sprintf("   📊 受影响行数: %d", n))
	return nil
}

// flushRedisDB 清空指定编号的 Redis db（编号 = 服号末位），通过 SSH 隧道连接。
func flushRedisDB(cfg *Config, dbNum int, log func(string)) error {
	rdb := redis.NewClient(&redis.Options{
		Addr:     fmt.Sprintf("%s:%d", cfg.Redis.Host, cfg.Redis.Port),
		Username: cfg.Redis.Username,
		Password: cfg.Redis.Password,
		DB:       dbNum,
		Dialer: func(ctx context.Context, network, addr string) (net.Conn, error) {
			return sshDial("tcp", addr)
		},
	})
	defer rdb.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()

	log(fmt.Sprintf("   FLUSHDB -n %d", dbNum))
	if err := rdb.FlushDB(ctx).Err(); err != nil {
		return fmt.Errorf("清空 redis db %d 失败: %w", dbNum, err)
	}
	return nil
}
