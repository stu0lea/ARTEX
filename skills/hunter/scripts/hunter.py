#!/usr/bin/env python3
"""鹰图 Hunter API 查询 + insert_assets 映射（artex skill）。

用法：
  python3 hunter.py --query 'domain.suffix="example.com"' --size 10
  python3 hunter.py --query 'ip="1.2.3.4"' --size 20 --map-assets
"""

import argparse
import base64
import ipaddress
import json
import os
import sys
import urllib.parse
import urllib.request

API = "https://hunter.qianxin.com/openApi/search"
MAX_SIZE = 50


def build_url(args, key):
    search = base64.b64encode(args.query.encode("utf-8")).decode("ascii")
    params = {
        "api-key": key,
        "search": search,
        "page": str(max(1, args.page)),
        "page_size": str(min(max(1, args.size), MAX_SIZE)),
        "is_web": str(args.is_web),
    }
    if args.start_time:
        params["start_time"] = args.start_time
    if args.end_time:
        params["end_time"] = args.end_time
    if args.fields:
        params["fields"] = args.fields
    return API + "?" + urllib.parse.urlencode(params)


def fetch(url, no_proxy):
    if no_proxy:
        opener = urllib.request.build_opener(urllib.request.ProxyHandler({}))
    else:
        opener = urllib.request.build_opener()
    req = urllib.request.Request(url, headers={"Accept": "application/json"})
    try:
        with opener.open(req, timeout=30) as resp:
            return json.load(resp)
    except urllib.error.HTTPError as e:
        sys.exit("HTTP %s: %s" % (e.code, e.reason))
    except Exception as e:
        sys.exit("request failed: %s" % e)


def is_ip(value):
    try:
        ipaddress.ip_address(value)
        return True
    except ValueError:
        return False


def compact(item):
    keys = (
        "url", "ip", "port", "domain", "protocol", "is_web", "status_code",
        "web_title", "component", "company", "os", "updated_at",
    )
    return {k: item[k] for k in keys if item.get(k) not in (None, "", [])}


def to_assets(items, max_assets):
    merged = {}
    order = []

    def add(asset, ident):
        key = (asset["type"], ident)
        if key in merged:
            cur = merged[key]
            for k, v in asset.items():
                if k in ("bound_domains", "open_ports", "record_value") and isinstance(v, list):
                    bucket = cur.setdefault(k, [])
                    for x in v:
                        if x not in bucket:
                            bucket.append(x)
            return
        merged[key] = asset
        order.append(key)

    for it in items:
        ip = (it.get("ip") or "").strip()
        dom = (it.get("domain") or "").strip()
        port = it.get("port")
        proto = (it.get("protocol") or "").strip()
        is_web = it.get("is_web")
        url = (it.get("url") or "").strip()

        if is_web == 1 and url:
            a = {"type": "service", "url": url}
            try:
                if it.get("status_code"):
                    a["status_code"] = int(it["status_code"])
            except (TypeError, ValueError):
                pass
            if it.get("web_title"):
                a["page_title"] = it["web_title"]
            comp = it.get("component") or ""
            techs = [x.strip() for x in comp.replace("|", ",").split(",") if x.strip()]
            if techs:
                a["technologies"] = techs
            if ip:
                a["ip"] = ip
            add(a, url)
        elif proto and port:
            a = {"type": "service", "service_name": proto}
            try:
                a["port"] = int(port)
            except (TypeError, ValueError):
                pass
            if ip:
                a["ip"] = ip
            elif dom:
                a["domain"] = dom
            add(a, "%s:%s/%s" % (ip or dom, port, proto))

        if dom and is_ip(ip):
            add({"type": "subdomain", "domain": dom, "record_type": "A",
                 "record_value": [ip]}, dom)
        elif dom and not ip:
            add({"type": "subdomain", "domain": dom}, dom)

        if is_ip(ip):
            a = {"type": "ip", "ip": ip}
            if dom:
                a["bound_domains"] = [dom]
            if port and proto:
                a["open_ports"] = [{"port": int(port) if str(port).isdigit() else port,
                                    "service": proto}]
            add(a, ip)

        if len(order) >= max_assets:
            break
    return {"assets": [merged[k] for k in order]}


def main():
    ap = argparse.ArgumentParser(description="Hunter API query / asset mapping")
    ap.add_argument("--query", required=True, help="鹰图查询语句")
    ap.add_argument("--size", type=int, default=10)
    ap.add_argument("--page", type=int, default=1)
    ap.add_argument("--is-web", type=int, default=3, choices=[1, 2, 3])
    ap.add_argument("--start-time", default="")
    ap.add_argument("--end-time", default="")
    ap.add_argument("--fields", default="")
    ap.add_argument("--api-key", default="")
    ap.add_argument("--map-assets", action="store_true")
    ap.add_argument("--max-assets", type=int, default=50)
    ap.add_argument("--raw", action="store_true")
    ap.add_argument("--no-proxy", action="store_true")
    args = ap.parse_args()

    key = args.api_key or os.environ.get("HUNTER_API_KEY", "")
    if not key:
        sys.exit("HUNTER_API_KEY not set")

    resp = fetch(build_url(args, key), args.no_proxy)
    code = resp.get("code")
    if code != 200:
        sys.exit("hunter error code=%s message=%s" % (code, resp.get("message")))

    data = resp.get("data") or {}
    arr = data.get("arr") or []

    if args.raw:
        json.dump(resp, sys.stdout, ensure_ascii=False, indent=2)
        return

    if args.map_assets:
        out = to_assets(arr, args.max_assets)
        json.dump(out, sys.stdout, ensure_ascii=False, indent=2)
        sys.stderr.write("total=%s returned=%s consume_credit=%s assets=%d\n" % (
            data.get("total"), len(arr), data.get("consume_credit"), len(out["assets"])))
        return

    out = {
        "total": data.get("total"),
        "returned": len(arr),
        "consume_credit": data.get("consume_credit"),
        "results": [compact(x) for x in arr],
    }
    json.dump(out, sys.stdout, ensure_ascii=False, indent=2)


if __name__ == "__main__":
    main()
