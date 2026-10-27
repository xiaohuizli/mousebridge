# MouseBridge v1 协议与行为规格

## 设计取舍

ShareMouse 样本证明鼠标消息经 UDP 发送，但本项目选择 TCP + TLS 1.3，以可靠有序的字节流传输按钮和移动，避免自行设计丢包重传与加密协议。代价是网络拥塞时存在队头阻塞；256 个事件的发送队列满时断开并恢复本机控制。尚未测量无线拥塞下的延迟。

## 手动密钥发现

默认 UDP 42047；IPv4 请求广播、响应单播。JSON envelope 包含原始 `payload` 和 Base64 `mac`；mac 为 HMAC-SHA256(pairKey, `mousebridge/v1/discovery:` || 原始 payload)。payload 包含 `v:1`、kind、128 位随机 nonce、Unix 秒时间。响应 kind 为 receiver，追加 name 和 port，并回显 nonce。

时间差最多 30 秒；包最多 2048 字节；本次请求只接受匹配的 nonce；发现客户端最多收集 32 个地址。应答 IP 取 UDP 来源地址，不相信包内地址。自动发现不代表身份独立：同组共享配对身份；不提供每台主机的独立撤销、授权弹窗或设备证书管理。响应只向通过签名校验的请求发送。

## 会话

默认 TCP 42048，TLS 最低版本 1.3，ALPN `mousebridge/1`，双方均提供自签名 Ed25519 证书。身份种子为 HMAC-SHA256(pairKey, `mousebridge/v1/tls-identity`)。固定对端公钥代替公共 CA/主机名校验；客户端和服务端都必须验证此公钥，服务端要求客户端证书。每个 TLS 会话有独立握手密钥与记录保护，不设计自有加密。配对密钥必须是 keygen 生成的 32 字节随机值。

认证后服务端独占分配控制权，返回 ready；已有控制方时返回 busy 并关闭。最多 8 个并行握手，每个最多 2 秒。不保存 TLS 会话或应用数据到磁盘。

## 命令

UTF-8 JSONL，每帧连同换行不超过 2048 字节；拒绝未知字段、未知命令及尾随数据。处理完每个事件均有应答；这些应答表示应用处理完成，不等价于操作系统实际显示的验收证据。

| kind | 参数 | 行为 / 应答 |
|---|---|---|
| enter | edge=left/right；y∈[0,1] | A 从 right 离开则 B 从 x=1 进入；从 left 离开则从 x=width−2 进入；y 四舍五入映射到远端高度；entered |
| move | x/y 相对位移，绝对值≤32768 | 累计为远端绝对坐标；跨入口边缘则释放按键并 return（带归一化 y）；其他边缘钳制 |
| button | button=0/1/2；down 布尔 | 左/右/中键；ok |
| wheel | x/y，绝对值≤100 | 向原生适配器提交滚动；ok |
| leave | 无 | 释放所有由本会话按下的按钮；return |
| ping | 无 | pong |

进入远端前及返回后收到的在途 move/button/wheel 返回 idle，不注入；直到下一次 enter。再次 enter 必须处于非远端状态。原生适配接口中的 move 是相对虚拟桌面左上角的绝对坐标，而网络 move 是相对位移。

## 状态与故障

- 本机没有按下鼠标按钮、沿指定方向运动且抵达边缘，才进入远端。
- 本机光标停在中央，物理移动被钩子抑制并转发。只转发三种按钮，不转发扩展侧键。
- 远端返回、Escape、队列溢出、网络错误时恢复本机光标到对应边缘内侧，300ms 内不再次切换。
- 远端按键按下状态记在 Machine；离开/EOF/静默 2 秒/非法命令/注入出错均执行释放。
- 心跳间隔400ms；读/写超时2秒。系统调度、阻塞的原生 API、操作系统强制杀进程或断电不受这一上界保证。特别是接收端被强杀时无法执行清理，必要时在 B 本机重新点击以解除系统按键状态。
- 手动 `control` 命令连接失败退出；`auto` 模式通过持续局域网发现与已保存设备信任自动重连。
- 两端自己产生的移动带标记；macOS 忽略本程序标记，Windows 忽略注入事件，防止回环。

## 原生实现限制

Windows 使用 WH_MOUSE_LL、WH_KEYBOARD_LL（仅 Escape）和 SendInput；虚拟桌面坐标按 0..65535 归一化，并设置 MOUSEEVENTF_VIRTUALDESK，启用进程 DPI awareness。macOS 使用 CGEventTapCreate、CGEventPost、所有活动显示器组成的联合逻辑坐标；鼠标拖动按按钮状态生成 dragged 事件。macOS 点击计数采用500ms、4点范围的规则，支持生成双击/三击 clickState，但尚未对应用实际响应进行验证；不是复制系统可调双击偏好。macOS 非整数滚轮量先累计再注入整行，不保持触控板惯性/相位信息。

