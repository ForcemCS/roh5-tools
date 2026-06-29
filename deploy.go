package main

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// containsInt 判断 id 是否在列表中。
func containsInt(list []int, id int) bool {
	for _, v := range list {
		if v == id {
			return true
		}
	}
	return false
}

// validateSelected 去重并校验所选服都在可选范围内，返回升序结果。
func validateSelected(selected, selectable []int) ([]int, error) {
	seen := map[int]bool{}
	var out []int
	for _, id := range selected {
		if seen[id] {
			continue
		}
		seen[id] = true
		if !containsInt(selectable, id) {
			return nil, fmt.Errorf("服 %d 不在可清档范围内", id)
		}
		out = append(out, id)
	}
	sort.Ints(out)
	return out, nil
}

// runDeploy 执行完整发布流程；cfg 为服务端固定配置，前端仅提供 tag 与勾选清档的服。
// 所有进度按行通过 log 回传。出错即中止并返回。
func runDeploy(cfg *Config, tag string, selectedIn []int, log func(string)) error {
	tag = strings.TrimSpace(tag)
	if tag == "" {
		return fmt.Errorf("镜像 TAG 不能为空")
	}

	selected, err := validateSelected(selectedIn, cfg.Servers.Selectable)
	if err != nil {
		return err
	}

	log("════════════════════════════════════════")
	log("🚀 ROH5 一键发布开始")
	log(fmt.Sprintf("🎯 镜像 TAG: %s", tag))
	log(fmt.Sprintf("🧩 必更新服: %v", cfg.Servers.Always))
	if len(selected) > 0 {
		log(fmt.Sprintf("🧹 清档+重置开服天数: %v", selected))
	} else {
		log("🧹 本次没有勾选清档的服")
	}
	log("════════════════════════════════════════")

	// 1) 建立 SSH 连接（kubectl/helmfile 远程执行 + MySQL/Redis 隧道）
	log("🔹 连接 SSH 跳板机 ...")
	client, err := dialSSH(cfg)
	if err != nil {
		return err
	}
	defer client.Close()
	setCurrentSSH(client)
	defer setCurrentSSH(nil)
	log("   ✔ SSH 已连接: " + cfg.SSH.User + "@" + cfg.SSH.Host)

	// 2) 打开 MySQL（经隧道）
	log("🔹 连接 MySQL（经 SSH 隧道）...")
	db, err := openMySQL(cfg)
	if err != nil {
		return err
	}
	defer db.Close()
	log("   ✔ MySQL 已连接")

	// 3) 清档+重置：删 deployment → 等 3 秒 → flush redis → 重置 open_time
	if len(selected) > 0 {
		log("\n🔸 步骤一：清档与重置开服天数")

		names := make([]string, len(selected))
		for i, id := range selected {
			names[i] = cfg.Kube.DeploymentPrefix + fmt.Sprint(id)
		}
		delCmd := fmt.Sprintf("kubectl -n %s delete deployments.apps %s",
			cfg.Kube.Namespace, strings.Join(names, " "))
		if err := runRemote(client, "", delCmd, log); err != nil {
			return err
		}

		log("   ⏳ 等待 3 秒让实例下线 ...")
		time.Sleep(3 * time.Second)

		log("   🧨 清空对应 Redis db（编号=服号末位）")
		for _, id := range selected {
			if err := flushRedisDB(cfg, id%10, log); err != nil {
				return err
			}
		}

		log("   🕒 重置 open_time = NOW()")
		if err := resetOpenTime(db, selected, log); err != nil {
			return err
		}
		log("   ✔ 清档与重置完成")
	}

	// 4) 更新 tag（参考原 main.go）
	log("\n🔸 步骤二：更新 T_SERVER.tag")
	if err := updateTag(db, cfg.Servers.Always, tag, log); err != nil {
		return err
	}
	log("   ✔ tag 更新完成")

	// 5) makeconf update / make
	log("\n🔸 步骤三：makeconf update / make")
	if err := runRemote(client, cfg.Paths.Makeconf, "./makeconf update", log); err != nil {
		return err
	}
	if err := runRemote(client, cfg.Paths.Makeconf, "./makeconf make", log); err != nil {
		return err
	}
	log("   ✔ makeconf 完成")

	// 6) helmfile sync（实时日志）
	log("\n🔸 步骤四：helmfile sync")
	if err := runRemote(client, cfg.Paths.Helmfile, "helmfile sync", log); err != nil {
		return err
	}
	log("   ✔ helmfile 同步完成")

	log("\n🎉 所有步骤执行成功")
	return nil
}
