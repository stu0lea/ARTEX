---
name: md2docx-report
description: 把 ARTEX「发现 → 导出报告」的 Markdown 转成客户模板 Word 报告时使用；支持单条漏洞报告与「漏洞发现汇总报告」。
---

# 漏洞报告转 Word

输入是 `GET /api/exploration/findings/export` 产出的 Markdown（`format=md-single` 的汇总报告，或
`format=md-zip` 里一漏洞一文件的单条报告）。直接喂给 md2docx，不要自己重排 Markdown。

## 可执行文件

- 容器内（ARTEX agent 默认环境）：`/app/skills/md2docx-report/bin/md2docx`

从 zip 上传安装时执行位会丢，首次运行若报 `Permission denied`，先
`chmod +x /app/skills/md2docx-report/bin/md2docx` 一次即可。

模板已放在二进制同目录的 `report-template.docx`，无需额外指定。

## 用法

```sh
# 单文件：省略 -out 时在 Markdown 同目录生成同名 .docx
/app/skills/md2docx-report/bin/md2docx -in findings-20261110-120000.md

# 指定输出
/app/skills/md2docx-report/bin/md2docx -in /app/data/task-6/findings.md -out /app/data/task-6/漏洞报告.docx

# 批量：输入目录下所有 .md，-out 必须是目录
/app/skills/md2docx-report/bin/md2docx -in ./export -out ./word
```

图片相对路径默认按 Markdown 所在目录解析；Markdown 与图片目录分离时用 `-base-dir` 指定图片根目录。

## 封面字段

默认从 Markdown 提取，提取不到再用模板占位。需要修改时显式覆盖：

- `-project` 项目名（汇总报告取「所属任务」，单条报告取任务名）
- `-title` 漏洞标题，`-severity` critical/high/medium/low/info，`-location` 漏洞位置
- `-date` 报告日期，格式 `2026年11月10日`
- `-template` 换模板，也可用环境变量 `ARTEX_REPORT_TEMPLATE`

## 校验

转换成功不代表内容对。打开 docx 的 `word/document.xml` 确认关键文字存在，例如：

```sh
unzip -p 漏洞报告.docx word/document.xml | grep -c "修复建议"
```

`.md` 里没有正文、只有表头时，先回到导出环节确认 `scope`/`ids` 选对了漏洞。

`-out -` 写出的是 docx 二进制，只能重定向到文件，不要打到终端；从标准输入 `-in -` 转换时同样要给 `-out`。
