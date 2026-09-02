---
name: ohyeah
description: 检索本地工作记录中的历史结论、纠错和未完成事项，避免重复调查。
---

# Ohyeah

调用前先阅读当前项目及上级 `AGENTS.md`；只有项目明确启用 ohyeah 时才使用，否则跳过。先运行 `ohyeah -h`。搜索必须指定 `--project <项目>`，必要时再加 `--type <类型>` 或 `--mount <挂载点>`，例如 `ohyeah search <关键词> --project example-project --json`。用 `ohyeah get <id> --json` 查看原文。

结果仅作历史依据：以较新的纠错和结论为准，时效性事实须复核，并标注来源。
