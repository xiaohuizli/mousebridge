# MouseBridge v0.1.0 预览版

Windows / macOS 局域网鼠标共享的命令行原型。

- 同组密钥发现与 TLS 1.3 配对认证。
- 左右跨屏、三键、屏内拖动、二维滚轮、Escape 返回。
- 独占控制、心跳、断线释放、记录模式验收。
- macOS arm64 / amd64 与 Windows amd64 / arm64 便携 ZIP。

下载与 CPU 架构匹配的 ZIP，解压后在终端按 README 运行 `bin/` 中的程序。macOS 为命令行可执行文件，Windows 为 `.exe`；这是便携包，不是图形安装向导、`.app`、DMG 或 MSI。

包内 `BUILD-INFO.json` 记录源码提交与构建环境，`SHA256SUMS` 校验包内文件，外部 `.zip.sha256` 校验下载包。macOS 未做 Developer ID 签名/公证，Windows 未做 Authenticode 签名。

自动化测试使用记录桌面和实际 loopback 网络，不运行真实输入钩子或注入。双机桌面、权限、DPI、触控板和高频鼠标仍需按 `docs/BEHAVIOR.md` 验收，因此标记为预览版。
