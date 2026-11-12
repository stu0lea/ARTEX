---
name: ir-collect
description: 单机应急响应只读采集与研判。当用户提到应急响应、IR、主机排查、恶意进程、挖矿/勒索告警、日志分析、时间线还原、取证采集时使用。仅支持 Linux 和 Windows 主机的只读操作，严禁任何改变现场的动作。
---

# 应急响应（单机只读采集 + 研判）

**本 skill 是应急响应 agent（role=assistant）的专用采集工作流。** 在已授权主机上执行**只读**采集，不改变现场；采集完成后基于产物做研判、时间线、报告。

---

## 铁律（Agent 必读 · 违反即越界）

### 绝对禁止

| 禁止 | 替代做法 |
|---|---|
| kill 进程、stop 服务、重启主机 | 先隔离（网络层/WAF），不碰主机内部 |
| 删除/修改文件、改配置、跑安装脚本 | 只读查看；需要隔离由用户/运维执行 |
| 发包扫描、跑 exploit、横向移动 | 仅采集当前主机，不向外探测 |
| 修改时间、改 NTP | 保留原始时间戳，采集时记录 NTP 偏差 |
| 在受害机上安装软件 | 工具放 U 盘或网络共享，**只运行便携版** |
| 执行任何"清理""恢复""修复"动作 | 只给建议，不执行 |

### 必须

- **每条结论必须附命令原文 + 原始输出片段**，不得凭印象总结
- **证据不完整时写"未确认"**，不得推测填补
- 采集前先记录：主机名、IP、时区、NTP 状态、采集开始时间
- 采集包立即算 SHA256 并存档

---

## 准备

```bash
# 确认平台
uname -s           # Linux
powershell.exe -v  # Windows（或 cmd: ver）

# 创建输出目录（在**外部存储**，不要放受害机上）
mkdir -p /mnt/ir-case-<hostname>/   # Linux
net use Z: \\ir-server\share\ir-case-<hostname>   # Windows
```

---

## 快速路径：用采集脚本

脚本在 `<skill_dir>/scripts/`：

| 脚本 | 适用 | 说明 |
|---|---|---|
| `collect.sh` | Linux | 一键只读采集；root 权限更全 |
| `collect.ps1` | Windows | PowerShell（建议管理员运行） |

**技能加载时会给出本 skill 的目录路径**；不确定时用 `find / -path '*/ir-collect/scripts' -type d 2>/dev/null | head -1` 定位。

### Linux

```bash
# 一键采集（输出到当前目录，建议 cd 到外部存储再跑）
sudo bash <skill_dir>/scripts/collect.sh -o ./ir-out/

# 或手动逐项（见下方章节）
```

### Windows

```powershell
# PowerShell（管理员）
powershell.exe -ExecutionPolicy Bypass -File <skill_dir>\scripts\collect.ps1 -OutputDir "Z:\ir-out"
```

脚本产出统一的目录结构：

```
ir-out/
  ├─ meta.txt                # 主机信息（hostname/IP/时区/NTP/采集时间）
  ├─ proc/                   # 进程、网络连接、文件句柄
  ├─ boot/                   # 启动项、计划任务、服务
  ├─ log/                    # 关键日志片段
  ├─ net/                    # 当前网络配置与连接
  ├─ file/                   # 可疑路径文件列表（只读不复制）
  └─ hash.txt                # 各子目录 tar.gz 的 SHA256
```

---

## Linux 手动采集（脚本外补项）

脚本已覆盖大部分常见项，以下为补充命令（手动用）：

```bash
# 1) 进程（含完整命令行 + 可执行文件路径）
ps -eo pid,ppid,user,start,args --forest
ls -l /proc/*/exe 2>/dev/null
cat /proc/*/cmdline 2>/dev/null | tr '\0' ' ' | head -50

# 2) 网络连接
ss -antp           # ESTAB/LISTEN
ss -i              # 网卡统计
iptables -L -n -v  # 防火墙规则

# 3) 持久化
systemctl list-timers --all | head -100
systemctl list-units --type=service --state=running
crontab -l && cat /etc/crontab /etc/cron.d/* /etc/cron.daily/* 2>/dev/null
ls -l /etc/profile.d/ /etc/bashrc.d/ 2>/dev/null

# 4) 账号与登录
last -F | head -50
lastb | head -50
awk -F: '$3 >= 1000' /etc/passwd
getent passwd shadow | awk -F: '$2 !~ /^\*|^!/'  # 有口令 shadow 条目

# 5) SUID/SGID/可疑文件
find / -xdev -perm /6000 -type f 2>/dev/null
find /tmp /var/tmp /dev/shm -type f -newer /etc/passwd 2>/dev/null

# 6) 关键日志
grep -E 'Accepted|Failed|Invalid' /var/log/auth.log 2>/dev/null | tail -200
grep -i 'module loaded|error|denied' /var/log/kern.log 2>/dev/null | tail -100
journalctl --since '24 hours ago' | tail -500
```

---

## Windows 手动采集（脚本外补项）

