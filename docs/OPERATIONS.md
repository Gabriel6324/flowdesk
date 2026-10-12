# 运行与恢复

## 个人工作台

Windows 运行「启动个人工作台.cmd」；Linux 使用 `./run-workspace.sh`，等价于 `flowdesk --workspace`。首次启动在终端设置自己的管理员，空白工作台不包含演示账号、工单或资料。后续启动直接登录已有账号。默认目录是原 FlowDesk 数据目录下的 workspace 子目录，示例数据仍留在原目录。

支持 `--workspace --init-admin` 离线初始化。个人工作台的诊断、备份、改密和密钥恢复均应加 `--workspace`，或明确指定它的数据目录。例如 `flowdesk.exe --workspace --reset-admin-password`、`flowdesk.exe --workspace --backup`。Windows 提供「个人工作台故障诊断.cmd」。

包含公开演示账号的旧目录会被个人模式拒绝；不会自动删除、替换或迁入该目录。原示例体验通过「启动FlowDesk.cmd」继续使用。


## 数据和退出

桌面默认数据目录为当前操作系统用户的 FlowDesk 数据目录。可以指定 `--data-dir /path/to/private-directory`；Windows 带空格的路径须加引号。程序不会把数据放到解压包目录，便于替换程序时继续使用。

## 从 0.1.0 更新与启动诊断

关闭旧程序，解压 0.1.1 包，运行新版的「启动FlowDesk.cmd」。继续使用原数据目录即可；不用卸载、删除数据库或重新配置模型。schema 仍为 1，正常的旧工单、回复、账户、会话和配套密钥会保留。

0.1.0 的 Windows 数据库地址处理有误，可能在首次启动失败后留下一个空数据库，随后误报 `SCHEMA_INVALID`。0.1.1 修正盘符、路径分隔符及字符转义；只读检查确认没有结构对象、业务数据和其他数据库版本标记时，可安全续建。旧版已经创建结构但尚未提交任何业务数据时，也可续建。数据库结构和首次业务数据现在在同一事务中提交。

已有业务数据但工作区信息缺失、外来数据库、损坏文件、更高结构版本或校验不符仍会停止启动。不要通过删除数据、修改 migrations 或删除 WAL 文件来跳过检查。

Windows 可双击随包的 **故障诊断.cmd**；它在解压目录写入 `FlowDesk-startup-diagnostic.txt`，仅检查数据，不修复或改写业务文件。命令行也可执行：

```text
flowdesk.exe --diagnose
flowdesk.exe --diagnose --data-dir "D:\FlowDesk data"
```

Linux 使用 `./flowdesk --diagnose`。服务器数据须加 `--profile server`。诊断会说明程序版本、实际数据路径、数据库状态和密钥文件状态；失败时显示具体检查步骤与 SQLite 错误编号，不包含密码、API 密钥或原始业务正文。文件可能含本机用户名和路径，分享前自行检查。

若诊断显示「首次初始化尚未完成，已确认没有业务数据」，直接正常启动即可。若显示损坏、版本或结构问题，保留原目录并按对应步骤处理；不要把空库续建当成通用数据库修复。

主要文件是 `flowdesk.db`、SQLite WAL相关文件、`master.key`、`instance.lock` 和有大小上限的日志。不要公开整个目录。密钥、备份和业务数据属于配套文件。一个目录只能启动一个应用进程；遇到实例锁提示，检查原进程，不要直接删数据库或锁文件来绕过活跃进程。

在终端 Ctrl+C 退出；Windows 关闭终端可能强制终止，优先 Ctrl+C 等待退出消息。系统先停止新操作并等待worker，再关闭SQLite；超过退出限时会明确中断。下次启动不会自动重复执行原来正在请求的模型任务。

## 备份

先停止原进程，再执行：

```bash
./flowdesk --data-dir /path/to/workspace --backup
```

服务器工作区须加 `--profile server`。程序持有独占锁，通过 SQLite `VACUUM INTO` 创建一致数据库副本，复制可用的32字节主密钥，写入带版本、数据库SHA-256及密钥指纹的manifest。备份保存在数据目录的backups下，自动保留最近3组；将需要长期保存的完整备份组复制到独立受保护位置。

