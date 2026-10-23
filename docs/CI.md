# GitHub Actions 验证

首次公开构建已通过：[运行 39933920012](https://github.com/xiaohuizli/mousebridge/actions/runs/39933920012)，源码提交 `58e66934b824c2d8a067109787da15679d18b113`。

| 目标包 | 构建与测试环境 | 结果 |
|---|---|---|
| macOS arm64 | macos-15，原生测试、race、网络冒烟 | 通过 |
| macOS amd64 | macos-15-intel，原生测试、race、网络冒烟 | 通过 |
| Windows amd64 | windows-2025，测试、vet、网络冒烟 | 通过 |
| Windows arm64 | windows-2025 amd64主机测试后交叉编译 | 通过；未在ARM64系统运行 |

所有网络冒烟均使用 Recorder，不调用真实输入钩子或注入。云构建通过不代表交互式桌面、系统权限或双机操作已经验收。历史本地验证快照保留于 `docs/validation/`，与每个发布包的 `BUILD-INFO.json` 分开记录；包内二进制以自己的校验值和源码提交为准。

后续主分支与标签构建见 [Actions](https://github.com/xiaohuizli/mousebridge/actions)。
