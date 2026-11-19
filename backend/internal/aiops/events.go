package aiops

import "strings"

type EventDefinition struct {
	Name        string `json:"name"`
	Label       string `json:"label"`
	Description string `json:"description"`
	Group       string `json:"group"`
}

var builtinEvents = []EventDefinition{
	{Name: "task.queued", Label: "任务排队", Description: "任一 Git、部署或启停任务排队时触发。", Group: "任务"},
	{Name: "task.started", Label: "任务开始执行", Description: "任一 Git、部署或启停任务开始执行时触发。", Group: "任务"},
	{Name: "task.success", Label: "任务成功", Description: "任一 Git、部署或启停任务成功时触发。", Group: "任务"},
	{Name: "task.failed", Label: "任务失败", Description: "任一 Git、部署或启停任务失败时触发。", Group: "任务"},
	{Name: "task.cancelled", Label: "任务被取消", Description: "任一 Git、部署或启停任务被取消时触发。", Group: "任务"},
	{Name: "deploy.success", Label: "拉取并部署成功", Description: "拉取并部署任务成功时触发。", Group: "部署与 Git"},
	{Name: "deploy.failed", Label: "拉取并部署失败", Description: "拉取并部署任务失败时触发。", Group: "部署与 Git"},
	{Name: "rebuild.success", Label: "重新构建成功", Description: "重新构建任务成功时触发。", Group: "部署与 Git"},
	{Name: "rebuild.failed", Label: "重新构建失败", Description: "重新构建任务失败时触发。", Group: "部署与 Git"},
	{Name: "git.check.success", Label: "Git 检查成功", Description: "Git 检查任务成功时触发。", Group: "部署与 Git"},
	{Name: "git.check.failed", Label: "Git 检查失败", Description: "Git 检查任务失败时触发。", Group: "部署与 Git"},
	{Name: "git.pull.success", Label: "Git 拉取成功", Description: "Git 拉取任务成功时触发。", Group: "部署与 Git"},
	{Name: "git.pull.failed", Label: "Git 拉取失败", Description: "Git 拉取任务失败时触发。", Group: "部署与 Git"},
	{Name: "git.updated", Label: "发现远程新版本", Description: "检测到远程提交发生变化且与本地版本不同时触发；同一远程版本不会反复触发。", Group: "部署与 Git"},
	{Name: "project.started", Label: "项目启动成功", Description: "针对项目执行的启动、停止或重启任务成功后触发。", Group: "运行操作"},
	{Name: "project.stopped", Label: "项目停止成功", Description: "针对项目执行的启动、停止或重启任务成功后触发。", Group: "运行操作"},
	{Name: "project.restarted", Label: "项目重启成功", Description: "针对项目执行的启动、停止或重启任务成功后触发。", Group: "运行操作"},
	{Name: "project.action_failed", Label: "项目启停或重启失败", Description: "针对项目执行的启动、停止或重启任务失败后触发。", Group: "运行操作"},
	{Name: "service.started", Label: "服务启动成功", Description: "针对服务执行的启动、停止或重启任务成功后触发。", Group: "运行操作"},
	{Name: "service.stopped", Label: "服务停止成功", Description: "针对服务执行的启动、停止或重启任务成功后触发。", Group: "运行操作"},
	{Name: "service.restarted", Label: "服务重启成功", Description: "针对服务执行的启动、停止或重启任务成功后触发。", Group: "运行操作"},
	{Name: "service.action_failed", Label: "服务启停或重启失败", Description: "针对服务执行的启动、停止或重启任务失败后触发。", Group: "运行操作"},
	{Name: "container.started", Label: "容器启动成功", Description: "针对容器执行的启动、停止或重启任务成功后触发。", Group: "运行操作"},
	{Name: "container.stopped", Label: "容器停止成功", Description: "针对容器执行的启动、停止或重启任务成功后触发。", Group: "运行操作"},
	{Name: "container.restarted", Label: "容器重启成功", Description: "针对容器执行的启动、停止或重启任务成功后触发。", Group: "运行操作"},
	{Name: "container.action_failed", Label: "容器启停或重启失败", Description: "针对容器执行的启动、停止或重启任务失败后触发。", Group: "运行操作"},
}

func EventDefinitions() []EventDefinition { return append([]EventDefinition{}, builtinEvents...) }
func reservedEvent(name string) bool {
	for _, prefix := range []string{"task.", "deploy.", "rebuild.", "git.", "project.", "service.", "container."} {
		if strings.HasPrefix(name, prefix) {
			return true
		}
	}
	return false
}
func validEvent(name string) bool {
	if name == "" || len(name) > 100 {
		return false
	}
	for _, r := range name {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '.' || r == '_' || r == '-') {
			return false
		}
	}
	if !reservedEvent(name) {
		return true
	}
	for _, event := range builtinEvents {
		if event.Name == name {
			return true
		}
	}
	return false
}
