import { useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { Button, Dropdown, Table } from "antd";
import { MoreHorizontal, Plus, RefreshCw } from "lucide-react";

import { ListToolbar, PaginationBar, TableSurface } from "@/components/layout/workspace-page";
import { WorkspaceErrorState, WorkspaceState } from "@/components/layout/workspace-state";
import { listPlatformChannels, listPlatformLogicalModels, type PlatformChannel, type PlatformLogicalModel } from "@/services/api/platform-catalog";
import { PlatformChannelDialog, type ChannelAction } from "./channel-dialog";
import { PlatformChannelModels } from "./channel-models";
import { PlatformModelDialog } from "./model-dialog";
import { PlatformModelPrices } from "./model-prices";
import { capabilityNames, usePlatformAdminKey } from "./catalog-form";

function ChannelsPanel() {
    const key = usePlatformAdminKey();
    const [page, setPage] = useState(1);
    const [pageSize, setPageSize] = useState(20);
    const [action, setAction] = useState<ChannelAction>();
    const [models, setModels] = useState<PlatformChannel>();
    const queryClient = useQueryClient();
    const query = useQuery({ queryKey: [...key, "channels", page, pageSize], queryFn: ({ signal }) => listPlatformChannels(page, pageSize, signal) });
    const changed = () => { setAction(undefined); void queryClient.invalidateQueries({ queryKey: ["platform-admin"] }); };
    return <>
        <ListToolbar trailing={<Button onClick={() => void query.refetch()} loading={query.isFetching} icon={<RefreshCw className="size-4" />}>刷新</Button>}><Button type="primary" icon={<Plus className="size-4" />} onClick={() => setAction({ kind: "create" })}>创建渠道</Button></ListToolbar>
        {query.isError ? <WorkspaceErrorState title="渠道加载失败" description={query.error.message} onRetry={() => void query.refetch()} /> : <TableSurface>
            <Table<PlatformChannel> rowKey="id" loading={query.isLoading} pagination={false} dataSource={query.data?.channels || []} scroll={{ x: 760 }} locale={{ emptyText: <WorkspaceState compact title="暂无平台渠道" /> }} columns={[
                { title: "渠道", dataIndex: "name", key: "name" },
                { title: "上游主机", dataIndex: "upstreamHost", key: "host", ellipsis: true },
                { title: "凭据版本", dataIndex: "credentialVersion", key: "version" },
                { title: "状态", key: "status", render: (_, row) => row.enabled ? "启用" : "停用" },
                { title: "操作", key: "actions", width: 72, render: (_, channel) => <Dropdown trigger={["click"]} menu={{ items: [{ key: "edit", label: "编辑与启停" }, { key: "credential", label: "轮换凭据" }, { key: "models", label: "渠道模型" }], onClick: ({ key: selected }) => selected === "models" ? setModels(channel) : setAction({ kind: selected as "edit" | "credential", channel }) }}><Button type="text" aria-label={`管理渠道 ${channel.name}`} icon={<MoreHorizontal className="size-4" />} /></Dropdown> },
            ]} />
        </TableSurface>}
        {query.data ? <PaginationBar alwaysShow current={page} pageSize={pageSize} total={query.data.total} itemLabel="个渠道" onChange={(next, size) => { setPage(next); setPageSize(size); }} /> : null}
        {action ? <PlatformChannelDialog action={action} onClose={() => setAction(undefined)} onComplete={changed} /> : null}
        {models ? <PlatformChannelModels channel={models} onClose={() => setModels(undefined)} /> : null}
    </>;
}

function LogicalModelsPanel() {
    const key = usePlatformAdminKey();
    const [page, setPage] = useState(1);
    const [pageSize, setPageSize] = useState(20);
    const [editor, setEditor] = useState<{ model?: PlatformLogicalModel }>();
    const [prices, setPrices] = useState<PlatformLogicalModel>();
    const queryClient = useQueryClient();
    const query = useQuery({ queryKey: [...key, "logical-models", page, pageSize], queryFn: ({ signal }) => listPlatformLogicalModels(page, pageSize, signal) });
    const invalidate = () => { void queryClient.invalidateQueries({ queryKey: ["platform-admin"] }); void queryClient.invalidateQueries({ queryKey: ["platform-model-catalog"] }); };
    return <>
        <ListToolbar trailing={<Button onClick={() => void query.refetch()} loading={query.isFetching} icon={<RefreshCw className="size-4" />}>刷新</Button>}><Button type="primary" icon={<Plus className="size-4" />} onClick={() => setEditor({})}>发布模型</Button></ListToolbar>
        {query.isError ? <WorkspaceErrorState title="模型加载失败" description={query.error.message} onRetry={() => void query.refetch()} /> : <TableSurface>
            <Table<PlatformLogicalModel> rowKey="id" loading={query.isLoading} pagination={false} dataSource={query.data?.models || []} scroll={{ x: 720 }} locale={{ emptyText: <WorkspaceState compact title="暂无平台模型" /> }} columns={[
                { title: "模型", dataIndex: "name", key: "name" },
                { title: "稳定标识", dataIndex: "code", key: "code" },
                { title: "能力", key: "capability", render: (_, row) => capabilityNames[row.capability] || row.capability },
                { title: "状态", key: "enabled", render: (_, row) => row.enabled ? "启用" : "停用" },
                { title: "操作", key: "actions", width: 72, render: (_, model) => <Dropdown trigger={["click"]} menu={{ items: [{ key: "edit", label: "发布新版本" }, { key: "prices", label: "固定点数价格" }], onClick: ({ key: selected }) => selected === "prices" ? setPrices(model) : setEditor({ model }) }}><Button type="text" aria-label={`管理模型 ${model.name}`} icon={<MoreHorizontal className="size-4" />} /></Dropdown> },
            ]} />
        </TableSurface>}
        {query.data ? <PaginationBar alwaysShow current={page} pageSize={pageSize} total={query.data.total} itemLabel="个模型" onChange={(next, size) => { setPage(next); setPageSize(size); }} /> : null}
        {editor ? <PlatformModelDialog model={editor.model} onClose={() => setEditor(undefined)} onComplete={() => { setEditor(undefined); invalidate(); }} /> : null}
        {prices ? <PlatformModelPrices model={prices} onClose={() => setPrices(undefined)} onComplete={invalidate} /> : null}
    </>;
}

export function PlatformAdminCatalog({ tab }: { tab: "channels" | "models" }) {
    return tab === "channels" ? <ChannelsPanel /> : <LogicalModelsPanel />;
}
