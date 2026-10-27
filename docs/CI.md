# GitHub Actions 构建与发布

主分支推送、Pull Request 和手动触发均运行 `.github/workflows/build.yml`。构建时校验核心、Windows 界面、macOS Bundle 版本一致；标签构建还要求标签严格匹配 `v<应用版本>`。

## 构建产物

| 工作项 | 运行环境 | 主要产物与检查 |
|---|---|---|
| macOS arm64 CLI | macos-15 | 原生测试、race、三种网络验证、CLI ZIP |
| macOS amd64 CLI | macos-15-intel | 原生测试、race、三种网络验证、CLI ZIP |
| Windows amd64 CLI | windows-2025 | 原生测试、vet、三种网络验证、CLI ZIP |
| Windows arm64 CLI | windows-2025 amd64 | 宿主机测试后交叉编译，ARM64 CLI ZIP |
| macOS 桌面安装包 | macos-15 | Apple Silicon / Intel 通用 `.app`，DMG、ZIP、SHA-256；验签、ZIP 解压、只读挂载 DMG，并验证挂载包内的核心网络功能 |
| Windows 桌面安装包 | windows-2025 | NSIS 3.12.0 自动选择 x64 / ARM64 的 Setup，两个原生便携 ZIP 和 SHA-256；检查 PE / GUI 子系统，静默安装、验证已安装核心网络功能并卸载 |

桌面任务在四个平台的基础测试通过后才开始。macOS 仅使用本地 ad-hoc 签名，未公证；Windows 未做 Authenticode 签名。

运行完成后，在 Actions 运行详情中下载：

- `release-desktop-macos`：`MouseBridge-<版本>-macos-universal.dmg`、`.app` ZIP 和校验值。
- `release-desktop-windows`：`MouseBridge-<版本>-windows-setup.exe`、x64 / ARM64 便携 ZIP 和校验值。
- `release-cli-*`：四个平台/架构的命令行包。
- `checks-desktop-macos` / `checks-desktop-windows`：当前实际构建的验证报告。

上传使用明确的文件匹配规则，不上传 `.app` 文件夹、工作目录或本地历史产物。

## 标签发布

推送匹配应用版本的 `v*` 标签后，Release 任务等待基础测试和两个安装包任务全部成功；合并 `release-*` 产物后检查 DMG 和 Setup 同时存在，并逐个验证 SHA-256。任一安装包缺失或校验不符就终止发布。

通过后创建或更新同名预览 Release，包含两种安装包、便携包和校验值。仓库文档按项目交付约定保存在 `docs` 分支；程序随包附带的运行说明位于主分支 `resources/`，构建不依赖本地未提交文档。

## 验证边界

全部网络测试使用 Recorder，不捕获或注入真实系统鼠标。Windows 云端安装/卸载检查不代表 GUI 交互、UAC 安全桌面、系统权限或多台实体电脑的鼠标已经验收。Windows ARM64 仍为交叉编译；每个安装包的 `BUILD-INFO.json` 和同次 Actions 检查报告记录实际来源与范围。

## 历史记录

旧版首次公开 CLI 构建：[运行 39933920012](https://github.com/xiaohuizli/mousebridge/actions/runs/39933920012)，对应旧提交 `58e66934b824c2d8a067109787da15679d18b113`，不能用来证明当前桌面安装包已通过。

## 本次更新验证（2026-10-27）

最新成功构建：[运行 40458653242](https://github.com/xiaohuizli/mousebridge/actions/runs/40458653242)，对应 `main` 提交 `737c23cbdb5d250c577397800cba8df67bec632c`。

四个平台的测试/CLI 构建以及两个桌面安装包任务全部通过。macOS DMG 已在云端挂载验签并验证内置核心；Windows Setup 已在 Windows runner 上通过静默安装、安装后的三进程核心网络验证及卸载。两种安装包和相应校验值已作为 Actions 产物上传。

首次尝试 [运行 40456745921](https://github.com/xiaohuizli/mousebridge/actions/runs/40456745921) 中，Mac 任务成功，Windows 因 NSIS 文件路径混用分隔符失败。提交 `737c23c` 改为传入系统原生完整路径后重建通过；未把首次失败记为成功。

本次触发为主分支推送，没有创建版本标签，因此 Release 任务按设计跳过；两种安装包可在成功运行的 Artifacts 中下载。标签发布仍按上文规则执行。Windows GUI 交互、ARM64 原生运行和实体机鼠标互控仍不在这次自动化验收范围内。
