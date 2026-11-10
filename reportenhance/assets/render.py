#!/usr/bin/env python3
"""把 ARTEX 的发现数据套进客户 Word 模板（docxtpl）。

输入 JSON 结构见 reportenhance/enhance.go 的 DocData。
截图失败整份报告继续生成：单张图异常只跳过该图。
"""
import argparse
import json
import os
import sys

from docxtpl import DocxTemplate, InlineImage
from docx.enum.text import WD_ALIGN_PARAGRAPH
from docx.shared import Mm, Pt, RGBColor
from docx.oxml.ns import qn

CODE_MARK = "\ue000"
CAPTION_MARK = "\ue001"
HEAD_MARK = "\ue002"
IMG_WIDTH = Mm(158)


def load(path):
    with open(path, "r", encoding="utf-8") as f:
        return json.load(f)


def attach_images(tpl, data):
    missing = 0
    for system in data.get("systems") or []:
        for vuln in system.get("vulns") or []:
            vuln["desc"] = vuln.get("desc") or []
            vuln["after"] = vuln.get("after") or []
            shots = []
            for shot in vuln.get("shots") or []:
                path = shot.get("path") or ""
                if not path or not os.path.isfile(path):
                    missing += 1
                    continue
                try:
                    shot["img"] = InlineImage(tpl, path, width=IMG_WIDTH)
                except Exception as exc:  # 图坏了就跳过，不阻断报告
                    print(f"skip image {path}: {exc}", file=sys.stderr)
                    missing += 1
                    continue
                shots.append(shot)
            vuln["shots"] = shots
    return missing


def strip_mark(paragraph, mark):
    texts = [r.text for r in paragraph.runs]
    if not any(mark in t for t in texts):
        return False
    for run in paragraph.runs:
        if mark in run.text:
            run.text = run.text.replace(mark, "", 1)
            return True
    return False


def restyle(doc):
    """把代码段与图注改成模板默认正文之外的样式，并补图号。"""
    fig = 0
    for paragraph in doc.paragraphs:
        text = paragraph.text or ""
        if CODE_MARK in text:
            strip_mark(paragraph, CODE_MARK)
            for run in paragraph.runs:
                run.font.name = "Consolas"
                run.font.size = Pt(8.5)
                run._element.rPr.rFonts.set(qn("w:eastAsia"), "等线")
            paragraph.paragraph_format.space_before = Pt(0)
            paragraph.paragraph_format.space_after = Pt(0)
            paragraph.paragraph_format.left_indent = Mm(6)
            continue
        if HEAD_MARK in text:
            strip_mark(paragraph, HEAD_MARK)
            for run in paragraph.runs:
                run.bold = True
                run.font.size = Pt(10.5)
            paragraph.paragraph_format.space_before = Pt(8)
            paragraph.paragraph_format.space_after = Pt(4)
            continue
        if CAPTION_MARK in text:
            strip_mark(paragraph, CAPTION_MARK)
            fig += 1
            if paragraph.runs:
                paragraph.runs[0].text = f"图 {fig}  " + paragraph.runs[0].text
            for run in paragraph.runs:
                run.font.size = Pt(9)
                run.font.color.rgb = RGBColor(0x59, 0x59, 0x59)
            paragraph.alignment = WD_ALIGN_PARAGRAPH.CENTER
            paragraph.paragraph_format.space_after = Pt(10)


def enable_update_fields(doc):
    settings = doc.settings.element
    tag = qn("w:updateFields")
    for old in settings.findall(tag):
        settings.remove(old)
    el = settings.makeelement(tag, {qn("w:val"): "true"})
    settings.append(el)


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--template", required=True)
    ap.add_argument("--data", required=True)
    ap.add_argument("--out", required=True)
    args = ap.parse_args()

    data = load(args.data)
    tpl = DocxTemplate(args.template)
    missing = attach_images(tpl, data)
    if missing:
        print(f"skipped {missing} missing/broken image(s)", file=sys.stderr)

    # autoescape 必须开：报告正文来自抓包/目标系统，可能带 < > &，直接拼进
    # OOXML 会破坏文档结构（实测 <br> 会把后续内容吞进正文元素里）。
    tpl.render(data, autoescape=True)
    doc = tpl.docx
    restyle(doc)
    enable_update_fields(doc)
    os.makedirs(os.path.dirname(os.path.abspath(args.out)), exist_ok=True)
    doc.save(args.out)
    print(f"wrote {args.out}")


if __name__ == "__main__":
    main()
