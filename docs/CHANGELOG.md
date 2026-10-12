# Versions

## 0.1.2

- Publish completion shows a prominent dismissible success notice and the result is labelled published; failed submissions never claim success.
- Ticket, staff-only draft history and recent jobs load in one SQL transaction. Terminal jobs no longer retain running-only cancellation controls or stop a mixed-state poll.
- Personal desktop workspace has own administrator credentials, separate default storage and no sample data. Old workspaces and schema1 remain compatible.
- Administrator dashboard reports actual account/knowledge/model configuration readiness; configuration is not presented as successful real-model validation.
- UTF-8 TXT/Markdown import remains editable and requires explicit save, with independent format/size recovery guides.
- Live task creation requires confirmation of the current configuration version; missing confirmation does not create a paid task.

# 版本记录

## 0.1.1

- 修正 Windows SQLite 文件地址：盘符归入路径，统一 Windows 分隔符，正确处理中文、空格、特殊字符和本地长路径前缀。
- 识别首次启动中断留下的空库，只在确认无结构对象、业务数据和外来版本标记时续建；保留原配套密钥。已有业务数据但工作区信息缺失时停止，避免重新播种。
- 首次结构与业务初始化合并为同一事务。无工作区时，备份、改密和密钥恢复命令不再创建新演示数据。
- 启动失败显示版本、数据目录、数据库路径和失败步骤；将打不开、损坏、读写失败和占用分别说明。
- 增加只读 `--diagnose` 与 Windows「故障诊断.cmd」入口，不回显模型密钥、密码或业务正文。
- 前端、API、打包和审计版本一致性检查；固定 SQL 源文件换行规则，运行包自动附带验证文档引用的记录。
- 增加路径、初始化中断、拒绝外来/损坏数据库、诊断只读和 0.1.0 已有工作区升级的检查。具体实测及平台限制见验证记录。

schema 仍为 1；此补丁不迁移未来结构。Windows 交叉编译不代表 Windows 原生验收，模拟模型协议不代表真实模型质量。

## 0.1.0

技术首版：三种角色的工单协作、公开/内部评论、版本化知识、AI 草稿、人工审核与历史、持久任务、恢复和备份、便携包及验证工具。
