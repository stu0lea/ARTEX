---
name: hunter
description: 使用奇安信鹰图（Hunter）API 做网络空间资产测绘：按 IP、域名、端口、指纹、证书、标题等查询互联网资产，并把结果登记进资产图。当用户提到鹰图、Hunter、空间测绘、IP 反查域名、同 IP 旁站、指纹/证书搜索、资产扩展时使用。
mcps: hunter
---

# 鹰图 Hunter 资产测绘

在**已授权**范围内，用鹰图 API 查询互联网资产，并把结果通过 `insert_assets` 登记进资产图，供后续测试复用。

## 授权边界（必读）

- 只查询**任务授权范围内**的目标（用户给定的根域名 / IP / 企业）。
- 本 skill **只做资产测绘**，不做漏洞利用；查到的资产是"待测面"，不是"已确认漏洞"。
- 不把鹰图返回的 `is_risk` / `vuln` 类字段当成已验证漏洞，需要验证时另行测试。

## 配置

API Key 按以下优先级读取（脚本已实现，改一处即可）：

1. `--api-key` 参数（不推荐，会进命令历史）
2. 环境变量 `HUNTER_API_KEY`
3. 文件 `<skill_dir>/.hunter_key`（推荐：`./skills` 挂载自宿主机，改文件即可）
4. 环境变量 `HUNTER_API_KEY_FILE` 指向的文件

- **禁止**把 API Key 写入本文件、命令行明文或报告。缺失时直接报错停止，不要猜。
- 脚本自带 base64 编码、分页和错误处理，不要手写 curl 拼接。

## 用法

优先用 MCP 工具（若本 skill 的 `mcps` 已解锁，工具名为 `mcp__hunter__hunter_search` /
`mcp__hunter__hunter_map_assets`）；MCP 未配置时用下面的脚本。

技能加载时会给出本 skill 的目录路径；脚本在 `<skill_dir>/scripts/hunter.py`。不确定路径时用
`find / -path '*/hunter/scripts/hunter.py' 2>/dev/null | head -1` 定位。

```bash
# 1) 查询（输出精简结果，省 token）
python3 <skill_dir>/scripts/hunter.py --query 'domain.suffix="example.com"' --size 10

# 2) 查询 + 直接产出 insert_assets 入参（推荐两步走）
python3 <skill_dir>/scripts/hunter.py --query 'domain.suffix="example.com"' --size 20 --map-assets
```

常用参数：

| 参数 | 说明 |
|---|---|
| `--query` | 鹰图查询语句（脚本自动 base64 编码） |
| `--size` | 每页条数，默认 10，上限 50（**别调大，按积分计费**） |
| `--page` | 页码，默认 1；**最多翻到第 2 页** |
| `--is-web` | 1=Web 资产，2=非 Web，3=全部（默认 3） |
| `--start-time` / `--end-time` | `YYYY-MM-DD`，按更新时间过滤 |
| `--map-assets` | 输出 `{"assets":[...]}`，可直接作为 `insert_assets` 的 `assets` 参数 |
| `--raw` | 输出鹰图原始 JSON（仅排障用，别默认开） |
| `--no-proxy` | 直连，不走流量录制代理 |

## 查询语法要点

| 目的 | 示例 |
|---|---|
| 按 IP | `ip="1.2.3.4"` |
| 域名后缀 | `domain.suffix="example.com"` |
| 精确域名 | `domain="www.example.com"` |
| 端口 + 指纹 | `ip="1.2.3.4" && port="8080" && web.title="后台"` |
| 标题/组件 | `web.title="登录" && web.component="Nginx"` |
| 证书反查 | `cert="example.com"` |
| 排除 CDN / 只看主域 | `ip="1.2.3.4" && is_domain=false` |
| 备案/单位 | `icp.name="某某公司"` |

- 支持 `&&`（与）、`||`（或）、`!=`（非）、括号分组。
- 字段名以鹰图官方文档为准；不确定时先用宽条件（如 `domain.suffix`）再收窄。
- 查询为 0 条时先检查语法（引号、字段名），**不要原样重复请求**。

## 落库：结果 → `insert_assets`

`--map-assets` 已按下列规则生成 `insert_assets` 入参，核对后用 `insert_assets` 写入：

| 鹰图结果 | 资产类型 | 关键字段 |
|---|---|---|
| `is_web=1` 且有 `url` | `service`（HTTP） | `url`、`status_code`、`page_title=web_title`、`technologies=component`、`ip` |
| 有 `protocol`+`port`（非 Web） | `service`（非 HTTP） | `service_name=protocol`、`port`、`ip` 或 `domain` |
| `domain` + `ip` | `subdomain` | `domain`、`record_type="A"`、`record_value=[ip]` |
| 仅 `ip` | `ip` | `ip`、`bound_domains`、`open_ports=[{port,service}]` |

写入要求：

1. **一次批量提交**，不要把每条结果单独调用一次 `insert_assets`。
2. 重复资产由系统按去重键合并（upsert），重复登记是幂等的，但仍不要为凑数反复插入。
3. `insert_assets` 返回 `{results, errors}`；有 `errors` 时按提示修正字段再补一次，**不要整批重试**。
4. 只登记资产；发现漏洞走 `report_finding`，两者不要混用。

## 积分与纪律

- 每次查询消耗积分：**默认 `--size 10`，单次任务查询不超过 5 次，翻页不超过 2 页**。
- 返回"积分不足"立即停止并告知用户，禁止换条件重试轰炸。
- 同一任务内相同 `--query` 不重复查询；先看资产图里是否已有。

## 常见错误

| 现象 | 处理 |
|---|---|
| `HUNTER_API_KEY not set` | 让用户配置环境变量，停止 |
| `401` 认证失败 | Key 失效，停止 |
| `402` / 积分不足 | 立即停止，报告已消耗情况 |
| `429` 请求过于频繁 | 等待后仅重试 1 次，仍失败则停 |
| 返回 0 条 | 检查查询语法，换条件；不要重复同样请求 |
| `insert_assets` 报字段错误 | 按 `errors` 里的 index 修正对应条目后补交 |
