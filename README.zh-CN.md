<div align="center">

<img alt="RunWisp 标志" src="packages/ui/static/runwisp_c.svg" width="120">

# RunWisp

**看清运行了什么、何时运行、为什么失败，以及输出了什么。**

RunWisp 是一个开源、自托管的 cron 任务管理器和进程守护工具。它以单个 Go 二进制文件发布，内置 Web 控制台、终端界面和 REST API。

[runwisp.com](https://runwisp.com) · [文档](https://docs.runwisp.com) · [安装](#安装) · [快速开始](#快速开始) · [对比](#对比)

[English](README.md) · [Deutsch](README.de.md) · **简体中文**

[![License: GPL-3.0](https://img.shields.io/badge/License-GPL--3.0-blue.svg)](LICENSE)
[![Latest Release](https://img.shields.io/github/v/release/runwisp/runwisp?include_prereleases&sort=semver&color=00ADD8)](https://runwisp.com/releases/)
[![Mentioned in Awesome Go](https://awesome.re/mentioned-badge.svg)](https://github.com/avelino/awesome-go)
[![Coverage](https://codecov.io/gh/runwisp/runwisp/graph/badge.svg)](https://app.codecov.io/gh/runwisp/runwisp)
[![Security Rating](https://sonarcloud.io/api/project_badges/measure?project=runwisp_runwisp&metric=security_rating)](https://sonarcloud.io/summary/new_code?id=runwisp_runwisp)
[![GitHub Stars](https://img.shields.io/github/stars/runwisp/runwisp?style=social)](https://github.com/runwisp/runwisp)

</div>

---

当你想把定时任务和常驻服务放在一处管理时，RunWisp 可以替代 `crond` 和 `supervisord`。两者都写在 `runwisp.toml` 里；守护进程负责运行它们，只占用约 30 MB 内存，不需要 Python、Node.js 或外部数据库。

> 想先看看？运行 `bunx runwisp demo`，用演示数据体验 RunWisp，无需任何配置。

每次运行都会留下记录：退出码、耗时、时间戳以及 stdout/stderr。你可以在 Web 控制台、终端界面或 REST API 中查看历史、实时跟踪日志、触发或停止任务。任务定义始终以 TOML 形式保存在磁盘上。

<div align="center">
<img alt="RunWisp Web 控制台，显示一次任务运行、实时日志和失败详情" src="apps/docs/src/assets/screenshots/runwisp-demo.webp" width="780">
<p><em>在内置控制台中触发运行、跟踪输出并排查失败。</em></p>
</div>

## 安装

```bash
curl -fsSL https://get.runwisp.com | sh
```

或者选择其他方式：

| 🍺<br>[Homebrew](https://runwisp.com/releases/?utm_source=github&utm_medium=readme-zh&utm_content=homebrew) | 🐧<br>[Debian / Ubuntu](https://runwisp.com/releases/?utm_source=github&utm_medium=readme-zh&utm_content=deb) | 🎩<br>[Fedora / RHEL](https://runwisp.com/releases/?utm_source=github&utm_medium=readme-zh&utm_content=rpm) | 📦<br>[npm / Bun](https://runwisp.com/releases/?utm_source=github&utm_medium=readme-zh&utm_content=npm) | 🐳<br>[Docker](https://runwisp.com/releases/?utm_source=github&utm_medium=readme-zh&utm_content=docker) | 🗜️<br>[压缩包](https://runwisp.com/releases/?utm_source=github&utm_medium=readme-zh&utm_content=tarball) |
| :---------------------------------------------------------------------------------------------------------: | :-----------------------------------------------------------------------------------------------------------: | :---------------------------------------------------------------------------------------------------------: | :-----------------------------------------------------------------------------------------------------: | :-----------------------------------------------------------------------------------------------------: | :------------------------------------------------------------------------------------------------------: |

从源码构建请参阅贡献指南中的 [Getting started](CONTRIBUTING.md#getting-started)。

<div align="center">
<img alt="RunWisp 终端界面，通过 SSH 显示任务、实时输出和运行控制" src="apps/docs/src/assets/screenshots/tui-demo.webp" width="780">
<p><em>在 SSH 会话中，终端界面集中展示任务状态、日志、历史记录和运行控制。</em></p>
</div>

## 快速开始

1. 在你希望存放配置的目录中启动 RunWisp：

   ```bash
   runwisp
   ```

   如果还没有 `runwisp.toml`，RunWisp 会提示为你创建一份。按 `Enter` 后，它会写入一个示例任务，在后台启动守护进程，并打开终端界面。

2. 运行示例任务。在侧边栏中选中 `hello`，然后按 `r`。

   输出会实时显示，这次运行会连同退出码、耗时和全部输出一起保留在历史记录中。

3. 打开 Web 控制台。在终端界面的首页选中 “Open Web UI” 并按 `Enter`。浏览器会打开 `http://localhost:9477`，并且已经自动登录。

4. 在 `runwisp.toml` 中把示例替换成你自己的任务和服务：

   ```toml
   [tasks.backup-db]
   cron       = "0 2 * * *"
   jitter     = "30m"  # may start up to 30m late while other jittered jobs run
   on_overlap = "kill" # if yesterday's job is still running, kill it
   keep_runs  = 30     # keep the last 30 runs in the history
   run        = "pg_dump mydb | gzip > /backups/mydb-$(date +%F).sql.gz"

   [services.worker]
   instances    = 3                           # keep three copies running
   env          = { NODE_ENV = "production" } # shown in API/UI
   secrets_file = "/etc/runwisp/worker.env"   # values never shown in API/UI
   run          = "node /app/worker.js"
   ```

5. 应用更改，然后立即试运行这个任务，不必等到 02:00：

   ```bash
   runwisp validate        # check the file, start nothing
   runwisp reload          # apply it; a file with an error is rejected and nothing changes
   runwisp run backup-db   # run it now
   runwisp logs backup-db  # what the last run printed
   ```

6. 让 RunWisp 在系统重启后自动启动（支持 systemd 和 launchd）：

   ```bash
   sudo runwisp service install   # on macOS: runwisp service install --local
   ```

首页还会显示一个自动生成的密码，用于从其他浏览器登录。设置 `RUNWISP_PASSWORD` 即可自己指定密码。再设置 `RUNWISP_VIEWER_PASSWORD` 后，用它登录的人可以[查看所有内容但无法修改](https://docs.runwisp.com/operations/auth/#read-only-access)。在可信的本地环境中，可以用 [`RUNWISP_AUTH=off`](https://docs.runwisp.com/operations/auth/#running-without-a-password) 明确关闭身份验证。

> **无终端运行？** 在 cron、systemd、Docker、CI 或管道化的 SSH 会话中使用 `runwisp daemon`。它不会弹出提示，缺少配置时以非零状态退出。

接下来可以阅读：[你的第一个任务](https://docs.runwisp.com/getting-started/first-task/)、[你的第一个服务](https://docs.runwisp.com/getting-started/first-service/)，以及[超时、重试和失败告警](https://docs.runwisp.com/getting-started/failures/)（英文）。

## 从 cron、supervisord 或 Docker Compose 迁移

RunWisp 可以直接沿用你现有的配置：

- [从 cron 接管](https://docs.runwisp.com/coming-from/cron/)。
- [导入 crontab](https://docs.runwisp.com/coming-from/cron/#convert-by-hand)。
- [导入 supervisord](https://docs.runwisp.com/coming-from/supervisord/)。
- [导入 Docker Compose](https://docs.runwisp.com/coming-from/docker-compose/)。
- [用 `@runwisp/js` 替换 node-cron](https://docs.runwisp.com/coming-from/node-cron/)。
- [用 `runwisp/runwisp` 运行 Laravel、Symfony 或 CodeIgniter 的计划任务](https://docs.runwisp.com/sdk/php/)。

## 功能

**调度与守护**

- 标准 cron 调度、错过运行的处理、重试、超时，以及 `queue`、`skip`、`kill` 三种重叠策略
- 常驻服务支持多实例、重启退避、崩溃恢复和优雅关闭。每天 50 万行输出、连续数月不间断，RunWisp 都能承受并实时推送给你。
- 手动运行时可传入带类型的参数，通过 UI/API/CLI 以命令行参数或环境变量的形式传递

**运行历史与日志**

- 退出码、耗时、时间戳和状态持久化在内嵌的 SQLite 中
- 在 Web 界面和终端界面中实时查看 stdout/stderr
- 按任务的日志轮转，以及通过 Slack、Discord、Telegram、ntfy、Gotify、Pushover、电子邮件、Webhook 或应用内收件箱发送失败通知

**日常运维**

- 单个二进制文件，内嵌 Svelte Web 控制台和 SQLite
- TOML 配置，可以像代码一样做版本管理和评审
- 先校验再生效的重载、崩溃或断电后可恢复的状态，以及重启时报告被中断的运行
- 本地优先，无需注册、账号或必需的网络连接。默认唯一的出站请求是匿名的更新检查，可用 [`check_updates = false`](https://docs.runwisp.com/configuration/daemon/#check_updates) 关闭

## 对比

|            | crond   | systemd timers    | supervisord | **RunWisp**                                                             |
| ---------- | ------- | ----------------- | ----------- | ----------------------------------------------------------------------- |
| Cron 调度  | 是      | 是                | 否          | **是**                                                                  |
| 进程守护   | 否      | 是                | 是          | **是**                                                                  |
| Web 控制台 | 否      | 否                | 简单 HTML   | **内置**                                                                |
| 终端界面   | 否      | 否                | 否          | **内置**                                                                |
| 远程接口   | 否      | D-Bus             | XML-RPC     | **REST API**                                                            |
| 实时日志流 | 否      | `journalctl -f`   | 仅 tail     | **Web 界面、TUI 和 SSE**                                                |
| 并发策略   | 否      | 防止重叠          | 否          | **排队、跳过或终止**                                                    |
| 失败告警   | 否      | `OnFailure=` 单元 | 事件监听器  | **Slack、Discord、Telegram、ntfy、Gotify、Pushover、电子邮件、Webhook** |
| 执行历史   | 否      | `journalctl`      | 否          | **SQLite，可在界面中浏览**                                              |
| 运行时依赖 | libc    | systemd           | Python      | **无**                                                                  |
| 配置方式   | crontab | Unit 文件         | INI 文件    | **一个 TOML 文件**                                                      |

API 和用户界面可以查看任务、触发或停止任务。**任务定义无法远程修改。** `runwisp.toml` 始终是唯一的事实来源。

## 链接

- [文档](https://docs.runwisp.com)：安装、配置、REST API 和运维（英文）
- [更新日志](CHANGELOG.md)：每个版本的变更内容
- [贡献指南](CONTRIBUTING.md)：想从源码构建或提交 PR 时参阅
- [安全策略](SECURITY.md)：发现安全漏洞时参阅
- [问题追踪](https://github.com/runwisp/runwisp/issues)：报告错误和提出功能建议

## RunWisp 是如何做出来的

RunWisp 由 PoppyCake, s.r.o. 开发。我们非常依赖代码评审和测试。评审细致到小处，比如某个辅助函数是否值得存在、还是直接内联三行就好，这样代码才能保持精简、易读。守护进程的测试代码行数比业务代码还多，其中包括一些棘手的情况，比如某个每日任务恰好被安排在夏令时跳过的那一小时里。

即便评审仔细、测试充分，也不能保证万无一失。如果我们漏掉了什么，请[提交 issue](https://github.com/runwisp/runwisp/issues)，我们会修复。

_Richard Popelis，首席开发者_

## 许可证

RunWisp 守护进程和 Web 界面采用 GPL-3.0-or-later 许可证。简单来说，你可以随意使用，但如果分发修改后的版本，必须以相同许可证保持开源。详见 [LICENSE](LICENSE)。

`packages/` 下的共享库、配置 JSON Schema 和 OpenAPI 描述则采用 Apache-2.0 许可证。详见 [LICENSE-APACHE](LICENSE-APACHE) 以及各个包中的许可证文件。

---

<div align="center">

[runwisp.com](https://runwisp.com) · [文档](https://docs.runwisp.com) · [版本发布](https://runwisp.com/releases/) · [报告问题](https://github.com/runwisp/runwisp/issues)

</div>
