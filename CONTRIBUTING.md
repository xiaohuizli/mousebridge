# 后续开发约定

- 以 docs/PROTOCOL.md 为本项目规格，docs/BEHAVIOR.md 区分原程序未知项和本项目待验收项。
- 不复制第三方闭源实现，不把本项目测试结果写成 ShareMouse 行为证据。
- 修改协议/状态机需覆盖公开接口的行为；网络测试使用实际 socket，系统输入边界使用 Recorder。
- 不在自动化测试中调用原生输入注入；真实模式验证需要明确的桌面测试环境。
- 配对密钥只从环境读取，不写入代码、配置或日志。
- macOS 使用 zsh/POSIX 工具；Windows运行示例用 cmd.exe，不要求 PowerShell。
- 合并或交付前运行 scripts/verify.py；准确列出平台实测未完成项。
