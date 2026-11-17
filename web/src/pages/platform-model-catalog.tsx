import { useQuery } from "@tanstack/react-query";
import { Alert, Button, Table, Tag } from "antd";
import { RefreshCw } from "lucide-react";
import { Navigate } from "react-router";

import { ListToolbar, TableSurface, WorkspacePage } from "@/components/layout/workspace-page";
import { WorkspaceErrorState, WorkspaceState } from "@/components/layout/workspace-state";
import { getPlatformModelCatalog } from "@/services/api/platform-catalog";
import { useUserStore } from "@/stores/use-user-store";
import "./platform-admin/platform-admin.css";

export default function PlatformModelCatalogPage() {
    const runtimeMode = useUserStore((state) => state.runtimeMode);
    const user = useUserStore((state) => state.user);
    const instanceId = useUserStore((state) => state.platformInstanceId);
    const generationEnabled = useUserStore((state) => state.features.generationEnabled);
    const query = useQuery({ queryKey: ["platform-model-catalog", instanceId, user?.id], queryFn: ({ signal }) => getPlatformModelCatalog(signal), enabled: runtimeMode === "platform" && Boolean(user) });
    if (runtimeMode !== "platform" || !user) return <Navigate to="/" replace />;
    return (
        <WorkspacePage className="platform-admin-page">
            <ListToolbar trailing={<Button icon={<RefreshCw className="size-4" />} loading={query.isFetching} onClick={() => void query.refetch()}>刷新</Button>}>
                <span>{generationEnabled ? "可使用的模型与操作价格" : "当前可查看模型与价格，生成暂未开放。"}</span>
            </ListToolbar>
            {query.isError ? <WorkspaceErrorState title="模型目录加载失败" description={query.error.message} onRetry={() => void query.refetch()} /> : query.data?.models.length ? (
                <TableSurface>
                    <Table rowKey="id" pagination={false} dataSource={query.data.models} scroll={{ x: 760 }} columns={[
                        { title: "模型", dataIndex: "name", key: "name" },
                        { title: "能力", key: "capability", render: (_, row) => ({ text: "文本", image: "图片", video: "视频", audio: "音频" }[row.capability] || row.capability) },
                        { title: "操作价格（点数）", key: "prices", render: (_, row) => <div className="flex flex-wrap gap-1">{Object.entries(row.pointPrices).map(([operation, points]) => <Tag key={operation}>{operation} · {points}</Tag>)}</div> },
                        { title: "状态", key: "available", render: (_, row) => row.available ? <Tag color="success">可用</Tag> : <Tag>暂不可用</Tag> },
                    ]} />
                </TableSurface>
            ) : query.data ? <Alert type="info" message="当前账号尚未获得模型授权" showIcon /> : <WorkspaceState title="正在读取模型目录" />}
        </WorkspacePage>
    );
}
