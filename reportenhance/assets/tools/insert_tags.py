#!/usr/bin/env python3
"""一次性脚本：把 Jinja 标签插进客户 Word 模板，产出 report-template-auto.docx。

用法：python3 insert_tags.py <原始模板.docx> <输出.docx>
原始模板不改动；本脚本只需要跑一次，产物随代码提交。
"""
import sys

from docx import Document
from docx.enum.text import WD_ALIGN_PARAGRAPH


def set_text(p, text):
    """清空段落并写入单 run，保留段落样式。"""
    for r in list(p.runs):
        r._element.getparent().remove(r._element)
    p.add_run(text)


def insert_before(p, text):
    np = p.insert_paragraph_before(text)
    return np


def remove(p):
    p._element.getparent().remove(p._element)


def main():
    src, dst = sys.argv[1], sys.argv[2]
    doc = Document(src)
    ps = doc.paragraphs

    # 封面 / 日期
    set_text(ps[1], "{{ project_name }}应用系统")
    set_text(ps[13], "{{ date_cn }}")

    # 摘要
    set_text(ps[29], "本次渗透测试主要采用专家人工测试和部分工具作为辅助。在渗透测试中我们发现该系统存在漏洞高危{{ high }}个，中危{{ medium }}个，低危{{ low }}个，其中主要漏洞类别见漏洞总结章节。")
    set_text(ps[30], "结论：整体而言，该系统在本次渗透测试实施期间的安全风险状况为“{{ risk }}”。(系统安全风险状况等级的含义及说明详见附录A)。")

    # 测试时间
    set_text(ps[77], "{{ date_cn }}，进行渗透测试。")
    set_text(ps[78], "{{ date_cn }}，进行报告撰写。")

    # 漏洞总结
    set_text(ps[103], "经天融信安全服务团队测试，共发现{{ total }}处安全漏洞，其中高危{{ high }}个，中危{{ medium }}个，低危{{ low }}个。")

    # 漏洞详情：系统 / 漏洞循环（{%p %} 会把标签所在段落整段移除）
    h2 = ps[107].style
    h3 = ps[108].style
    set_text(ps[107], "{%p for sys in systems %}")
    sys_name = insert_before(ps[108], "{{ sys.name }}")
    sys_name.style = h2
    set_text(ps[108], "{%p for v in sys.vulns %}")
    vuln_title = insert_before(ps[109], "{{ v.title }}")
    vuln_title.style = h3
    set_text(ps[110], "{{ v.location }}")

    # 漏洞描述：正文段落 + 截图
    set_text(ps[112], "{%p for p in v.desc %}")
    insert_before(ps[113], "{{ p }}")
    insert_before(ps[113], "{%p endfor %}")
    insert_before(ps[113], "{%p for s in v.shots %}")
    img = insert_before(ps[113], "{{ s.img }}")
    img.alignment = WD_ALIGN_PARAGRAPH.CENTER
    cap = insert_before(ps[113], "{{ s.caption }}")
    cap.alignment = WD_ALIGN_PARAGRAPH.CENTER
    insert_before(ps[113], "{%p endfor %}")

    # 漏洞建议：用动态内容替换模板里的固定 SQL 建议
    set_text(ps[114], "{%p for p in v.fix %}")
    set_text(ps[115], "{{ p }}")
    set_text(ps[116], "{%p endfor %}")
    for p in ps[117:123]:
        remove(p)

    # 脆弱性评价
    set_text(ps[124], "{{ v.severity }}")

    # 模板里第二套「系统/漏洞」示例整体删掉，避免渲染后残留
    tail = ps[181]
    for p in ps[125:181]:
        remove(p)
    insert_before(tail, "{%p endfor %}")
    insert_before(tail, "{%p endfor %}")

    # 测试范围表：只保留一行
    scope_tbl = doc.tables[4]
    row = scope_tbl.rows[1]
    row.cells[0].text = "1"
    row.cells[1].text = "{{ scope[0].name }}"
    row.cells[2].text = "{{ scope[0].url }}"
    if len(scope_tbl.rows) > 2:
        scope_tbl._tbl.remove(scope_tbl.rows[2]._tr)

    # 漏洞汇总表：4 行按分组填充
    vuln_tbl = doc.tables[5]
    for i in range(1, 5):
        cells = vuln_tbl.rows[i].cells
        cond = f"groups|length > {i - 1}"
        cells[0].text = "{{ groups[%d].name if %s else \"\" }}" % (i - 1, cond)
        cells[1].text = "{{ groups[%d].count if %s else \"\" }}" % (i - 1, cond)
        cells[2].text = "{{ groups[%d].severity if %s else \"\" }}" % (i - 1, cond)
        cells[3].text = ""

    # 文档修订表日期
    rev = doc.tables[1]
    if len(rev.rows) > 1:
        rev.rows[1].cells[1].text = "{{ date_cn }}"

    doc.save(dst)
    print("wrote", dst)


if __name__ == "__main__":
    main()