密钥不可用时，manifest会标明缺失，业务数据库仍可备份。这类备份无法恢复已加密的模型密钥，不能当成完整密钥备份。磁盘不足、权限错误或快照失败不会报告完成。

恢复时停止程序，保留当前目录作为回退，再把完整备份里的数据库及配套密钥恢复到一个私有目录。不要拼接不同备份的数据库与密钥，也不要把旧WAL文件覆盖到恢复的快照上。使用匹配的软件版本和原profile启动。当前版本只有schema1，不会自动迁移未来结构；较新结构或checksum不符会停止启动。

## 管理员密码

持有本机数据目录权限的人可以执行离线重置：

```bash
./flowdesk --data-dir /path/to/workspace --reset-admin-password
```

服务器加 `--profile server`。按提示输入已有管理员邮箱及两次新密码；密码12–128个Unicode字符，空格属于密码本身。交互终端隐藏输入；管道输入须由操作者保护。密码不会出现在命令参数里。此操作撤销该管理员的旧会话，不创建一个新的管理员。

首次服务器管理员使用 `--profile server --init-admin`，并输入邮箱、显示名称和两次密码。已经初始化的目录拒绝重复初始化；没有网络密码重置接口。

## 模型密钥文件丢失

先停止应用，保留原数据库、可找到的旧密钥、恢复标记与备份。若有完整配套备份，可先按上文恢复。无法恢复旧密钥时执行：

```bash
./flowdesk --data-dir /path/to/workspace --recover-keystore
```

该命令先备份，再生成新的私有主密钥；清除已保存的模型接口密钥，终止未完成真实任务，保留工单、资料及历史，切回demo。之后管理员重新配置模型。准备、提交和安装三个崩溃阶段通过恢复标记重入；缺失恢复临时文件或指纹不符时停止，不能靠删除标记强行继续。

重试保存只能挽救当前进程仍保留的结果。退出后丢失的内存结果无法通过该按钮找回；重新生成是一个新任务，真实模式可能再收费。

## 常见启动问题

| 提示 | 处理 |
| --- | --- |
| INSTANCE_LOCK_FAILED | 先关闭使用同一目录的原进程 |
| LISTEN_FAILED | 检查端口；显式选择空闲的IPv4 loopback端口 |
| PROFILE_MISMATCH | 桌面和服务器使用各自目录，不修改元数据绕过 |
| SERVER_BOOTSTRAP_REQUIRED | 在本机离线初始化服务器管理员 |
| SCHEMA_INVALID | 运行诊断，核对失败步骤；已有业务数据或外来结构不会自动覆盖，保留原目录和完整备份 |
| SCHEMA_TOO_NEW / SCHEMA_CHECKSUM_MISMATCH | 保留数据，用匹配版本或完整备份恢复 |
| DATABASE_OPEN_FAILED / DATABASE_PATH_INVALID | 核对实际路径、文件类型和当前账号的文件权限 |
| DATABASE_CORRUPT | 文件不是有效数据库或已经损坏；停止该目录的写入，保留文件并从完整配套备份恢复 |
| DATABASE_IO_FAILED | 核对剩余磁盘空间、读写权限和磁盘状态 |
| DATABASE_BUSY | 关闭占用数据库的其他程序，等待写入结束后重试 |
| DATABASE_QUERY_FAILED | 查看具体检查步骤和错误编号，先核对文件、磁盘与权限 |
| KEYSTORE_RECOVERY_INCOMPLETE | 保留标记和配套文件，按恢复记录核对缺失文件 |
| STORAGE_OUTCOME_UNKNOWN | 先查当前记录；相同操作重试沿用原幂等键，不连点创建 |
| PROVIDER_* | 查看任务中的失败阶段和专属引导，核对服务商/网络/参数后手动处理 |

业务故障的详细目录来自 `internal/domain/guides.json`，前后端共用。未知故障显示安全的通用步骤和请求ID，不展示原始模型响应或敏感服务日志。

