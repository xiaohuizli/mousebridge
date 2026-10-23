# MouseBridge v1 协议与行为规格

## 设计取舍

ShareMouse 样本证明鼠标消息经 UDP 发送，但本项目选择 TCP + TLS 1.3，以可靠有序的字节流传输按钮和移动，避免自行设计丢包重传与加密协议。代价是网络拥塞时存在队头阻塞；256 个事件的发送队列满时断开并恢复本机控制。尚未测量无线拥塞下的延迟。

## 发现

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

进入远端前及返回后收到的在途 move/button/wheel 返回 idle，不注入；直到下一次 enter。再次 enter 必须处于非远端状态。原生适配接口中的 move 是绝对主屏坐标，而网络 move 是相对位移。

## 状态与故障

- 本机没有按下鼠标按钮、沿指定方向运动且抵达边缘，才进入远端。
- 本机光标停在中央，物理移动被钩子抑制并转发。只转发三种按钮，不转发扩展侧键。
- 远端返回、Escape、队列溢出、网络错误时恢复本机光标到对应边缘内侧，300ms 内不再次切换。
- 远端按键按下状态记在 Machine；离开/EOF/静默 2 秒/非法命令/注入出错均执行释放。
- 心跳间隔400ms；读/写超时2秒。系统调度、阻塞的原生 API、操作系统强制杀进程或断电不受这一上界保证。特别是接收端被强杀时无法执行清理，必要时在 B 本机重新点击以解除系统按键状态。
- 无自动重连；连接失败退出，用户重新运行控制命令。
- 两端自己产生的移动带标记；macOS 忽略本程序标记，Windows 忽略注入事件，防止回环。

## 原生实现限制

Windows 使用 WH_MOUSE_LL、WH_KEYBOARD_LL（仅 Escape）和 SendInput；主显示器坐标按 0..65535 归一化，启用进程 DPI awareness。macOS 使用 CGEventTapCreate、CGEventPost、主显示器逻辑坐标；鼠标拖动按按钮状态生成 dragged 事件。macOS 点击计数采用500ms、4点范围的规则，支持生成双击/三击 clickState，但尚未对应用实际响应进行验证；不是复制系统可调双击偏好。macOS 非整数滚轮量先累计再注入整行，不保持触控板惯性/相位信息。

API 依据：
- [Microsoft LowLevelMouseProc](https://learn.microsoft.com/en-us/windows/win32/winmsg/lowlevelmouseproc)
- [Microsoft MOUSEINPUT](https://learn.microsoft.com/en-us/windows/win32/api/winuser/ns-winuser-mouseinput)
- [Apple Quartz Event Services](https://developer.apple.com/documentation/coregraphics/quartz-event-services)
- [Apple mouseEventClickState](https://developer.apple.com/documentation/coregraphics/cgeventfield/mouseeventclickstate)

这些文档支持 API 用法，不替代双机行为验收。

横向滚动以协议 x 正值向右；macOS 捕获/注入边界反转 axis2 符号，Windows 保留 HWHEEL 符号。该映射核对了 [Input Leap 官方 OSXScreen 实现中的平台转换](https://github.com/input-leap/input-leap/blob/master/src/lib/platform/OSXScreen.mm)；此处仅参考坐标方向事实，没有引入其源码。跨平台实际滚动效果仍列入待验收项。
