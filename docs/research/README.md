# 静态分析依据

本目录保留静态分析报告供设计溯源。完整 REA 记录、汇编片段和分析归档仅保留在本地交付目录，不纳入公开仓库，也不用于本项目编译。

- 官方样本：sharemouse.exe 7.0.31.0
- SHA-256：c6e63d6217b0e476824feafe5efc2b2c4e856e574a778f56d886b5bf2a267a40
- [官方来源](https://www.sharemouse.com/download/)
- REA 负责身份/资源/归档核验，原生调用链来自 LLVM 静态反汇编与 PE/Delphi 元数据解析。
- 没有运行原样本，没有双机抓包。