```powershell
# 1) 进程（含命令行 + 签名 + 路径）
Get-Process | Select-Object Id, ParentId, ProcessName, StartTime, Path, @{N='Cmd';E={(Get-CimInstance Win32_Process -Filter "ProcessId=$($_.Id)").CommandLine}}
Get-AuthenticodeSignature (Get-Process).Path -ErrorAction SilentlyContinue | Select Status, Path   # 批量看签名

# 2) 网络
Get-NetTCPConnection | Select LocalAddress,LocalPort,RemoteAddress,RemotePort,State,OwningProcess | Sort RemoteAddress
netstat -ano -b

# 3) 持久化
Get-ScheduledTask | Select TaskName,TaskPath,State
Get-Service | Where StartType -eq Automatic | Select Name,DisplayName,PathName
reg query HKLM\Software\Microsoft\Windows\CurrentVersion\Run /s
reg query HKLM\Software\Wow6432Node\Microsoft\Windows\CurrentVersion\Run /s

# 4) 账号登录
Get-WinEvent -LogName Security -MaxEvents 500 | Where Id -in 4624,4625,4634,4672,4728,4732,4720
Get-WinEvent -LogName 'Microsoft-Windows-TerminalServices-LocalSessionManager/Operational' -MaxEvents 200

# 5) Prefetch / Amcache / Shimcache（有 Eric Zimmerman 工具时）
MFTECmd.exe -f C:\$MFT --csv out\
PECmd.exe -d C:\Windows\Prefetch --csv out\
RECmd.exe -f "HKLM\SYSTEM\CurrentControlSet\Control\Session Manager\AppCompatCache" --csv out\

# 6) 快速时间线（Hayabusa）
hayabusa csv-timeline -d .\evtx -o timeline.csv
chainsaw hunt .\evtx -s sigma/ --mapping mappings/sigma-event-logs-all.yml -o .\out
```

---

## 研判工作流（Agent 侧）

### 阶段 1：快速扫（读 JSONL / CSV / 纯文本）

```bash
# JSONL 逐行 grep
jq -c '. | select(.CommandLine | test("powershell -enc|cmd /c|rundll32"))' *.json 2>/dev/null

# DuckDB 直接 SQL（不用导入数据库）
duckdb -c "SELECT Pid, Name, CommandLine FROM read_json_auto('proc/*.json') LIMIT 50"
duckdb -c "SELECT * FROM read_csv_auto('timeline.csv') WHERE EventID IN (4625,4728)"
```

### 阶段 2：研判五问

| # | 看什么 | 可疑特征 |
|---|---|---|
| 1 | **外连** | 陌生公网 IP、非常用端口、短周期心跳 |
| 2 | **进程树** | `w3wp`/`sqlservr` 派生 `cmd`/`powershell`；父进程已退出 |
| 3 | **持久化** | 启动项/服务/计划任务近期新增且路径不常见 |
| 4 | **账号** | 本地管理员新增、RDP 爆破成功日志、异常时间登录 |
| 5 | **落地** | 临时目录/用户目录近期 exe/dll/脚本 |

### 阶段 3：时间线

把文件时间、日志时间、进程启动时间**统一时区**（采集脚本会记主机时区 + NTP 偏差），合并成：

```
时间(UTC) | 主机 | 事件类型 | 描述 | 证据来源
```

### 阶段 4：结论与交付

报告必须包含：

- **概述**：确认/否认感染；置信度（高/中/低）
- **影响范围**：哪些主机、哪些账号、哪些数据
- **时间线**：阶段 3 产出
- **IOC 清单**：IP/域名/哈希/文件路径/注册表项
- **处置建议**：**只建议不执行**——隔离、封禁、清除、轮换凭据
- **证据来源**：每条结论对应命令原文与输出片段

---

## 证据固定（Agent 只记录，不操作）

| 项目 | 说明 |
|---|---|
| SHA256 | 采集完成后，记录各子目录 tar.gz 的哈希到 `hash.txt` |
| 时间基准 | 主机时区、NTP 状态、采集开始/结束时间已在 `meta.txt` |
| 交接单模板 | Agent 需在报告末尾附上：采集人、主机名、IP、采集时间、外部存储路径 |

---

## 常见坑

| 现象 | 原因 | 处理 |
|---|---|---|
| Linux 下某些 `/proc/<pid>/` 读不到 | 权限不足 | 用 `sudo` 重跑；或在输出里如实写"权限不足" |
| Windows 事件日志中文但字段错位 | 时区差异 | 先统一时区再做时间线；报告里注明主机时区 |
| JSONL 用 `jq '.'` 报错 | JSONL 一行一个对象，不是数组 | 用 `jq -c '.'` 或 `jq -s '.'` |
| DuckDB SQL 里找不到字段 | JSON 字段名大小写敏感、嵌套需展开 | 先用 `duckdb -c "DESCRIBE SELECT * FROM read_json_auto('file.json')"` 看 schema |
| 受害机上文件时间乱序 | NTP 漂移 / 人为改过 | 对比多源（日志 + 文件时间 + Prefetch）找一致点 |
| 采集包里出现敏感数据 | 采集范围过宽 | 报告里对明文凭据做脱敏；交接时提醒 |

---

## 脚本位置

```
<skill_dir>/scripts/collect.sh   # Linux
<skill_dir>/scripts/collect.ps1  # Windows
```

不确定路径时：

```bash
# Linux / Mac
find / -path '*/ir-collect/scripts' -type d 2>/dev/null | head -1

# Windows (PowerShell)
Get-ChildItem -Path C:\ -Recurse -Directory -Filter scripts -ErrorAction SilentlyContinue |
  Where { $_.FullName -match 'ir-collect' } | Select -First 1 -ExpandProperty FullName
```
