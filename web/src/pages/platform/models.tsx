import { useEffect, useRef, useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { Alert, App, Button, Input, Select, Spin } from "antd";
import { Network, Plus } from "lucide-react";
import { EmptyState } from "@/components/ui/product/empty-state/empty-state";
import { PaginationBar } from "@/components/layout/workspace-page";
import { getPlatformChannels, type PlatformChannel, type PlatformChannelPage } from "@/services/api/platform-models";
import { PlatformPanel } from "./platform-panel";
import { PlatformChannelCard } from "./model-admin-channel-card";
import { PlatformChannelDrawer, type ChannelEditorMode } from "./model-admin-channel-drawer";
import { DeletePlatformChannelDialog } from "./model-admin-delete-channel";
import "./model-admin.css";

export function ModelAdmin() {
    const [page, setPage] = useState(1);
    const [query, setQuery] = useState("");
    const [search, setSearch] = useState("");
    const [status, setStatus] = useState("");
    const [expanded, setExpanded] = useState<Set<string>>(new Set());
    const [focusChannel, setFocusChannel] = useState("");
    const root = useRef<HTMLDivElement>(null);
    const firstExpanded = useRef(false);
    const [editor, setEditor] = useState<{ mode: ChannelEditorMode; channel?: PlatformChannel } | null>(null);
    const [deleting, setDeleting] = useState<PlatformChannel | null>(null);
    const client = useQueryClient();
    const { message } = App.useApp();
    const channels = useQuery({ queryKey: ["platform-channels", page, search, status], queryFn: ({ signal }) => getPlatformChannels({ page, pageSize: 10, q: search, status }, signal) });
    useEffect(() => {
        if (!channels.data) return;
        const lastPage = Math.max(1, Math.ceil(channels.data.total / 10));
        if (page > lastPage) setPage(lastPage);
    }, [channels.data, page]);
    useEffect(() => {
        const first = channels.data?.channels[0];
        if (!first || firstExpanded.current) return;
        firstExpanded.current = true;
        setExpanded((current) => new Set(current).add(first.id));
    }, [channels.data]);
    useEffect(() => {
        if (!focusChannel) return;
        const heading = root.current?.querySelector<HTMLButtonElement>(`[data-channel-id="${CSS.escape(focusChannel)}"] .platform-channel-heading`);
        if (!heading) return;
        heading.scrollIntoView({ block: "nearest" });
        heading.focus({ preventScroll: true });
        setFocusChannel("");
    }, [channels.data, focusChannel]);
    function refresh() {
        void client.invalidateQueries({ queryKey: ["platform-channels"] });
    }
    function deleted(channel: PlatformChannel) {
        setDeleting(null);
        setExpanded((current) => {
            const next = new Set(current);
            next.delete(channel.id);
            return next;
        });
        client.removeQueries({ queryKey: ["platform-models", channel.id] });
        client.setQueriesData<PlatformChannelPage>({ queryKey: ["platform-channels"] }, (current) => {
            if (!current?.channels.some((item) => item.id === channel.id)) return current;
            return { ...current, channels: current.channels.filter((item) => item.id !== channel.id), total: Math.max(0, current.total - 1) };
        });
        refresh();
        void message.success("渠道已删除");
    }
    function saved(channel?: PlatformChannel) {
        if (editor?.mode === "create") {
            setPage(1);
            setQuery("");
            setSearch("");
            setStatus("");
        }
        if (channel) {
            setExpanded((current) => new Set(current).add(channel.id));
            setFocusChannel(channel.id);
        }
        setEditor(null);
        refresh();
        void client.invalidateQueries({ queryKey: ["platform-models"] });
        void message.success("配置已保存");
    }
    return (
        <div className="platform-model-admin" ref={root}>
            <PlatformPanel pagination={<PaginationBar current={page} pageSize={10} pageSizeOptions={[10]} total={channels.data?.total || 0} onChange={setPage} />}>
                <div className="platform-model-toolbar">
                    <Input.Search
                        aria-label="搜索渠道"
                        placeholder="搜索渠道名称或地址"
                        value={query}
                        allowClear
                        onChange={(event) => {
                            setQuery(event.target.value);
                            if (!event.target.value) {
                                setSearch("");
                                setPage(1);
                            }
                        }}
                        onSearch={(value) => {
                            setSearch(value.trim());
                            setPage(1);
                        }}
                    />
                    <Select
                        aria-label="渠道状态筛选"
                        value={status}
                        options={[
                            { value: "", label: "全部渠道" },
                            { value: "enabled", label: "已启用" },
                            { value: "disabled", label: "已停用" },
                        ]}
                        onChange={(value) => {
                            setStatus(value);
                            setPage(1);
                        }}
                    />
                    <Button type="primary" icon={<Plus size={16} />} onClick={() => setEditor({ mode: "create" })}>
                        新增渠道
                    </Button>
                </div>
                <p className="platform-note">渠道密钥仅保存在服务端。连接测试只查询模型目录；模型配置完成并发布积分价格后，用户才能使用。</p>
                {channels.error ? (
                    <Alert type="error" showIcon title={channels.error.message} action={<Button onClick={() => void channels.refetch()}>重新加载</Button>} />
                ) : channels.isPending ? (
                    <div className="platform-model-pending">
                        <Spin aria-label="正在读取渠道" />
                    </div>
                ) : channels.data?.channels.length ? (
                    <div className="platform-channel-list">
                        {channels.data.channels.map((channel) => (
                            <PlatformChannelCard
                                key={channel.id}
                                channel={channel}
                                expanded={expanded.has(channel.id)}
                                onToggle={() =>
                                    setExpanded((current) => {
                                        const next = new Set(current);
                                        if (next.has(channel.id)) next.delete(channel.id);
                                        else next.add(channel.id);
                                        return next;
                                    })
                                }
                                onEdit={() => setEditor({ mode: "edit", channel })}
                                onDelete={() => setDeleting(channel)}
                                onAddModels={() => setEditor({ mode: "models", channel })}
                                onChanged={refresh}
                            />
                        ))}
                    </div>
                ) : (
                    <EmptyState icon={Network} title={search || status ? "没有符合条件的渠道" : "尚未配置模型渠道"} description="新增渠道时，可以一起获取或手动添加第一批模型。" />
                )}
            </PlatformPanel>
            {editor && <PlatformChannelDrawer key={`${editor.mode}-${editor.channel?.id || "new"}`} {...editor} onClose={() => setEditor(null)} onSaved={saved} />}
            {deleting && <DeletePlatformChannelDialog key={deleting.id} channel={deleting} onClose={() => setDeleting(null)} onDeleted={() => deleted(deleting)} />}
        </div>
    );
}
