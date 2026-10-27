# MouseBridge

独立实现的局域网鼠标共享项目，支持 Windows 和 macOS。依据 ShareMouse 静态分析中确认的“捕获输入 → 网络传输 → 远端注入”机制编写，使用自己的协议，不加载或复用 ShareMouse 二进制，不与其私有协议互通。

**当前是可构建的 v0.3 原型。协议、状态机、故障处理和两个真实进程的网络联调已经验证；Windows/macOS 双机的系统捕获与注入尚未实测。** 完整结果见 [验证记录](docs/VALIDATION.md)，原程序与本项目的待确认项见 [行为清单](docs/BEHAVIOR.md)。

## 已实现

- 两台电脑、一端物理鼠标控制另一端；两种系统均有控制端和接收端实现。
- 指定左/右相邻布局：穿过本机主屏边缘进入远端，从远端对应边缘返回；本机 Escape 随时返回。
- 左、右、中键，远端屏内拖动，垂直与水平滚轮。正在本机拖动时不跨屏。
- UDP 发现与手工指定地址；同一配对密钥才能发现和连接。
- TLS 1.3 双向配对认证、可靠有序传输；每个接收端只允许一个控制端。
- 400ms 心跳，2 秒读写超时，连接关闭/超时/协议错误释放远端按键；控制端恢复本机鼠标。
- 默认接收端只打印输入事件，`-live` 才进行真实注入。

v0.3 不含键盘转发、剪贴板、文件传输、开机启动、多显示器布局、复杂的多屏路由或 ShareMouse 兼容性。Escape 的本机键盘钩子仅用于退出远端控制，不转发键盘内容。

## macOS 双击应用

`MouseBridge.app` 默认开启局域网开放发现，无需填写 IP、密钥或首次数字确认。同一局域网内开启该功能的电脑可自动连接，每台同时具备控制与接收能力；两台时自动选择对方，多台时可从列表切换控制目标，当前最多连接 16 台其他电脑。开启即允许该局域网内其他开放实例控制本机。关闭发现会结束所有远端会话。实际鼠标操作仍需系统辅助功能和输入监控权限。

运行 `python3 scripts/package_macos_app.py`，在 `dist/macos-app/` 生成支持 Apple Silicon / Intel 的通用 `.app`、DMG 和 ZIP。v0.3.1 每次启动生成临时身份，不再访问 macOS 钥匙串。应用要求 macOS 12+，本地 ad-hoc 签名，未公证。详见 [macOS 应用说明](docs/MACOS_APP.md)。

Windows 同款原生窗口：运行 `python3 scripts/package_windows_app.py`，生成自动选择 x64 / ARM64 的每用户安装向导和便携包。双击安装/启动即可使用开放局域网互控，无需命令行。详见 [Windows 应用说明](docs/WINDOWS_APP.md)。

## 下载与自动打包

