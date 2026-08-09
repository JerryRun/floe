# 第三方 SSH 终端集成调研

Floe 当前通过 Windows Terminal + OpenSSH 启动 SSH 会话，并用一次性本地
`SSH_ASKPASS` 地址完成免密输入。密码不出现在命令行、窗口标题、模板或日志中；
这是 Floe 的安全和体验优势，应继续保留为内置适配器。

## 市面工具的可集成边界

| 终端/客户端 | 常见启动方式 | 免密能力 | 适合作为 Floe 第一版适配器吗 |
| --- | --- | --- | --- |
| Windows Terminal + OpenSSH | `wt.exe` 调起 `ssh.exe` 或 PowerShell 脚本 | SSH agent、密钥、Floe 的 AskPass | 已内置，默认方案 |
| PuTTY / plink | `putty.exe -ssh user@host -P port`，密钥使用 `-i` | Pageant/Windows OpenSSH agent；命令行密码参数不安全 | 可做内置适配器，禁止自动拼接密码 |
| Tabby | URI/profile 或启动 shell 命令（不同版本参数有差异） | profile、密钥、agent | 适合用户自定义命令模板，不宜硬编码版本参数 |
| SecureCRT | 官方命令行打开会话/profile | 会话 profile、密钥、agent | 适合通过已保存 profile 调起 |
| Xshell | URI/会话参数（版本差异明显） | 会话 profile、密钥、agent | 适合通过 profile 调起 |
| MobaXterm / Cmder / ConEmu | 启动后执行 `ssh` 或 shell 命令 | 依赖 OpenSSH agent/密钥 | 作为“外部终端 + 命令”通用模式 |

第三方客户端对“密码作为 CLI 参数”的支持虽然常见，但会泄漏到进程列表、崩溃
转储和审计工具，不能移植 Floe 的免密体验。通用集成应只传递非敏感参数，密码
继续由客户端自己的密钥/agent/profile 负责。

## 建议的数据模型

新增“终端配置”而不是把一个字符串直接拼到命令行：

```text
name           显示名
executable     可执行文件路径（例如 wt.exe、putty.exe）
arguments      参数模板
working_dir    工作目录模板，可为空
enabled        是否在终端菜单显示
```

允许的参数占位符应采用白名单：`{host}`、`{port}`、`{user}`、`{session}`、
`{cwd}`、`{ssh_command}`。不提供 `{password}`、`{private_key_content}` 等
敏感占位符；私钥只允许作为路径参数，并在启动前按客户端要求检查文件存在。

参数模板必须按 Windows argv 规则解析，而不是先拼成一整条 PowerShell 字符串。
每个参数单独解析和转义，拒绝换行、`&`、`|`、`;` 等会改变 shell 语义的输入；
启动失败时只记录可脱敏的配置名和错误码。

## 分阶段实现方案

1. 保持当前 Windows Terminal 内置适配器和 AskPass 不变。
2. 增加“终端配置”管理：名称、可执行文件、参数模板、启用开关；提供参数预览，
   预览中隐藏密码和私钥内容。
3. 先提供“以外部终端执行 OpenSSH 命令”的通用适配器。PuTTY、Tabby、SecureCRT
   等通过用户填写的 profile/参数接入，不对版本差异做脆弱的硬编码。
4. 增加启动前验证：可执行文件存在、模板占位符合法、目标是已连接的 SFTP 会话；
   非 Windows 平台隐藏 Windows Terminal 专属项。
5. 后续再为 PuTTY 和常见终端增加可选内置预设，但预设只使用密钥/agent/profile，
   不携带密码。

结论：第三方终端“可手工配置”值得做，但应定位为安全的命令/配置适配层，而不是
复制 Floe 的免密密码注入。这样既保留 Floe 的特色，也覆盖用户已经安装的终端。
