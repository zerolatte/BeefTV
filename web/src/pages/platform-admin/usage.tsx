import { useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { Button, Dropdown, Input, Table } from "antd";
import { MoreHorizontal, RefreshCw } from "lucide-react";

import { ListToolbar, PaginationBar, TableSurface } from "@/components/layout/workspace-page";
import { WorkspaceErrorState, WorkspaceState } from "@/components/layout/workspace-state";
import { listPlatformUsage, type PlatformUsage } from "@/services/api/platform-admin";
import { usePlatformAdminKey } from "./catalog-form";
import { PlatformExecutionReviewDialog } from "./execution-review-dialog";

const stateLabels: Record<PlatformUsage["state"], string> = {
    reserved: "已预留", submitting: "提交中", accepted: "上游已受理", unknown: "结果待确认",
    settled: "已结算", released: "已释放", compensated: "已补偿",
};

export function PlatformAdminUsage() {
    const key = usePlatformAdminKey();
    const [page, setPage] = useState(1);
    const [pageSize, setPageSize] = useState(20);
    const [input, setInput] = useState("");
    const [accountId, setAccountId] = useState("");
    const [review, setReview] = useState<PlatformUsage>();
    const queryClient = useQueryClient();
    const query = useQuery({ queryKey: [...key, "usage", page, pageSize, accountId], queryFn: ({ signal }) => listPlatformUsage(page, pageSize, accountId, signal) });
    return <>
        <ListToolbar trailing={<Button onClick={() => void query.refetch()} loading={query.isFetching} icon={<RefreshCw className="size-4" />}>刷新</Button>}>
            <Input.Search aria-label="筛选用量账号 ID" placeholder="按账号 ID 筛选" allowClear value={input} onChange={(event) => setInput(event.target.value)} onSearch={(value) => { setPage(1); setAccountId(value.trim()); }} className="max-w-sm" />
        </ListToolbar>
        {query.isError ? <WorkspaceErrorState title="用量记录加载失败" description={query.error.message} onRetry={() => void query.refetch()} /> : <TableSurface>
            <Table<PlatformUsage> rowKey="id" loading={query.isLoading} pagination={false} dataSource={query.data?.items || []} scroll={{ x: 1120 }} locale={{ emptyText: <WorkspaceState compact title="暂无用量记录" /> }} columns={[
                { title: "创建时间", dataIndex: "createdAt", key: "createdAt", width: 180, render: (value: string) => new Date(value).toLocaleString() },
                { title: "账号", dataIndex: "accountId", key: "accountId", ellipsis: true },
                { title: "任务", dataIndex: "taskId", key: "taskId", ellipsis: true },
                { title: "模型", dataIndex: "modelId", key: "modelId", ellipsis: true },
                { title: "模型操作", dataIndex: "operation", key: "operation" },
                { title: "点数", dataIndex: "points", key: "points", width: 90, render: (value: number) => value.toLocaleString() },
                { title: "执行状态", key: "state", width: 120, render: (_, row) => stateLabels[row.state] || row.state },
                { title: "人工核对", key: "reviewRequired", width: 120, render: (_, row) => row.reviewRequired ? "待核对" : "—" },
                { title: "操作", key: "actions", width: 72, render: (_, row) => row.reviewRequired ? <Dropdown trigger={["click"]} menu={{ items: [{ key: "review", label: "人工核对" }], onClick: () => setReview(row) }}><Button type="text" aria-label={`核对任务 ${row.taskId}`} icon={<MoreHorizontal className="size-4" />} /></Dropdown> : null },
            ]} />
        </TableSurface>}
        {query.data ? <PaginationBar alwaysShow current={page} pageSize={pageSize} total={query.data.total} itemLabel="条记录" onChange={(next, size) => { setPage(next); setPageSize(size); }} /> : null}
        {review ? <PlatformExecutionReviewDialog usage={review} onClose={() => setReview(undefined)} onComplete={() => { setReview(undefined); void queryClient.invalidateQueries({ queryKey: ["platform-admin"] }); }} /> : null}
    </>;
}
