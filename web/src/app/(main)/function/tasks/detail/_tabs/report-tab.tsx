"use client";

import * as React from "react";

import { CheckIcon, CopyIcon, FileDownIcon, FileTextIcon, Loader2Icon, RefreshCwIcon } from "lucide-react";
import { toast } from "sonner";

import { Markdown } from "@/components/markdown";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { api } from "@/lib/api";
import { copyText } from "@/lib/utils";

export function ReportTab({ taskId }: { taskId: string }) {
  const [report, setReport] = React.useState<string>("");
  const [loading, setLoading] = React.useState(true);
  const [copied, setCopied] = React.useState(false);
  const [docxState, setDocxState] = React.useState<"checking" | "idle" | "running" | "done" | "failed">("checking");
  const [docxError, setDocxError] = React.useState("");

  React.useEffect(() => {
    let active = true;
    setLoading(true);
    api
      .report(taskId)
      .then((text) => {
        if (active) setReport(text);
      })
      .catch(() => {
        if (active) setReport("");
      })
      .finally(() => {
        if (active) setLoading(false);
      });
    return () => {
      active = false;
    };
  }, [taskId]);

  // 进入页面先恢复状态：内存中的任务或磁盘上已生成的报告都直接显示「下载 Word」，
  // 避免每次重新访问都只能再生成一遍。
  React.useEffect(() => {
    let active = true;
    setDocxState("checking");
    api
      .reportDocxStatus(taskId)
      .then((st) => {
        if (!active) return;
        const next = st.state === "done" ? "done" : st.state === "running" ? "running" : st.state === "failed" ? "failed" : "idle";
        setDocxState((cur) => (cur === "checking" ? next : cur));
        if (st.state === "failed") setDocxError(st.error ?? "");
      })
      .catch(() => {
        if (active) setDocxState((cur) => (cur === "checking" ? "idle" : cur));
      });
    return () => {
      active = false;
    };
  }, [taskId]);

  // Word 生成是异步任务：每 3 秒查一次状态，完成后按钮变成「下载」。
  React.useEffect(() => {
    if (docxState !== "running") return;
    const timer = setInterval(async () => {
      try {
        const st = await api.reportDocxStatus(taskId);
        if (st.state === "running") return;
        setDocxState(st.state === "done" ? "done" : "failed");
        setDocxError(st.error ?? "");
        if (st.state === "done") toast.success("Word 报告已生成");
      } catch {
        /* 网络抖动继续轮询 */
      }
    }, 3000);
    return () => clearInterval(timer);
  }, [docxState, taskId]);

  async function startDocx() {
    setDocxError("");
    setDocxState("running");
    try {
      const job = await api.reportDocxStart(taskId);
      if (job.state !== "running") setDocxState(job.state === "done" ? "done" : "failed");
    } catch (e) {
      setDocxState("failed");
      setDocxError(e instanceof Error ? e.message : String(e));
    }
  }

  async function generateDocx() {
    if (docxState === "done") {
      await api.downloadReportDocx(taskId);
      return;
    }
    await startDocx();
  }

  async function copy() {
    if (!report) return;
    const ok = await copyText(report);
    if (ok) {
      setCopied(true);
      toast.success("已复制 Markdown");
      setTimeout(() => setCopied(false), 1500);
    } else {
      toast.error("复制失败，请手动选择文本复制");
    }
  }

  let content: React.ReactNode;
  if (loading) {
    content = (
      <div className="flex flex-col items-center justify-center gap-2 rounded-md border border-dashed py-16 text-muted-foreground text-sm">
        <FileTextIcon className="size-8 opacity-40" />
        加载中…
      </div>
    );
  } else if (report) {
    content = (
      <div className="max-h-[60vh] overflow-auto rounded-md border bg-muted/20 p-4">
        <Markdown text={report} />
      </div>
    );
  } else {
    content = (
      <div className="flex flex-col items-center justify-center gap-2 rounded-md border border-dashed py-16 text-muted-foreground text-sm">
        <FileTextIcon className="size-8 opacity-40" />
        暂无报告
      </div>
    );
  }

  return (
    <Card>
      <CardHeader className="flex-row items-center justify-between">
        <CardTitle className="flex items-center gap-2 text-sm">
          <FileTextIcon className="size-4" /> 渗透测试报告（Markdown）
        </CardTitle>
        <div className="flex gap-2">
          <Button size="sm" variant={docxState === "done" ? "default" : "outline"} onClick={generateDocx} disabled={docxState === "running" || docxState === "checking"}>
            {docxState === "running" ? <Loader2Icon className="animate-spin" /> : <FileDownIcon />}
            {docxState === "running"
              ? "生成中…"
              : docxState === "checking"
                ? "检查中…"
                : docxState === "done"
                  ? "下载 Word"
                  : "生成 Word"}
          </Button>
          {docxState === "done" && (
            <Button size="sm" variant="outline" onClick={startDocx}>
              <RefreshCwIcon /> 重新生成
            </Button>
          )}
          {report && (
            <Button size="sm" variant="outline" onClick={copy}>
              {copied ? <CheckIcon /> : <CopyIcon />} 复制
            </Button>
          )}
        </div>
      </CardHeader>
      <CardContent>
        {docxError && <div className="mb-3 rounded-md border border-destructive/40 bg-destructive/10 px-3 py-2 text-destructive text-xs">{docxError}</div>}
        {content}
      </CardContent>
    </Card>
  );
}
