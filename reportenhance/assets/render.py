#!/usr/bin/env python3
"""把 ARTEX 的发现数据套进客户 Word 模板（docxtpl）。

输入 JSON 结构见 reportenhance/enhance.go 的 DocData。
截图失败整份报告继续生成：单张图异常只跳过该图。
"""
import argparse
import base64
import copy
import json
import os
import re
import sys

from docxtpl import DocxTemplate, InlineImage
from docx.enum.table import WD_TABLE_ALIGNMENT
from docx.enum.text import WD_ALIGN_PARAGRAPH
from docx.shared import Mm, Pt, RGBColor
from docx.oxml import OxmlElement
from docx.oxml.ns import qn
from docx.text.run import Run

CODE_MARK = "\ue000"
CAPTION_MARK = "\ue001"
HEAD_MARK = "\ue002"
TABLE_MARK = "\ue005"
INLINE_CODE = re.compile("\ue003(.*?)\ue004", re.S)
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


WRAP_SUCCESSORS = (
    "w:overflowPunct", "w:topLinePunct", "w:autoSpaceDE", "w:autoSpaceDN",
    "w:bidi", "w:adjustRightInd", "w:snapToGrid", "w:spacing", "w:ind",
    "w:contextualSpacing", "w:mirrorIndents", "w:suppressOverlap", "w:jc",
    "w:textDirection", "w:textAlignment", "w:textboxTightWrap",
    "w:outlineLvl", "w:divId", "w:cnfStyle", "w:rPr", "w:sectPr", "w:pPrChange",
)


def allow_word_wrap(paragraph):
    """允许西文在单词中间换行：长 URL / base64 / JSON 不会顶出右边距。"""
    pPr = paragraph._p.get_or_add_pPr()
    ww = pPr.find(qn("w:wordWrap"))
    if ww is None:
        ww = OxmlElement("w:wordWrap")
        # pPr 子元素顺序是 schema 规定的，必须插在 overflowPunct 之前
        pPr.insert_element_before(ww, *WRAP_SUCCESSORS)
    ww.set(qn("w:val"), "true")


def shade_code(paragraph):
    pPr = paragraph._p.get_or_add_pPr()
    shd = OxmlElement("w:shd")
    shd.set(qn("w:val"), "clear")
    shd.set(qn("w:color"), "auto")
    shd.set(qn("w:fill"), "F7F8FA")
    pPr.append(shd)
    bdr = OxmlElement("w:pBdr")
    left = OxmlElement("w:left")
    left.set(qn("w:val"), "single")
    left.set(qn("w:sz"), "8")
    left.set(qn("w:space"), "6")
    left.set(qn("w:color"), "D0D7DE")
    bdr.append(left)
    pPr.append(bdr)


def style_inline_code(paragraph):
    """把 `行内代码` 拆成独立 run，换成等宽字体，不破坏整段排版。"""
    for run in list(paragraph.runs):
        text = run.text
        if "\ue003" not in text:
            continue
        parts = INLINE_CODE.split(text)
        run.text = parts[0]
        anchor = run._r
        for idx in range(1, len(parts)):
            chunk = parts[idx]
            new_el = copy.deepcopy(run._r)
            anchor.addnext(new_el)
            nr = Run(new_el, run._parent)
            is_code = idx % 2 == 1
            nr.text = chunk
            if is_code:
                nr.font.name = "Consolas"
                nr.font.size = Pt(9)
                try:
                    nr._element.rPr.rFonts.set(qn("w:eastAsia"), "等线")
                    shd = OxmlElement("w:shd")
                    shd.set(qn("w:val"), "clear")
                    shd.set(qn("w:color"), "auto")
                    shd.set(qn("w:fill"), "F2F2F2")
                    nr._element.rPr.append(shd)
                except Exception:
                    pass
            anchor = new_el


