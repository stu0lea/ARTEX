#!/usr/bin/env python3
"""鹰图 Hunter 本地 MCP server（stdio / JSON-RPC 2.0，零依赖）。

由 artex 以子进程方式启动，向 stdout 输出 JSON-RPC，日志一律走 stderr。

暴露工具：
  hunter_search      查询资产（精简结果）
  hunter_map_assets  查询并产出 insert_assets 入参
"""

import json
import os
import sys
import types

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import hunter  # noqa: E402  复用查询/映射逻辑

SERVER_NAME = "hunter"
SERVER_VERSION = "1.0.0"
DEFAULT_PROTOCOL = "2024-11-05"

TOOLS = [
    {
        "name": "hunter_search",
        "description": (
            "用奇安信鹰图(Hunter) API 查询互联网资产。仅限已授权目标。"
            "返回精简字段（url/ip/port/domain/title/component/status_code 等）。"
            "默认每页 10 条、上限 50；按积分计费，不要循环翻页。"
        ),
        "inputSchema": {
            "type": "object",
            "properties": {
                "query": {"type": "string", "description": '鹰图查询语句，如 ip="1.2.3.4" 或 domain.suffix="example.com"'},
                "size": {"type": "integer", "description": "每页条数，默认 10，最大 50", "default": 10},
                "page": {"type": "integer", "description": "页码，默认 1，最多 2", "default": 1},
                "is_web": {"type": "integer", "enum": [1, 2, 3], "description": "1=Web 2=非Web 3=全部", "default": 3},
                "start_time": {"type": "string", "description": "更新时间起 YYYY-MM-DD（可选）"},
                "end_time": {"type": "string", "description": "更新时间止 YYYY-MM-DD（可选）"},
            },
            "required": ["query"],
        },
    },
    {
        "name": "hunter_map_assets",
        "description": (
            "查询鹰图并映射为 insert_assets 的入参 {\"assets\":[...]}。"
            "拿到结果后仍需调用 insert_assets 落库（MCP 无法直接写资产图）。"
        ),
        "inputSchema": {
            "type": "object",
            "properties": {
                "query": {"type": "string", "description": "鹰图查询语句"},
                "size": {"type": "integer", "description": "每页条数，默认 20，最大 50", "default": 20},
                "max_assets": {"type": "integer", "description": "最多产出多少条资产，默认 50", "default": 50},
            },
            "required": ["query"],
        },
    },
]


def log(msg):
    sys.stderr.write("[hunter-mcp] %s\n" % msg)
    sys.stderr.flush()


def send(obj):
    sys.stdout.write(json.dumps(obj, ensure_ascii=False) + "\n")
    sys.stdout.flush()


def reply(rid, result):
    send({"jsonrpc": "2.0", "id": rid, "result": result})


def reply_error(rid, code, message):
    send({"jsonrpc": "2.0", "id": rid, "error": {"code": code, "message": message}})


def ns(args):
    return types.SimpleNamespace(
        query=args["query"],
        size=int(args.get("size", 10)),
        page=int(args.get("page", 1)),
        is_web=int(args.get("is_web", 3)),
        start_time=args.get("start_time", "") or "",
        end_time=args.get("end_time", "") or "",
        fields=args.get("fields", "") or "",
        api_key="",
        max_assets=int(args.get("max_assets", 50)),
        raw=False,
        no_proxy=bool(args.get("no_proxy", False)),
        map_assets=False,
    )


def run_query(args, want_assets):
    key = hunter.load_key(types.SimpleNamespace(api_key=""))
    if not key:
        raise RuntimeError("HUNTER_API_KEY not set（env 或 skills/hunter/.hunter_key）")
    resp = hunter.fetch(hunter.build_url(args, key), args.no_proxy)
    code = resp.get("code")
    if code != 200:
        raise RuntimeError("hunter error code=%s message=%s" % (code, resp.get("message")))
    data = resp.get("data") or {}
    arr = data.get("arr") or []
    if want_assets:
        out = hunter.to_assets(arr, args.max_assets)
    else:
        out = {
            "total": data.get("total"),
            "returned": len(arr),
            "consume_credit": data.get("consume_credit"),
            "results": [hunter.compact(x) for x in arr],
        }
    meta = "total=%s returned=%s consume_credit=%s" % (
        data.get("total"), len(arr), data.get("consume_credit"))
    return out, meta


def call_tool(name, args):
    if not isinstance(args, dict):
        raise RuntimeError("arguments 必须是对象")
    if not args.get("query"):
        raise RuntimeError("缺少必填参数 query")

    if name == "hunter_search":
        out, meta = run_query(ns(args), False)
    elif name == "hunter_map_assets":
        out, meta = run_query(ns(args), True)
    else:
        raise RuntimeError("unknown tool: %s" % name)

    log(meta)
    text = json.dumps(out, ensure_ascii=False)
    if name == "hunter_map_assets":
        text += "\n\n（下一步：把上面 assets 数组传给 insert_assets 落库）"
    return {"content": [{"type": "text", "text": text}], "isError": False}


def handle(req):
    method = req.get("method")
    rid = req.get("id")
    params = req.get("params") or {}

    if method == "initialize":
        proto = params.get("protocolVersion") or DEFAULT_PROTOCOL
        reply(rid, {
            "protocolVersion": proto,
            "capabilities": {"tools": {"listChanged": False}},
            "serverInfo": {"name": SERVER_NAME, "version": SERVER_VERSION},
        })
    elif method in ("notifications/initialized", "initialized"):
        pass  # 通知无响应
    elif method == "ping":
        reply(rid, {})
    elif method == "tools/list":
        reply(rid, {"tools": TOOLS})
    elif method == "tools/call":
        name = params.get("name")
        args = params.get("arguments") or {}
        try:
            reply(rid, call_tool(name, args))
        except Exception as e:  # 工具执行错误按 MCP 约定回 isError
            reply(rid, {"content": [{"type": "text", "text": str(e)}], "isError": True})
    elif method and method.startswith("notifications/"):
        pass
    else:
        if rid is not None:
            reply_error(rid, -32601, "method not found: %s" % method)


def main():
    log("started, pid=%d" % os.getpid())
    for raw in sys.stdin.buffer:
        line = raw.decode("utf-8", "replace").strip()
        if not line:
            continue
        try:
            req = json.loads(line)
        except ValueError:
            log("invalid json: %s" % line[:200])
            continue
        if isinstance(req, list):  # 兼容批量
            for one in req:
                handle(one)
        else:
            handle(req)


if __name__ == "__main__":
    main()
