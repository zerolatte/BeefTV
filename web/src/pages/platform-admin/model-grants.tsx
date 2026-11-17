import { useEffect, useRef, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { Button, Table } from "antd";

import { AppModal } from "@/components/ui/product/app-modal";
import { PaginationBar } from "@/components/layout/workspace-page";
import { WorkspaceErrorState, WorkspaceState } from "@/components/layout/workspace-state";
import type { PlatformAccount } from "@/services/api/platform-auth";
import { getPlatformModelGrants, listPlatformLogicalModels, replacePlatformModelGrants } from "@/services/api/platform-catalog";
import { useCatalogSubmit, usePlatformAdminKey } from "./catalog-form";

export function PlatformModelGrants({ account, onClose, onComplete }: { account: PlatformAccount; onClose: () => void; onComplete: () => void }) {
    const key = usePlatformAdminKey();
    const [page, setPage] = useState(1);
    const [pageSize, setPageSize] = useState(20);
    const [selected, setSelected] = useState<string[]>([]);
    const [ready, setReady] = useState(false);
    const loaded = useRef(false);
    const grants = useQuery({ queryKey: [...key, "grants", account.id], queryFn: ({ signal }) => getPlatformModelGrants(account.id, signal), refetchOnWindowFocus: false });
    const models = useQuery({ queryKey: [...key, "logical-models", page, pageSize], queryFn: ({ signal }) => listPlatformLogicalModels(page, pageSize, signal) });
    const mutation = useCatalogSubmit();
    useEffect(() => {
        if (loaded.current || !grants.data || !grants.isFetchedAfterMount) return;
        loaded.current = true;
        setSelected(grants.data.logicalModelIds);
        setReady(true);
    }, [grants.data, grants.isFetchedAfterMount]);
    const failure = grants.error || models.error;
    const save = () => mutation.submit(async () => {
        if (!loaded.current) throw new Error("授权读取尚未完成");
        await replacePlatformModelGrants(account.id, selected);
    }, onComplete);
    return <AppModal open scrollBody width={800} title={`模型授权：${account.displayName}`} onCancel={mutation.busy ? undefined : onClose} onOk={() => void save()} confirmLoading={mutation.busy} okButtonProps={{ disabled: !ready || Boolean(failure) }} closable={!mutation.busy} maskClosable={!mutation.busy} okText="保存授权" cancelText="取消">
        <div className="platform-admin-dialog">
            {mutation.error ? <p role="alert" className="platform-admin-error">{mutation.error}</p> : null}
            {failure ? <WorkspaceErrorState compact title="授权加载失败" description={failure.message} onRetry={() => { void models.refetch(); void grants.refetch(); }} /> : null}
            <div className="platform-admin-grant-summary"><span>已选择 {selected.length} 个模型</span><Button disabled={mutation.busy || !ready} onClick={() => setSelected([])}>清空授权</Button></div>
            <Table rowKey="id" loading={models.isLoading || !ready} pagination={false} dataSource={models.data?.models || []} rowSelection={{ selectedRowKeys: selected, preserveSelectedRowKeys: true, onChange: (keys) => setSelected(keys.map(String)), getCheckboxProps: () => ({ disabled: mutation.busy || !ready }) }} locale={{ emptyText: <WorkspaceState compact title="暂无可授权模型" /> }} columns={[
                { title: "模型", dataIndex: "name", key: "name" }, { title: "稳定标识", dataIndex: "code", key: "code" },
                { title: "状态", key: "enabled", render: (_, row) => row.enabled ? "启用" : "停用" },
            ]} />
            {models.data ? <PaginationBar alwaysShow current={page} pageSize={pageSize} total={models.data.total} itemLabel="个模型" onChange={(next, size) => { setPage(next); setPageSize(size); }} /> : null}
        </div>
    </AppModal>;
}
