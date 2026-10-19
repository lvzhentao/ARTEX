"use client";

import * as React from "react";

import { Loader2Icon, NetworkIcon, PlusIcon, Trash2Icon } from "lucide-react";
import { toast } from "sonner";

import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Switch } from "@/components/ui/switch";
import { Textarea } from "@/components/ui/textarea";
import { api } from "@/lib/api";
import type { ProxyPoolEntryInput, ProxyPoolState, ProxyPoolTestResult } from "@/lib/types";

// 编辑态的一行(与池内条目一一对应,保存时全量提交)。
interface Row {
  url: string;
  enabled: boolean;
}

// Egress Proxy Pool:所有出口(LLM 请求、MITM 上游、WebSearch、agent env 注入)从池内
// round-robin 轮询;失败自动冷却跳过。此卡片管理条目与开关,支持连通性测试。
export function ProxyPoolCard({ onChanged }: { onChanged?: () => void }) {
  const [enabled, setEnabled] = React.useState(false);
  const [rows, setRows] = React.useState<Row[]>([]);
  const [coolingUrls, setCoolingUrls] = React.useState<Set<string>>(new Set());
  const [results, setResults] = React.useState<Record<string, ProxyPoolTestResult>>({});
  const [importText, setImportText] = React.useState("");
  const [importScheme, setImportScheme] = React.useState("socks5");
  const [loaded, setLoaded] = React.useState(false);
  const [saving, setSaving] = React.useState(false);
  const [testingAll, setTestingAll] = React.useState(false);
  const [testingUrls, setTestingUrls] = React.useState<Set<string>>(new Set());

  const applyState = React.useCallback((s: ProxyPoolState) => {
    setEnabled(s.enabled);
    setRows(s.entries.map((e) => ({ url: e.url, enabled: e.enabled })));
    setCoolingUrls(new Set(s.entries.filter((e) => e.cooling).map((e) => e.url)));
  }, []);

  const refresh = React.useCallback(() => {
    api
      .proxyPool()
      .then(applyState)
      .catch(() => undefined)
      .finally(() => setLoaded(true));
  }, [applyState]);

  React.useEffect(() => {
    refresh();
  }, [refresh]);

  const toggleEnabled = (v: boolean) => {
    setEnabled(v); // optimistic
    api
      .putProxyPool({ enabled: v })
      .then(applyState)
      .then(() => {
        toast.success(v ? "Proxy pool enabled — all egress now rotates through the pool" : "Proxy pool disabled");
        onChanged?.();
      })
      .catch((e) => {
        setEnabled(!v);
        toast.error(`Save failed: ${(e as Error).message}`);
      });
  };

  const importLines = () => {
    const existing = new Set(rows.map((r) => r.url));
    let added = 0;
    const next = [...rows];
    for (const line of importText.split(/\r?\n/)) {
      const t = line.trim();
      if (!t) continue;
      if (existing.has(t)) continue;
      existing.add(t);
      next.push({ url: t, enabled: true });
      added++;
    }
    if (added === 0) {
      toast.info("No new lines to import (empty or duplicate)");
      return;
    }
    setRows(next);
    setImportText("");
    toast.success(`Imported ${added} line(s) — click Save to apply`);
  };

  const save = () => {
    setSaving(true);
    const entries: ProxyPoolEntryInput[] = rows
      .filter((r) => r.url.trim() !== "")
      .map((r) => ({ url: r.url.trim(), enabled: r.enabled }));
    api
      .putProxyPool({ entries, default_scheme: importScheme })
      .then(applyState)
      .then(() => {
        toast.success(`Saved ${entries.length} entr${entries.length === 1 ? "y" : "ies"}`);
        onChanged?.();
      })
      .catch((e) => toast.error(`Save failed: ${(e as Error).message}`))
      .finally(() => setSaving(false));
  };

  const runTests = async (urls: string[]) => {
    const targets = urls.map((u) => u.trim()).filter(Boolean);
    if (targets.length === 0) {
      toast.info("Nothing to test — the pool is empty");
      return;
    }
    const prev = { ...results };
    for (const u of targets) delete prev[u];
    setResults(prev);
    if (urls.length > 1) setTestingAll(true);
    setTestingUrls(new Set(targets));
    try {
      const r = await api.testProxyPool(targets);
      const map: Record<string, ProxyPoolTestResult> = {};
      for (const res of r.results) map[res.url] = res;
      setResults((cur) => ({ ...cur, ...map }));
      const ok = r.results.filter((x) => x.ok).length;
      const level = ok === r.results.length ? "success" : ok === 0 ? "error" : "info";
      toast[level](`${ok}/${r.results.length} reachable`);
      refresh(); // 失败条目进入冷却,刷新徽标
    } catch (e) {
      toast.error(`Test failed: ${(e as Error).message}`);
    } finally {
      setTestingAll(false);
      setTestingUrls(new Set());
    }
  };

  const updateRow = (i: number, patch: Partial<Row>) => {
    setRows((cur) => cur.map((r, idx) => (idx === i ? { ...r, ...patch } : r)));
  };

  const removeRow = (i: number) => {
    setRows((cur) => cur.filter((_, idx) => idx !== i));
  };

  const enabledCount = rows.filter((r) => r.enabled && r.url.trim() !== "").length;

  return (
    <Card className="mb-4 break-inside-avoid md:mb-6">
      <CardHeader>
        <CardTitle className="flex items-center gap-2 text-base">
          <NetworkIcon className="size-4" />
          Egress Proxy Pool
        </CardTitle>
        <CardDescription>
          When enabled, <b>every outbound request</b> (LLM model calls, captured agent traffic via the MITM upstream,
          WebSearch, browser egress) rotates round-robin through the entries below, taking precedence over the single
          global proxy. Failed entries are cooled down automatically (skipped until they recover). For per-LLM-profile
          overrides, leave the profile proxy empty (pool) or set an explicit URL (fixed egress).
          <br />
          Formats per line: <b>host:port:user:pass</b>, <b>host:port</b>, or a full <b>scheme://user:pass@host:port</b>.
        </CardDescription>
      </CardHeader>
      <CardContent className="flex flex-col gap-4">
        <div className="flex items-center justify-between gap-4">
          <Label htmlFor="proxy-pool-enable" className="text-sm font-normal text-muted-foreground">
            {enabled
              ? `Enabled · ${enabledCount}/${rows.length} entr${rows.length === 1 ? "y" : "ies"} in rotation`
              : "Disabled · falls back to the single global proxy"}
          </Label>
          <Switch id="proxy-pool-enable" checked={enabled} disabled={!loaded} onCheckedChange={toggleEnabled} />
        </div>

        <div className="flex flex-col gap-2">
          <Label htmlFor="proxy-pool-import" className="text-sm font-normal text-muted-foreground">
            Bulk import
          </Label>
          <Textarea
            id="proxy-pool-import"
            rows={4}
            spellCheck={false}
            className="font-mono text-xs"
            placeholder={"38.248.238.174:443:user:pass\n198.65.6.179:443:user:pass\nhttp://user:pass@15.204.80.28:2260"}
            value={importText}
            disabled={!loaded}
            onChange={(e) => setImportText(e.target.value)}
          />
          <div className="flex items-center gap-2">
            <Select value={importScheme} onValueChange={setImportScheme} disabled={!loaded}>
              <SelectTrigger className="w-44 shrink-0">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="socks5">Default scheme: socks5</SelectItem>
                <SelectItem value="http">Default scheme: http</SelectItem>
              </SelectContent>
            </Select>
            <Button
              type="button"
              variant="outline"
              onClick={importLines}
              disabled={!loaded || importText.trim() === ""}
            >
              Import
            </Button>
            <span className="text-muted-foreground text-xs">
              Lines without a scheme get the selected scheme on save.
            </span>
          </div>
        </div>

        <div className="flex flex-col gap-2">
          {rows.length === 0 && (
            <p className="text-muted-foreground text-xs">No entries yet — import or add lines below, then Save.</p>
          )}
          {rows.map((row, i) => {
            const res = results[row.url.trim()];
            const cooling = coolingUrls.has(row.url);
            return (
              <div key={`${row.url}-${i}`} className="flex flex-wrap items-center gap-2">
                <Switch
                  checked={row.enabled}
                  disabled={!loaded}
                  onCheckedChange={(v) => updateRow(i, { enabled: v })}
                  aria-label="Toggle entry"
                />
                <Input
                  className="min-w-64 flex-1 font-mono text-xs"
                  spellCheck={false}
                  value={row.url}
                  disabled={!loaded}
                  onChange={(e) => updateRow(i, { url: e.target.value })}
                />
                {testingUrls.has(row.url.trim()) ? (
                  <Loader2Icon className="text-muted-foreground size-4 shrink-0 animate-spin" />
                ) : res ? (
                  res.ok ? (
                    <Badge variant="outline" className="shrink-0 font-mono text-xs text-emerald-500">
                      {res.egress_ip} · {res.latency_ms}ms
                    </Badge>
                  ) : (
                    <Badge variant="destructive" className="max-w-56 shrink-0 truncate text-xs" title={res.error}>
                      {res.error || "failed"}
                    </Badge>
                  )
                ) : cooling ? (
                  <Badge variant="outline" className="shrink-0 text-xs text-amber-500">
                    cooling
                  </Badge>
                ) : null}
                <Button
                  type="button"
                  variant="ghost"
                  size="sm"
                  className="shrink-0"
                  disabled={!loaded || testingUrls.size > 0 || row.url.trim() === ""}
                  onClick={() => runTests([row.url])}
                >
                  Test
                </Button>
                <Button
                  type="button"
                  variant="ghost"
                  size="sm"
                  className="shrink-0"
                  disabled={!loaded}
                  onClick={() => removeRow(i)}
                  aria-label="Remove entry"
                >
                  <Trash2Icon className="size-4" />
                </Button>
              </div>
            );
          })}
        </div>

        <div className="flex flex-wrap items-center gap-2 border-t pt-4">
          <Button
            type="button"
            variant="outline"
            onClick={() => setRows((cur) => [...cur, { url: "", enabled: true }])}
            disabled={!loaded}
          >
            <PlusIcon className="size-4" /> Add
          </Button>
          <Button
            type="button"
            variant="outline"
            onClick={() => runTests(rows.map((r) => r.url))}
            disabled={!loaded || testingAll || rows.length === 0}
          >
            {testingAll ? <Loader2Icon className="size-4 animate-spin" /> : null}
            {testingAll ? "Testing…" : "Test all"}
          </Button>
          <div className="flex-1" />
          <Button type="button" onClick={save} disabled={!loaded || saving}>
            {saving ? "Saving…" : "Save pool"}
          </Button>
        </div>
        <p className="text-muted-foreground text-xs">
          Test dials https://api.ipify.org through each entry and shows the egress IP; failures are cooled down for 5
          minutes. Live traffic failures cool an entry for 60s and rotate to the next one.
        </p>
      </CardContent>
    </Card>
  );
}
