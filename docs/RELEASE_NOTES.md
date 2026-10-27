# MouseBridge v0.3.1 预览版

本版移除 macOS 钥匙串读写，应用每次启动生成仅存在内存中的临时身份，旧钥匙串条目不访问、不自动删除。新增 macOS 通用 DMG 与 Windows 原生窗口/安装程序：Windows 安装向导自动选择 x64 / ARM64，提供开始菜单与卸载入口，不需要管理员权限。两端都使用免数字确认的开放局域网模式。Windows 安装和窗口尚未真机验证，macOS 的 Intel 版本尚未运行验证。

Windows / macOS 局域网鼠标共享原型，提供命令行程序与 macOS 原生启动界面。

- 同组密钥发现与 TLS 1.3 配对认证。
- 左右跨屏、三键、屏内拖动、二维滚轮、Escape 返回。
- 独占控制、心跳、断线释放、记录模式验收。
- macOS arm64 / amd64 与 Windows amd64 / arm64 便携 ZIP。

命令行便携包：下载与 CPU 架构匹配的 ZIP，解压后在终端按 README 运行 `bin/` 中的程序。macOS 为命令行可执行文件，Windows 为 `.exe`。

macOS 应用包：原生 `.app` 默认使用局域网开放共享；首次无数字确认、无设备白名单。开启发现的电脑自动连接，每台同时运行控制与接收服务，多台可从列表切换目标（最多 16 个其他设备）；关闭发现同时撤销全部远端会话。每个桌面使用输入租约避免多个远端鼠标同时注入，并在切换/断线时释放按键。所有输入通道仍使用独立临时密钥和 TLS 1.3。支持 Apple Silicon / Intel，要求 macOS 12+，本地 ad-hoc 签名，未公证。详见 `docs/MACOS_APP.md`。旧命令行审批模式仍保留，GitHub Actions 同时生成两种桌面安装包与命令行包，标签发布必须包含两种安装包并通过校验。

包内 `BUILD-INFO.json` 记录源码提交与构建环境，`SHA256SUMS` 校验包内文件，外部 `.zip.sha256` 校验下载包。macOS 未做 Developer ID 签名/公证，Windows 未做 Authenticode 签名。

自动化测试使用记录桌面和实际 loopback 网络，不运行真实输入钩子或注入。双机桌面、权限、DPI、触控板和高频鼠标仍需按 `docs/BEHAVIOR.md` 验收，因此标记为预览版。
