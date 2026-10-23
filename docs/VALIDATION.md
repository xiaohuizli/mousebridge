# 实现验证记录

验证日期：2026-10-23。环境：macOS 26.5 arm64，Go 1.25.12。最终执行结果由 [summary.json](validation/summary.json) 记录，含命令、退出码、通过测试名及四个二进制的 SHA-256。

**结论：源码、协议/状态机、网络联调和四个目标构建通过；原生双机输入验收尚未完成。** 本次没有给终端新增系统权限，也没有运行 `control -live` 或 `receive -live`。

## 已执行

| 验证 | 结果 | 证据 |
|---|---|---|
| `go test -race -count=1 -json ./...` | 13 个顶层测试、5 个子测试通过；未报告 Go 数据竞争 | [tests.log](validation/tests.log) |
| `go vet ./...` | exit 0 | [vet.log](validation/vet.log)（无诊断输出） |
| Windows目标 `go vet ./...` | exit 0 | [vet-windows.log](validation/vet-windows.log) |
| `make build` | macOS arm64 / amd64、Windows amd64 / arm64 全部编译成功 | [build.log](validation/build.log) |
| 两个 CLI 进程、真实 loopback UDP/TLS/TCP | 发现、配对、移动、按钮、拖动、滚轮、返回、心跳、错误密钥拒绝通过 | [smoke.json](validation/smoke.json) |

不把交叉编译通过写成 Windows 上运行通过；Intel Mac 与 Windows arm64 同理。Go race 检测覆盖本次执行的 Go 路径，不证明未执行的原生钩子和 C 回调不存在竞态。

## 关键断言

- 1920×1080 接收屏从左边进入：归一化高度0.5映射至 `(1,540)`；位移 `(100,20)` 后为 `(101,560)`；真实进程记录与独立固定预期一致。
- 远端鼠标按下后返回、连接关闭、静默超时、非法命令、未知字段和超长帧均触发按钮释放。
- 左右入口、另一侧边界钳制、返回后的在途事件忽略、正在本机拖动时不跨屏、Escape 返回与发送失败恢复通过。
- TLS 双方同密钥握手成功，异密钥失败；第二个已认证控制端收到 busy。
- UDP 发现拒绝错误密钥、伪造签名、过期消息、上一请求 nonce 的响应。
- Windows目标静态检查最初报告回调 uintptr 转指针的风险；改为原生指针类型回调参数，保留回调期内的指针生命周期后检查通过。
- 监听器关闭时结束活动会话。该测试最初失败（等待旧会话），修复为统一取消会话后通过。

## 可重复执行

在 macOS arm64 开发机，项目目录中执行：

```sh
python3 scripts/verify.py
```

脚本运行测试、静态检查、四目标构建、两个进程联调，并刷新 `docs/validation/`。配对密钥仅存在于测试进程的内存与环境中，不写入日志。网络联调将桌面替换为 Recorder，实际使用操作系统 UDP/TCP socket 与 TLS 握手。

## 未执行与原因

| 检查 | 原因 / 影响 |
|---|---|
| Windows真实钩子和 SendInput | 当前没有可用的 Windows 交互式运行环境；只能交叉编译 |
| macOS真实 Event Tap / CGEventPost | 本轮没有进行用户桌面输入操作或权限配置；编译通过不足以验证系统授权及事件显示 |
| 两台电脑局域网联调 | 当前仅有本机环境；没有第二台已配对设备 |
| 真机断网、睡眠、锁屏、UAC | 缺少对应环境与交互；强杀进程无法保证释放系统按钮 |
| Retina / Windows DPI、双击、横向滚动、自然滚动、惯性 | 需要两平台实际应用验收；已有适配代码不能代替体验验证 |
| 高频鼠标、拥塞、1小时稳定性 | 未开展性能测试，不提供时延或稳定性承诺 |

执行步骤和成功标准见 [BEHAVIOR.md](BEHAVIOR.md) 的真机验收矩阵。以上限制属于明确保留的验收缺口，项目当前应按 v0.1 原型使用。