def restyle(doc):
    """把代码段与图注改成模板默认正文之外的样式，并补图号。"""
    fig = 0
    for paragraph in doc.paragraphs:
        style_inline_code(paragraph)
        allow_word_wrap(paragraph)
        text = paragraph.text or ""
        if CODE_MARK in text:
            strip_mark(paragraph, CODE_MARK)
            for run in paragraph.runs:
                run.font.name = "Consolas"
                run.font.size = Pt(8.5)
                run._element.rPr.rFonts.set(qn("w:eastAsia"), "等线")
            paragraph.alignment = WD_ALIGN_PARAGRAPH.LEFT
            paragraph.paragraph_format.space_before = Pt(0)
            paragraph.paragraph_format.space_after = Pt(0)
            paragraph.paragraph_format.left_indent = Mm(5)
            paragraph.paragraph_format.right_indent = Mm(3)
            paragraph.paragraph_format.first_line_indent = Pt(0)
            shade_code(paragraph)
            continue
        if HEAD_MARK in text:
            strip_mark(paragraph, HEAD_MARK)
            for run in paragraph.runs:
                run.bold = True
                run.font.size = Pt(12)
            paragraph.paragraph_format.space_before = Pt(8)
            paragraph.paragraph_format.space_after = Pt(4)
            continue
        if CAPTION_MARK in text:
            strip_mark(paragraph, CAPTION_MARK)
            fig += 1
            if paragraph.runs:
                paragraph.runs[0].text = f"图 {fig}  " + paragraph.runs[0].text
            for run in paragraph.runs:
                run.font.size = Pt(12)
                run.font.color.rgb = RGBColor(0x59, 0x59, 0x59)
            paragraph.alignment = WD_ALIGN_PARAGRAPH.CENTER
            paragraph.paragraph_format.space_after = Pt(10)
            continue
        # 正文统一小四（12pt）；模板里已显式设过字号的不动
        for run in paragraph.runs:
            if run.font.size is None:
                run.font.size = Pt(12)


def _cell_shade(cell, fill):
    tcPr = cell._tc.get_or_add_tcPr()
    shd = OxmlElement("w:shd")
    shd.set(qn("w:val"), "clear")
    shd.set(qn("w:color"), "auto")
    shd.set(qn("w:fill"), fill)
    tcPr.append(shd)


def _table_borders(table):
    tblPr = table._tbl.tblPr
    borders = OxmlElement("w:tblBorders")
    for edge in ("top", "left", "bottom", "right", "insideH", "insideV"):
        el = OxmlElement("w:" + edge)
        el.set(qn("w:val"), "single")
        el.set(qn("w:sz"), "4")
        el.set(qn("w:space"), "0")
        el.set(qn("w:color"), "BFBFBF")
        borders.append(el)
    tblPr.append(borders)


def build_tables(doc):
    """把占位段落替换成带表头样式的 Word 表格。"""
    for paragraph in list(doc.paragraphs):
        text = (paragraph.text or "").strip()
        if not text.startswith(TABLE_MARK):
            continue
        try:
            rows = json.loads(base64.b64decode(text[len(TABLE_MARK):]))
        except Exception:
            continue
        if not rows:
            paragraph._p.getparent().remove(paragraph._p)
            continue
        cols = max(len(r) for r in rows)
        table = doc.add_table(rows=len(rows), cols=cols)
        table.alignment = WD_TABLE_ALIGNMENT.CENTER
        table.autofit = True
        _table_borders(table)
        for ri, row in enumerate(rows):
            for ci in range(cols):
                cell = table.cell(ri, ci)
                value = row[ci] if ci < len(row) else ""
                value = value.replace("\ue003", "").replace("\ue004", "")
                allow_word_wrap(cell.paragraphs[0])
                run = cell.paragraphs[0].add_run(value)
                run.font.size = Pt(12)
                if ri == 0:
                    run.bold = True
                    _cell_shade(cell, "F2F2F2")
        # 表头跨页重复
        trPr = table.rows[0]._tr.get_or_add_trPr()
        th = OxmlElement("w:tblHeader")
        th.set(qn("w:val"), "true")
        trPr.append(th)
        paragraph._p.addnext(table._tbl)
        paragraph._p.getparent().remove(paragraph._p)


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
    build_tables(doc)
    enable_update_fields(doc)
    os.makedirs(os.path.dirname(os.path.abspath(args.out)), exist_ok=True)
    doc.save(args.out)
    print(f"wrote {args.out}")


if __name__ == "__main__":
    main()
