import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { Button, Input, Table } from "antd";
import { RefreshCw } from "lucide-react";

import { ListToolbar, PaginationBar, TableSurface } from "@/components/layout/workspace-page";
import { WorkspaceErrorState, WorkspaceState } from "@/components/layout/workspace-state";
import { listPlatformAuditLogs, type PlatformAudit } from "@/services/api/platform-admin";
import { useUserStore } from "@/stores/use-user-store";

const actionLabels: Record<string, string> = { account_created: "创建账号", account_updated: "修改账号", password_reset: "重置密码", storage_limit_updated: "修改存储上限", points_adjusted: "调整 AI 点数", execution_reviewed: "人工核对执行", channel_created: "创建渠道", channel_updated: "修改渠道", channel_credential_rotated: "轮换渠道凭据", channel_model_saved: "保存渠道模型", logical_model_published: "发布模型版本", model_price_published: "发布价格", model_grants_replaced: "替换模型授权" };
const fieldLabels: Record<string, string> = { displayName: "显示名", role: "角色", status: "状态", storageLimitBytes: "存储上限（字节）", reason: "原因", adjustmentId: "调整记录 ID", id: "记录 ID", intentId: "执行意图 ID", actorAccountId: "操作者", previousState: "核对前状态", fact: "终局事实", createdAt: "记录时间", channelId: "渠道 ID", name: "名称", enabled: "启用", version: "版本", points: "点数", operation: "操作", logicalModelId: "模型 ID", logicalModelIds: "授权模型", credentialVersion: "凭据版本", credentialVersionId: "凭据版本 ID", channelModelId: "渠道模型 ID", revisionId: "模型版本 ID", priceVersionId: "价格版本 ID" };
const valueLabels: Record<string, string> = { active: "正常", disabled: "已禁用", deleted: "已注销", user: "普通用户", admin: "管理员", reserved: "已预留", submitting: "提交中", accepted: "上游已受理", unknown: "结果待确认", succeeded: "至少一份成功结果", not_accepted: "确认未受理", failed_no_cost: "完全失败且未消耗", failed_consumed: "完全失败但上游已消耗" };

function differenceSummary(value: string) {
    try {
        const difference = JSON.parse(value) as Record<string, unknown>;
        return Object.entries(difference).map(([field, change]) => {
            const label = (entry: unknown) => typeof entry === "number" ? entry.toLocaleString() : Array.isArray(entry) ? entry.join("、") || "无" : typeof entry === "boolean" ? entry ? "是" : "否" : valueLabels[String(entry)] || String(entry ?? "—");
            if (change && typeof change === "object" && ("from" in change || "to" in change)) return `${fieldLabels[field] || field}：${label("from" in change ? change.from : undefined)} → ${label("to" in change ? change.to : undefined)}`;
            return `${fieldLabels[field] || field}：${label(change)}`;
        }).join("；") || "—";
    } catch { return "变更摘要不可用"; }
}

export function PlatformAdminAudit() {
    const actor = useUserStore((state) => state.user);
    const instanceId = useUserStore((state) => state.platformInstanceId);
    const [page, setPage] = useState(1);
    const [pageSize, setPageSize] = useState(20);
    const [accountId, setAccountId] = useState("");
    const [input, setInput] = useState("");
    const query = useQuery({ queryKey: ["platform-admin", instanceId, actor?.id, "audit", page, pageSize, accountId], queryFn: ({ signal }) => listPlatformAuditLogs(page, pageSize, accountId, signal) });
    return (
        <>
            <ListToolbar trailing={<Button onClick={() => void query.refetch()} loading={query.isFetching} icon={<RefreshCw className="size-4" />}>刷新</Button>}>
                <Input.Search aria-label="筛选对象账号 ID" placeholder="按对象账号 ID 筛选" allowClear value={input} onChange={(event) => setInput(event.target.value)} onSearch={(value) => { setPage(1); setAccountId(value.trim()); }} className="max-w-sm" />
            </ListToolbar>
            {query.isError ? <WorkspaceErrorState title="审计记录加载失败" description={query.error.message} onRetry={() => void query.refetch()} /> : (
                <TableSurface>
                    <Table<PlatformAudit> rowKey="id" loading={query.isLoading} dataSource={query.data?.logs || []} pagination={false} scroll={{ x: 840 }} locale={{ emptyText: <WorkspaceState compact title="暂无管理审计记录" /> }} columns={[
                        { title: "时间", dataIndex: "createdAt", key: "createdAt", width: 180, render: (value: string) => new Date(value).toLocaleString() },
                        { title: "操作", dataIndex: "action", key: "action", width: 120, render: (value: string) => actionLabels[value] || value },
                        { title: "操作者账号", dataIndex: "actorAccountId", key: "actorAccountId", ellipsis: true },
                        { title: "对象账号", dataIndex: "targetAccountId", key: "targetAccountId", ellipsis: true },
                        { title: "变更摘要", dataIndex: "difference", key: "difference", render: (value: string) => differenceSummary(value) },
                    ]} />
                </TableSurface>
            )}
            {query.data ? <PaginationBar alwaysShow current={page} pageSize={pageSize} total={query.data.total} onChange={(nextPage, nextSize) => { setPage(nextPage); setPageSize(nextSize); }} /> : null}
        </>
    );
}
