# ShareMouse 多电脑鼠标共享：静态分析与证据

分析日期：2026-10-23。未安装、未运行任何被分析的 Windows 程序，未抓取双机流量。

**不需要先安装才能解析。** 完整安装包、便携包或主程序均可提供静态分析材料。本次通过 REA 核验、解包官方便携包，再用现有 LLVM objdump 和 PE/Delphi 元数据解析补足原生代码证据，已经连通鼠标捕获、消息传递、UDP 发送、接收和系统输入注入的关键路径。

## 样本身份与边界

用户提供的 `ShareMouse_102_10097466.exe` 是本次首先分析的样本：7,034,888 字节，SHA-256 为 `547bc050187fc7ae7d5c9d910a45d6fefc913b6d904d434393f5bdafb74711c2`。REA 将其识别为 x86 PE32；资源清单标识 `Minisoft.UIDownloader`，版本资源为 `1.0.1.276`。文件内的 DuiLib、InstallerWorkflow、DownloadTargetSoftware、RemoteApiService 属于下载器。不能把这些代码当成 ShareMouse 的鼠标共享实现。

为继续回答鼠标共享问题，本次另从 [ShareMouse 官方下载页](https://www.sharemouse.com/download/) 获取 [Windows 便携 ZIP](https://www.sharemouse.com/ShareMouse_USB.zip)，作为**独立的官方分析样本**，不声称它与原下载器未来下载的版本相同。

- ZIP SHA-256：`90e89bf33b18f96e0a1417841269cbb6702a2908aed3564ff4f8b88527b29d90`。
- 主程序 SHA-256：`c6e63d6217b0e476824feafe5efc2b2c4e856e574a778f56d886b5bf2a267a40`，18,766,400 字节。
- 下载页标注 `7.0.30`，但实际下载的主程序版本资源是 **7.0.31.0**；本报告代码结论以实际字节为准。
- REA 的 Ghidra/Hopper/IDA 等原生提供器当前不可用，未获得 REA 反编译伪代码。REA 负责身份、资源、归档图与提取证据；代码调用关系来自 LLVM 21 静态反汇编与原始字节解析，分开列证。

## 模块结构

| 模块 | 观察与职责 | 证据强度 |
|---|---|---|
| `sharemouse.exe` | 原生 Delphi/VCL GUI；含 `TsmMain`、显示布局与输入状态、Indy UDP/TCP 组件。主窗处理本地钩子通知及网络输入 | 方法表、DFM、导入表、直接调用 |
| `smkey.dll` | 导出 SetHook、SetHookFromThread、GetThreadID；创建钩子线程，安装低级键盘和鼠标钩子 | 导出表及函数汇编 |
| `smdesk.exe` | 所检查入口按下再释放 Ctrl，底层调用 SendInput | 入口调用链；其由主程序何时启动尚未追全 |
| `smlang.dll` | 包含该文件，主程序含加载名称；语言资源模块的角色是推断 | 包清单与名称，不是完整调用链 |
| `libcrypto-3.dll` / `libssl-3.dll` | 随包提供 OpenSSL 相关库 | 文件清单；不能仅凭存在就认定每条控制报文使用 TLS |
| `vcruntime140_32.dll` | 运行库文件 | 文件清单 |

本次官方包共有 8 个提取文件，包括 ReadMe。未见 Electron 的 app.asar、preload 或 Chromium renderer 组件；结合原生 VCL 与钩子代码，**已确认的通信架构是原生主窗、钩子线程与网络组件**。“主进程—渲染进程 IPC”不适合描述这条已经恢复的输入链。不能据此断言程序所有模式下只有一个进程。

## 本机通信：钩子线程与主窗口

主 EXE 在 `0xA6E3F3` 调用 thunk `0xA603CC`，其跳转至 `smkey.dll!SetHook` 的 IAT `0xAE4A34`，传入窗口句柄。DLL 将此 HWND 保存，并创建钩子线程。

`smkey.dll` 在 `0x41E9F8` 调 `SetWindowsHookExW(13, …)`，在 `0x41EA25` 调同 API 的类型 14，分别对应低级键盘和鼠标钩子。主窗的 Delphi 消息表位于主 EXE 文件偏移 `0x65B024`：60 个消息 ID 后紧接处理器指针数组，表指针在 `0x658618` 被引用。

| 方向 | 消息 | 数据含义 | 主窗处理器 VA |
|---|---|---|---|
| 钩子 → 主窗 | `0x402` | 键盘虚拟键、按下/抬起、扫描码和标记 | `0xA77C30` |
| 钩子 → 主窗 | `0x404` | 鼠标按钮、按下/抬起 | `0xA78158` |
| 钩子 → 主窗 | `0x414` | 绝对位置 x/y | `0xA78208` |
| 钩子 → 主窗 | `0x41E` | 相对位移 dx/dy | `0xA78484` |
| 钩子 → 主窗 | `0x417` | 垂直/水平滚轮及增量 | `0xA784E8` |
| 钩子 → 主窗 | `0x419` | 钩子就绪通知 | `0xA776A8` |
| 控制方 → 钩子线程 | `0x415` | 改变鼠标接管标志 | DLL 消息循环调用 `0x41E188` |

消息数值和载荷由指令恢复，并非原作者命名。钩子使用 PostMessageW 回传；另有 SendMessageW 同步问询及线程控制消息。这里实证的是进程内 DLL 钩子线程与窗口的通信，**不能把 PostMessageW 一概称为跨进程 IPC**。程序另含 Global\\ShareMouse… 命名事件线索，其跨桌面/多实例全链未在本次恢复。

## 多电脑共享鼠标的实现

```mermaid
flowchart LR
    A[电脑 A：物理鼠标] --> B[smkey 低级鼠标钩子]
    B -->|0x414 或 0x41E| C[主窗位置与接管状态]
    C --> D[定时器检查坐标变化]
    D -->|UDP mousemove:x,y| E[电脑 B：UDP 接收与命令分发]
    E --> F[坐标及屏幕状态处理]
    F --> G[SendInput 注入系统鼠标输入]
```

1. **捕获与接管。** DLL 鼠标回调 `0x41E3F8` 检查注入标记及接管状态。普通状态发送 `0x414` 携带 x/y，并继续钩子链；接管状态在 `0x41E64D/0x41E65D` 计算 dy/dx，在 `0x41E676` 发送 `0x41E`，之后在 `0x41E693` 令返回值为 1，抑制本机继续响应这次移动。不是所有注入事件都会被简单丢弃，代码还检查额外标记。
2. **维护虚拟坐标。** 主窗 `0xA78484` 处理 `0x41E`：在 `0xA784A4/0xA784B4` 将 dx/dy 加到对象偏移 `+0x660/+0x664` 的坐标状态。因此本机消息中的相对位移与网络中的坐标并非同一层数据。
3. **有变化才发送。** `MouseTimerTimer` 方法入口 `0xA75450` 读取上述坐标，与 `+0x650/+0x654` 的已发送值比较；变化时 `0xA7553D → 0xA94BFC`，后者使用字面量 `mousemove:%d,%d`（文件偏移 `0x694098`）格式化消息，再于 `0xA94C59` 调 `0xA9509C`。代码通过主窗 UDPClient/UDPClient6 对象发送；随后更新已发送坐标。存在启用、控制状态等条件，不是无条件持续发送。
4. **收包与分发。** DFM 将 `TIdUDPServer.OnUDPRead` 绑定至 `UDPServerUDPRead`，方法表恢复其入口为 `0xA9563C`。接收端经带状态的回调、队列处理，`0xA958D2 → 0xA959E8`，按冒号分离命令。识别 `mousemove` 后在 `0xA95BA3` 调 `0xA830E8`；其他分支含 `keyact`、`wheelact`、`moveswitch`、`moveok`。
5. **在另一台机器执行。** 已连通的条件调用链为：

   `UDP 分发 → 0xA830E8 →[0xA83159] 0xA7CB3C →[0xA7CE29 / 0xA7CEEE] 0xA7C94C →[0xA7C9D2] 0x740D98 →[0x740EFE] SendInput`。

   其中 `0x740D98` 构造 INPUT_MOUSE，写入坐标；一种模式采用 MOVE，绝对坐标模式再加 ABSOLUTE 与 VIRTUALDESK 标志。`0x740EFE` 调 `SendInput(1, &INPUT, 0x1C)`，API IAT 为 `0xAE48B4`。上述路径有状态和坐标分支，不表示每个报文都会无条件注入。

这解释了为什么鼠标可以跨机器移动：本地设备事件被程序捕获，远端把收到的输入还原为系统事件。官方也说明它不传输屏幕画面，跨屏切换依据显示器布局进行；这部分产品行为与本次输入路径相符，但完整边缘命中和显示布局算法尚未恢复。[官方 FAQ](https://www.sharemouse.com/faq/)

## “搜索”对应的设备自动发现

按用户澄清，本次关注局域网中发现其他电脑，而非文本搜索。

- 主窗包含 `AutoFindTimerTimer`（`0xA7BC78`）；其 `0xA7BC9E → 0xA94960`，构造 `rc_iamhere:%d:%d:%d:%s`（格式化调用 `0xA949E2`）后调用后续发送函数。这是自报存在的发现消息证据；格式中每个字段的确切含义尚未全部恢复，不根据 `%d` 猜测。
- DFM 中 UDPServer 的 DefaultPort 编码为 `0x0416 = 1046`，启用线程事件并绑定 UDPServerUDPRead；配置读取处也能见到 UDP 1046 和 TCP 6555 的默认值。
- 官方说明各实例通过 UDP 广播发布 IP/端口信息并监听同网段实例，也可以关闭广播、手工指定客户端。[客户端管理说明](https://www.sharemouse.com/doc/settings/client-management/)
- 官方将 UDP 1046 用于发现，TCP 6555 用于剪贴板/拖放文件；本次代码另直接证明 `mousemove` 经 UDP 对象发送。[网络设置说明](https://www.sharemouse.com/doc/settings/network/)

不能仅凭库的 `search`、FindFirstFile 或证书查找函数认定业务搜索。本次设备发现结论依赖实际方法、消息和 UDP 对象证据。

## 密码和加密：区分文档与代码

官方声明：设定密码后使用 AES-128-CBC 保护输入及传输数据，仅相同密码的客户端互连。[官方网络设置](https://www.sharemouse.com/doc/settings/network/)

本次样本含 DoEncrypt 状态、加密库及网络发送路径，但未完整追踪密钥派生、IV、认证、所有报文加密分支。因此不能据此评价其抗重放、安全强度，或声称默认/所有消息都已加密。上文的 `mousemove:…` 是构造时的命令表示，**不是已抓包验证的线上明文**。

## 证据索引与完成程度

REA 原始记录：

- 下载器资源：`ev_440a2dcb2d1583df4b3b92937f3bc23a59a1d8e11c7ebc90eb87e8f36ab471ea`，见 `downloader-rea-evidence.json`。
- 官方归档检查：`ev_945e0d8ccbd14387933f4e1d57ef982a2943b47bd111fa94a90fa1e9d2c495ea`。
- 官方归档提取：`ev_a8c41b872c1321544a58c8986daedf6cd432c649ab940de9d382f808bdd37514`，所有常规文件提取成功，无完整性矛盾。
- 官方主程序资源：`ev_0b4dd4f8eca45739a48b8851b2f3f2d1a0b008bb465b60f265c567f3bf7afd2e`。
- 官方 smkey 资源：`ev_2473dc9ecaf1d22a8d92a90d62ae31f2a7b346f8403a6b8f36b1da0cf266e92b`。
- 上述官方记录均见 `official-sharemouse-rea-evidence.json`。

辅助原生证据（不冒充 REA Evidence）：

- `main-pe.json` / `key-pe.json` / `desk-pe.json`：身份、节表、导入导出。
- `main-methods.json` / `main-message-table.json`：Delphi 方法及消息映射的字节位置。
- `hook-binding.asm` / `helper-evidence.asm`：主 EXE 调 hook，DLL 捕获、回传、抑制输入。
- `network-findings.md` / `network-selected.asm` / `network-evidence.json`：发现、发送、收包分发。
- `receive-mouse.asm` / `receive-coordinates.asm` / `input-injection.asm`：鼠标接收至 SendInput 的条件调用链。
- `sample-provenance.json` / `main-version-resource.txt`：下载来源及版本差异。

全部 VA 为对应 PE 的 preferred image base `0x400000` 下地址，不是运行时 ASLR 地址。主 EXE .text 的文件偏移 = VA − `0x400C00`；此公式不能套用到其他节或 DLL。反汇编中的 `madTraceProcess+…` 等标签只是 objdump 选择的最近导出标签，不是恢复的业务函数名。

| 原始问题 | 当前结果 |
|---|---|
| 模块梳理 | 已识别关键主程序、钩子、辅助与网络模块；非全程序架构穷举 |
| 主/渲染进程 IPC | 原提法不适用已恢复路径；已恢复本机钩子线程与主窗的消息表；其他多实例/桌面 IPC 未全追 |
| 多电脑鼠标共享 | 捕获 → 本机消息 → 坐标累加 → UDP 发送 → 接收分发 → 输入注入已有静态调用证据 |
| 设备搜索/发现 | 默认端口、定时发现方法和发现命令已定位；字段含义未全部恢复 |
| 双机运行、报文时序、延迟/丢包处理 | 未执行、未抓包，仍未知 |

安装不会自动补齐反编译引擎；进一步确认线上报文和跨屏体验，需要可控 Windows 双机环境运行官方程序。当前静态证据已足以解释主要机制，无需先在你的电脑安装。