公开仓库：[xiaohuizli/mousebridge](https://github.com/xiaohuizli/mousebridge)。

- [Releases 下载](https://github.com/xiaohuizli/mousebridge/releases)：分别提供 macOS arm64/amd64、Windows amd64/arm64 便携 ZIP 与校验值。
- [Actions](https://github.com/xiaohuizli/mousebridge/actions)：main 推送运行测试与打包；v* 标签成功构建后发布预览版。
- 命令行 ZIP 解压后保留 `bin/` 和文档目录，以下命令在解压根目录执行。GitHub Actions 同时构建命令行便携包、macOS 通用 DMG / ZIP 和 Windows 双架构 Setup / ZIP。主分支可下载 Actions 产物；匹配版本的 v* 标签发布到 Releases。

## 直接运行

`bin/` 提供本次构建的 macOS arm64/amd64、Windows amd64/arm64 程序。macOS 程序未做开发者签名和公证；可在本机用下述命令从源码构建。GitHub Actions 已完成 macOS 两种架构与 Windows amd64 的自动化运行验证；Windows arm64 目前只交叉编译。详见 [云端构建验证](docs/CI.md)。

假设 A 有物理鼠标，B 在 A 右侧。A 运行 `control`，B 运行 `receive`。两端都支持 Windows 或 macOS。交换角色时停止原进程并交换命令。

### 1. 配对密钥

只在一台电脑生成一次，将同一个值设置为两端的环境变量 `MOUSEBRIDGE_KEY`。它是 256 位随机密钥，不能使用普通口令。拥有该密钥的电脑属于同一信任组，接收端启用 `-live` 后允许其控制鼠标。

macOS / zsh：

```sh
export MOUSEBRIDGE_KEY="$(./bin/mousebridge-macos-arm64 keygen)"
printf %s "$MOUSEBRIDGE_KEY" | pbcopy
```

在另一台 Mac 的终端粘贴密钥（输入不回显）：

```sh
read -rs 'MOUSEBRIDGE_KEY?配对密钥: '
export MOUSEBRIDGE_KEY
```

Windows 使用 `cmd.exe`，不要把真实密钥写入脚本或项目文件：

```bat
set /p MOUSEBRIDGE_KEY=Pair key:
```

也可先运行 `bin\mousebridge-windows-amd64.exe keygen` 生成，再复制到两端。密钥不会由程序自动落盘。请通过可信方式传递，配对后清理剪贴板。

### 2. 先测试连接

在 B 上运行（示例为 Mac；Windows 替换程序名）：

```sh
./bin/mousebridge-macos-arm64 receive
```

在 A 上发现 B，再发送示例输入。此时 B **只记录事件**，不移动系统鼠标：

```sh
./bin/mousebridge-macos-arm64 discover
./bin/mousebridge-macos-arm64 send -peer 192.168.1.20:42048 -script examples/demo.jsonl
```

将示例 IP 替换为发现结果。发现依赖同网段 IPv4 广播；多网卡时可用 `discover -target 192.168.1.255:42047` 指定网段广播，或直接指定已知的 B 地址。TCP 42048 是控制端口，UDP 42047 是发现端口；防火墙需允许本地网络访问。`receive -no-discovery` 可以关闭发现。

### 3. 开启真实鼠标共享

停止 B 的记录模式，在 B 运行：

```sh
./bin/mousebridge-macos-arm64 receive -live
```

在 A 运行：

```sh
./bin/mousebridge-macos-arm64 control -peer 192.168.1.20:42048 -edge right -live
```

Windows 的等效命令：

```bat
bin\mousebridge-windows-amd64.exe receive -live
bin\mousebridge-windows-amd64.exe control -peer 192.168.1.20:42048 -edge right -live
```

上面两条命令分别放在 B 和 A。B 在左侧时使用 `-edge left`。

- 将 A 的鼠标穿过指定屏幕边缘，进入 B；A 的光标暂时停在主屏中央。
- 从 B 的入口边缘返回，或在 A 按 Escape。按 Ctrl+C 停止程序。
- 断线后 A 恢复本机控制，B 释放程序按下的按钮。重新建立连接需重新运行 A 的 `control` 命令。
- B 本机用户可以在接收端终端按 Ctrl+C 停止共享。
- macOS 真实模式需要“隐私与安全性 → 辅助功能”，控制端还需要“输入监控”；为实际运行的终端/程序授权后重新启动。程序发现权限缺失时明确报错，不主动改变系统设置。
- Windows 使用普通交互桌面；UAC 安全桌面、锁屏、不同完整性级别窗口不属于已支持范围。程序不自动提权。
- 当前只设计了主显示器左右布局；更改分辨率后重新启动。Retina/Windows 缩放、多屏和触控板自然滚动仍需真机验证。

## 源码构建与验证

需要 Go 1.25+，无第三方 Go 依赖。macOS 原生适配需要 Xcode Command Line Tools / C 编译器。

```sh
go build -o mousebridge ./cmd/mousebridge
go test -race ./...
go vet ./...
```

在本次 macOS arm64 开发机可构建四种架构：

```sh
make build
python3 scripts/smoke.py bin/mousebridge-macos-arm64
```

`make build` 面向 macOS arm64 构建主机；在 Windows 直接使用 `go build -o mousebridge.exe ./cmd/mousebridge`。冒烟测试可传入对应宿主机程序路径；它只启动记录模式接收端，使用内存中的临时密钥。

## 模块

| 路径 | 职责 |
|---|---|
| `cmd/mousebridge` | 命令行、进程生命周期、事件队列与心跳 |
| `internal/bridge/controller.go` | 本机/远端控制状态、屏幕边缘、Escape、队列失败恢复 |
| `internal/bridge/machine.go` | 接收命令、坐标累计、按键状态与故障释放 |
| `internal/bridge/discovery.go` | HMAC 签名的 UDP 请求/响应发现 |
| `internal/bridge/security.go` | 随机配对密钥、双向证书公钥固定、TLS 1.3 |
| `internal/bridge/transport.go` | 帧限制、超时、独占控制、会话关闭 |
| `internal/desktop` | Windows 低级钩子/SendInput；macOS Event Tap/CGEventPost；记录适配器 |

命令行核心是原生单进程程序，系统回调与 Go 逻辑通过函数调用和有界队列协作。macOS 应用由 AppKit 界面和其管理的核心子进程组成，使用私有管道交换状态和设置，没有 Electron 依赖。网络协议详见 [PROTOCOL.md](docs/PROTOCOL.md)。