API 依据：
- [Microsoft LowLevelMouseProc](https://learn.microsoft.com/en-us/windows/win32/winmsg/lowlevelmouseproc)
- [Microsoft MOUSEINPUT](https://learn.microsoft.com/en-us/windows/win32/api/winuser/ns-winuser-mouseinput)
- [Apple Quartz Event Services](https://developer.apple.com/documentation/coregraphics/quartz-event-services)
- [Apple mouseEventClickState](https://developer.apple.com/documentation/coregraphics/cgeventfield/mouseeventclickstate)

这些文档支持 API 用法，不替代双机行为验收。

横向滚动以协议 x 正值向右；macOS 捕获/注入边界反转 axis2 符号，Windows 保留 HWHEEL 符号。该映射核对了 [Input Leap 官方 OSXScreen 实现中的平台转换](https://github.com/input-leap/input-leap/blob/master/src/lib/platform/OSXScreen.mm)；此处仅参考坐标方向事实，没有引入其源码。跨平台实际滚动效果仍列入待验收项。

## v0.2 自动发现与首次配对

独立于旧的密钥签名发现，`auto` 模式在 UDP 42049 广播公开设备 ID（Ed25519 公钥 SPKI 的 SHA-256）、电脑名和动态 TCP 配对端口，接收时使用 UDP 来源 IP。广播和首次发现均不建立信任。设备 ID 较小的一端主动建立连接，避免同时建立相反的控制会话；只处理一个配对设备。

配对连接使用 TLS 1.3，ALPN `mousebridge/auto/1`，双方提供由各自钥匙串私密身份派生的 Ed25519 证书。新设备没有 CA 信任，需在两端核对源于 TLS exporter `mousebridge/auto/v1/display-code` 的 8 位数字并批准。未确认、拒绝、过期（90 秒）或旧会话的确认均不能生成信任或启动输入；审批令牌来自同一 TLS 通道且只在当前进程中有效。双方批准后，由 exporter `mousebridge/auto/v1/shared-key` 派生 32 字节共享密钥。

已配对重连须公钥 ID 一致，并以共享密钥计算消息认证码，验证该 TLS 通道的绑定值和发送方身份。两端通过加密状态帧交换角色偏好、共享开关和动态输入接收端口；双方开启时自动选择互补角色。角色冲突或任一端暂停会取消输入会话。控制流仍使用原有 TLS 双向认证和事件协议。连接断开后取消输入会话并重新发现，每次重连重新创建输入监听端口。每次连接只有一个读取协程；状态帧最大 4096 字节，静默 5 秒断开；握手限时 3 秒且最多 4 路。

私密身份及每台设备的共享密钥由 `.app` 保存在 macOS 钥匙串，以环境变量传入后台服务；首次派生的新信任通过私有 JSON 管道交给界面保存。普通日志不记录密钥。GUI 标准输入关闭时后台服务取消上下文并释放输入，避免界面异常退出遗留控制。旧的手动命令继续兼容，Windows CLI 也可运行自动服务，但当前自动配对图形界面仅提供 macOS 版。

## v0.3 局域网开放共享（macOS 应用默认）

`auto -open-lan` 使用 UDP 42049、广播版本 `v:2` 与独立 ALPN `mousebridge/lan/1`，与审批模式隔离。开启发现是本机对同一直接连接局域网内所有开放实例的授权，不要求数字确认或事先信任名单。仅允许直接连接的可广播网络接口来源与回环地址；广播接收使用来源 IP。

设备 ID 较小的一端发起每一对 TLS 连接，双方发送 `open` 帧后立即派生临时输入密钥（TLS exporter `mousebridge/lan/v1/input-key`），无需申请审批，也不持久化对方身份或通道密钥。设备公钥 ID 与广播须在当前通道一致，但此一致性不代表用户审批身份。每对电脑独立密钥，避免其他开放实例复用另一条输入通道的身份。最多同时连接 16 个其他设备，握手最多并行 8 路、3 秒超时；每帧最多 4096 字节、状态静默 4 秒断开。

每台主机向所有连接发布独立接收端口，同时向当前选中的一个目标运行控制端；新发现时自动选择首个设备，用户可从列表切换。全局发送槽保证每台机器只有一个捕获回调，切换会取消并等待旧回调退出。接收端各连接共用输入租约：进入时原子取得该桌面，已被占用则返回 `return`，不注入任何事件；返回、断线、取消和协议错误先释放按下的按钮，再释放租约。不同会话保持空闲连接，前一操作者离开后可立即尝试接管。macOS 原生注入使用进程级互斥，保护共享 C 输入状态；注入事件标记仍用于排除回环。

`settings` 中的 discover=false 停止广播、拒绝新连接、关闭全部已建会话；即使快速关闭再开启，代数变更也会撤销旧会话。enabled=false 仅暂停输入（用于尚未授权系统权限），发现仍可显示其他电脑。标准输入关闭会取消服务并释放全部监听和输入。

## v0.3.1 桌面分发

Mac 与 Windows 图形应用都在每次打开时生成 32 字节随机临时身份，仅在主进程内存及内置核心子进程的环境中传递，退出即丢弃；不读写钥匙串、凭据管理器或身份文件。旧版 macOS 钥匙串记录不访问、不自动删除。每次打开的公开设备 ID 可以改变，开放模式通过发现自动更新设备列表，不依赖持久化白名单。

Windows 原生 Win32 窗口通过同样的 JSON 私有管道管理核心进程，关闭窗口先关闭核心标准输入，以取消服务、释放输入，再退出窗口。NSIS 安装向导按本机架构选择 x64 / ARM64 原生载荷，安装、快捷方式和卸载注册均属于当前用户，不修改防火墙规则或安装系统服务。macOS 使用通用 `.app`、DMG 和 ZIP 分发。当前构建环境没有 Windows GUI 或安装运行验收；PE 架构、编译、归档和核心协议验收不能替代 Windows 真机测试。

## v0.3.2 多显示器边界

控制端以完整桌面矩形判断左右最外侧边缘，同时要求采样点位于桌面范围内且沿外侧运动。显示器之间的接缝和副屏内部不触发 enter。Mac 捕获时减去桌面原点、注入时加回；Windows 使用虚拟桌面尺寸和原点，绝对输入映射到同一矩形。支持显示器位于主屏左侧或上方而产生负坐标的排列；更改显示器布局后需重启共享服务。事件协议不变，Esc、返回和断线释放行为保留。
