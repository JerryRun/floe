# Floe 文件传输方案评审（2026-08）

这份评审把“速度”和“完整性”分开衡量：速度是有效字节/秒，完整性是目标文件与发送内容逐字节一致、失败时旧文件不被破坏、重启后可以继续。对比对象选择了桌面端常用的 WinSCP、FileZilla、命令行的 rsync、rclone，以及高延迟链路常用的 aria2。

## 横向结论

| 工具 | 主要传输模型 | 断点/失败恢复 | 完整性保障 | 对 Floe 的启示 |
| --- | --- | --- | --- | --- |
| WinSCP | SFTP/FTP 单文件流，按需并行队列 | 临时文件、续传；重连后校验大小/偏移 | 传输后大小校验，用户可启用校验和 | 临时文件必须隔离；覆盖提交必须是最后一步 |
| FileZilla | 单文件流 + 多任务队列；FTP 受服务器能力约束 | `.part`/续传，失败重试 | 传输后大小，部分场景支持校验 | 不要把 FTP 当随机并发写协议；并行应放在文件级 |
| rsync | 差分块（rolling checksum + 强校验） | 只重传变化块，天然适合重复同步 | 临时文件 + rename；可 `--checksum` | 对重复同步可增加差分模式；普通复制不必强制全文件二读 |
| rclone | Provider 原生 multipart/chunk；并发可调 | 独立 chunk、重试、校验；对象存储用远端 MD5/ETag | 源/目标 checksum，失败不提交最终对象 | 应抽象 Provider checksum 能力，避免盲目回读全部目标 |
| aria2 | 多连接分段下载 | `.aria2` 控制文件，分段续传 | piece hash / 完成后 hash | 分块要足够多以平衡并发和重传成本；manifest 要可恢复 |

参考：

- WinSCP：[resuming file transfers](https://winscp.net/eng/docs/resume)
- FileZilla：[transfer settings](https://wiki.filezilla-project.org/Transfer_settings)
- rsync：[--partial、--append-verify、--checksum](https://download.samba.org/pub/rsync/rsync.1)
- rclone：[checksums](https://rclone.org/docs/#checksums) 与 [transfers/chunk size](https://rclone.org/docs/#transfers)
- aria2：[piece length、checksum、续传](https://aria2.github.io/manual/en/html/aria2c.html)

## Floe 原实现的关键问题

1. SFTP 目标曾直接写最终路径。网络中断或校验失败时，用户会看到半文件；覆盖已有文件时还可能把原文件先破坏。
2. SFTP 的 `MaxConcurrentWrites=1` 同时限制了单个大文件和目录中的文件。目录任务会把大量小文件串行化，吞吐明显低于成熟工具。
3. 固定 64 MiB 分块对中等文件并不合适：100~300 MiB 文件只有 2~5 个任务分片，无法填满并发；失败时还要重传最多 64 MiB。
4. 每个分块先写、再打开目标回读；FTP 因此每块结束一次 STOR，SFTP 也容易遇到“写句柄未关闭时不可读”的托管服务限制。
5. 应用内暂停后立即恢复可能让旧 worker 仍在阻塞的网络 I/O 上运行，新 worker 同时写同一临时文件，存在竞态。
6. 重启恢复会无条件重新读取所有已校验分块。在高延迟或按流量计费链路上，这会把恢复时间和流量近似翻倍。

## 本次落地的方案

- Local/FTP 使用带任务 ID 的 `.floe-part-*` 临时文件；SFTP 保留直接路径模式以兼容会隐藏临时文件或不支持跨通道 rename 的服务器。所有模式都会在完成前逐块校验。
- 支持原子替换的 Provider 直接使用原子 rename。Windows 本地文件使用 `MoveFileEx(REPLACE_EXISTING|WRITE_THROUGH)`；不支持原子覆盖的协议使用“备份旧目标 → 提升临时文件 → 失败恢复”的安全流程，绝不先删除旧文件。
- 写入与验证解耦：worker 先连续写完自己的范围并关闭/落盘写句柄，再使用远端 SHA-256 或回读验证。FTP 大文件不再每 64 MiB 重建一次数据连接。
- SFTP 数据通道启用 128 个并发请求窗口（单个文件内部由 `pkg/sftp` 流水线填充），但跨文件写入槽恢复为 1；单文件随机写仍限制为 1 个 Floe worker，优先保证托管 SFTP 兼容性。
- 分块大小按文件大小和并发自适应：8 MiB 下限、64 MiB 上限，目标约为并发数的 4 倍分片；小文件不再分配 4 MiB 以上的缓冲区。
- 失败 worker 会取消同一任务的其他 worker。暂停不会删除运行句柄；恢复请求会排队到旧 worker 完全退出后再启动，避免双写。
- 只有重启加载的任务会重新验证已完成分块；同进程暂停/恢复信任已落盘 manifest。远端 SHA-256 能力探测失败后会缓存不可用状态，避免每块重复启动失败的 shell 会话。
- 删除任务或清理失败历史时异步清理临时文件。

## 验收指标

建议在同一机器、同一服务器、同一文件上记录以下数据，而不是只看 UI 百分比：

| 指标 | 目标 |
| --- | --- |
| 有效吞吐 | 本地↔本地达到磁盘顺序读写基线的 70% 以上；SFTP 高延迟链路相对旧版提升至少 2 倍 |
| 重连重传量 | 失败点之后只重传当前分块，单次上限 8~64 MiB（随自适应分块） |
| 完整性 | 成功后源/目标 SHA-256 相等；注入写失败、回读不一致、rename 失败时旧目标仍保持原内容 |
| 恢复 | 应用重启后只验证 manifest 中已完成分块一次；同进程暂停恢复不产生已完成分块的重复读取 |
| 并发 | 目录任务可同时上传多个文件；单个 FTP 文件保持顺序数据流，不进行随机并发写 |

基准入口：

```bash
GOPATH="$PWD/.cache/gopath" GOCACHE="$PWD/.cache/go-build" \
  go test ./internal/core -run '^$' -bench BenchmarkTransferBlockProgress -benchmem
```

线上验收还应增加真实 SFTP（OpenSSH、托管 SFTP）和 FTP（被动模式、断线重连）矩阵；单元测试无法模拟服务器对同时读写句柄、fsync 扩展和 rename 覆盖语义的差异。
