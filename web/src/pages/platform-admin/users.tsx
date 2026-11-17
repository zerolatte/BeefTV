import { useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { Button, Dropdown, Table } from "antd";
import { MoreHorizontal, Plus, RefreshCw } from "lucide-react";

import { ListToolbar, PaginationBar, TableSurface } from "@/components/layout/workspace-page";
import { WorkspaceErrorState, WorkspaceState } from "@/components/layout/workspace-state";
import { listPlatformAccounts } from "@/services/api/platform-admin";
import type { PlatformAccount } from "@/services/api/platform-auth";
import { useUserStore } from "@/stores/use-user-store";
import { PlatformAccountDialog, type AccountAction } from "./account-dialog";
import { PlatformModelGrants } from "./model-grants";
import { PlatformPointAdjustmentDialog } from "./point-adjustment-dialog";

const statusLabels = { active: "正常", disabled: "已禁用", deleted: "已注销" };

export function PlatformAdminUsers() {
    const actor = useUserStore((state) => state.user);
    const instanceId = useUserStore((state) => state.platformInstanceId);
    const [page, setPage] = useState(1);
    const [pageSize, setPageSize] = useState(20);
    const [action, setAction] = useState<AccountAction | null>(null);
    const [grants, setGrants] = useState<PlatformAccount>();
    const [points, setPoints] = useState<PlatformAccount>();
    const queryClient = useQueryClient();
    const query = useQuery({ queryKey: ["platform-admin", instanceId, actor?.id, "users", page, pageSize], queryFn: ({ signal }) => listPlatformAccounts(page, pageSize, signal) });
    const changed = () => {
        setAction(null);
        setGrants(undefined);
        setPoints(undefined);
        void queryClient.invalidateQueries({ queryKey: ["platform-admin"] });
        void queryClient.invalidateQueries({ queryKey: ["platform-model-catalog"] });
    };

    const actions = (account: PlatformAccount) => {
        if (account.status === "deleted") return [];
        return [
            { key: "edit", label: "编辑账号" },
            { key: "quota", label: "查看与修改额度" },
            { key: "points", label: "调整 AI 点数" },
            { key: "grants", label: "模型授权" },
            { key: "password", label: "重置密码" },
            ...(account.id !== actor?.id ? [
                { key: "status", label: account.status === "active" ? "禁用账号" : "启用账号", danger: account.status === "active" },
                { key: "delete", label: "注销账号", danger: true },
            ] : []),
        ];
    };

    return (
        <>
            <ListToolbar trailing={<Button onClick={() => void query.refetch()} loading={query.isFetching} icon={<RefreshCw className="size-4" />}>刷新</Button>}>
                <Button type="primary" icon={<Plus className="size-4" />} onClick={() => setAction({ kind: "create" })}>创建账号</Button>
            </ListToolbar>
            {query.isError ? <WorkspaceErrorState title="用户账号加载失败" description={query.error.message} onRetry={() => void query.refetch()} /> : (
                <TableSurface>
                    <Table<PlatformAccount>
                        rowKey="id"
                        loading={query.isLoading}
                        dataSource={query.data?.users || []}
                        pagination={false}
                        scroll={{ x: 680 }}
                        locale={{ emptyText: <WorkspaceState compact title="暂无用户账号" /> }}
                        columns={[
                            { title: "用户名", dataIndex: "username", key: "username", render: (name: string) => <span title={name}>{name}</span> },
                            { title: "显示名", dataIndex: "displayName", key: "displayName", ellipsis: true },
                            { title: "角色", key: "role", render: (_, row) => row.role === "admin" ? "管理员" : "普通用户" },
                            { title: "状态", key: "status", render: (_, row) => <span>{statusLabels[row.status]}{row.mustChangePassword && row.status !== "deleted" ? " · 待改密" : ""}</span> },
                            { title: "创建时间", key: "createdAt", render: (_, row) => row.createdAt ? new Date(row.createdAt).toLocaleString() : "—" },
                            { title: "操作", key: "actions", width: 72, render: (_, account) => account.status === "deleted" ? null : <Dropdown trigger={["click"]} menu={{ items: actions(account), onClick: ({ key }) => key === "grants" ? setGrants(account) : key === "points" ? setPoints(account) : setAction({ kind: key as "edit" | "quota" | "password" | "status" | "delete", account }) }}><Button type="text" aria-label={`管理 ${account.username}`} icon={<MoreHorizontal className="size-4" />} /></Dropdown> },
                        ]}
                    />
                </TableSurface>
            )}
            {query.data ? <PaginationBar alwaysShow current={page} pageSize={pageSize} total={query.data.total} itemLabel="个账号" onChange={(nextPage, nextSize) => { setPage(nextPage); setPageSize(nextSize); }} /> : null}
            {action ? <PlatformAccountDialog action={action} onClose={() => setAction(null)} onComplete={changed} /> : null}
            {grants ? <PlatformModelGrants account={grants} onClose={() => setGrants(undefined)} onComplete={changed} /> : null}
            {points ? <PlatformPointAdjustmentDialog account={points} onClose={() => setPoints(undefined)} onComplete={changed} /> : null}
        </>
    );
}
