import { useEffect, useRef, useState } from "react";
import { Alert, Button, Dropdown, Grid, Input, Select, Spin, Switch, Table } from "antd";
import { Boxes, ChevronDown, MoreHorizontal, Plus } from "lucide-react";
import { useQuery } from "@tanstack/react-query";
import { EmptyState } from "@/components/ui/product/empty-state/empty-state";
import { PaginationBar } from "@/components/layout/workspace-page";
import { getPlatformModels, probePlatformChannel, savePlatformChannel, setPlatformModelEnabled, type PlatformChannel, type PlatformModel, type PlatformReadiness } from "@/services/api/platform-models";
import { MODEL_CAPABILITIES, selectorLabel } from "./model-admin-drafts";
import { PlatformModelDrawer, PlatformPriceDrawer } from "./model-admin-model-drawer";

const readinessLabels: Record<PlatformReadiness, string> = { available: "可用", partially_published: "部分已发布", channel_disabled: "渠道停用", model_disabled: "模型停用", incomplete: "待配置", unpublished: "待发布" };
export function PlatformChannelCard({ channel, expanded, onToggle, onEdit, onDelete, onAddModels, onChanged }: { channel: PlatformChannel; expanded: boolean; onToggle: () => void; onEdit: () => void; onDelete: () => void; onAddModels: () => void; onChanged: () => void }) {
    const [page, setPage] = useState(1);
    const [query, setQuery] = useState("");
    const [search, setSearch] = useState("");
    const [capability, setCapability] = useState("");
    const [status, setStatus] = useState("");
    const [busy, setBusy] = useState("");
    const [error, setError] = useState("");
    const [probeNotice, setProbeNotice] = useState("");
    const [editor, setEditor] = useState<{ kind: "model" | "price"; model: PlatformModel } | null>(null);
    const screens = Grid.useBreakpoint();
    const alive = useRef(true);
    const locked = useRef(false);
    const request = useRef<AbortController | null>(null);
    useEffect(() => {
        request.current?.abort();
        setProbeNotice("");
    }, [channel.updatedAt, channel.baseUrl]);
    useEffect(() => {
        alive.current = true;
        return () => {
            alive.current = false;
            request.current?.abort();
        };
    }, []);
    const models = useQuery({ queryKey: ["platform-models", channel.id, page, search, capability, status], queryFn: ({ signal }) => getPlatformModels(channel.id, { page, pageSize: 20, q: search, capability, status }, signal), enabled: expanded });
    async function perform(key: string, work: () => Promise<unknown>) {
        if (locked.current) return;
        locked.current = true;
        setBusy(key);
        setError("");
        try {
            await work();
            if (alive.current && key !== "probe") {
                onChanged();
                if (expanded) void models.refetch();
            }
        } catch (failure) {
            if (alive.current) setError(failure instanceof Error ? failure.message : "操作失败");
        } finally {
            if (alive.current) {
                locked.current = false;
                setBusy("");
            }
        }
    }
    const done = () => {
        setEditor(null);
        onChanged();
        void models.refetch();
    };
    return (
        <article className="platform-channel-card" data-channel-id={channel.id}>
            <div className="platform-channel-header">
                <button type="button" className="platform-channel-heading" aria-expanded={expanded} onClick={onToggle}>
                    <ChevronDown size={18} className={expanded ? "is-expanded" : ""} />
                    <span>
                        <strong>{channel.name}</strong>
                        <small title={channel.baseUrl}>{channel.baseUrl}</small>
                    </span>
                </button>
                <div className="platform-channel-counts">
                    <span>{channel.totalModels} 个模型</span>
                    <span>{channel.availableModels} 个可用</span>
                </div>
                <div className="platform-channel-actions">
                    <Switch aria-label={`启用渠道 ${channel.name}`} checked={channel.enabled} loading={busy === "enabled"} disabled={Boolean(busy)} onChange={(enabled) => void perform("enabled", () => savePlatformChannel(channel.id, { enabled }))} />
                    <Button
                        loading={busy === "probe"}
                        disabled={Boolean(busy) && busy !== "probe"}
                        onClick={() =>
                            void perform("probe", async () => {
                                setProbeNotice("");
                                request.current?.abort();
                                const controller = new AbortController();
                                request.current = controller;
                                const result = await probePlatformChannel({ channelId: channel.id }, controller.signal);
                                if (alive.current && !controller.signal.aborted) setProbeNotice(`连接成功 · ${result.models.length} 个上游模型 · ${result.durationMs} 毫秒`);
                            })
                        }
                    >
                        测试连接
                    </Button>
                    <Button onClick={onAddModels} disabled={Boolean(busy)}>
                        获取模型
                    </Button>
                    <Dropdown disabled={Boolean(busy)} menu={{ items: [{ key: "edit", label: "编辑渠道", onClick: onEdit }, { type: "divider" }, { key: "delete", label: "删除渠道", danger: true, onClick: onDelete }] }}>
                        <Button aria-label={`${channel.name} 更多操作`} disabled={Boolean(busy)} icon={<MoreHorizontal size={16} />} />
                    </Dropdown>
                </div>
            </div>
            {error && <Alert type="error" showIcon title={error} />}
            {probeNotice && (
                <p className="platform-probe-notice" role="status">
                    {probeNotice}
                </p>
            )}
            {expanded && (
                <div className="platform-channel-models">
                    <div className="platform-model-toolbar">
                        <Input.Search
                            aria-label={`搜索 ${channel.name} 模型`}
                            placeholder="搜索模型名称或标识"
                            value={query}
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
                            allowClear
                        />
                        <Select
                            aria-label={`${channel.name} 模型能力筛选`}
                            value={capability}
                            options={[{ value: "", label: "全部能力" }, ...MODEL_CAPABILITIES]}
                            onChange={(value) => {
                                setCapability(value);
                                setPage(1);
                            }}
                        />
                        <Select
                            aria-label={`${channel.name} 模型状态筛选`}
                            value={status}
                            options={[{ value: "", label: "全部状态" }, ...Object.entries(readinessLabels).map(([value, label]) => ({ value, label }))]}
                            onChange={(value) => {
                                setStatus(value);
                                setPage(1);
                            }}
                        />
                        <Button icon={<Plus size={14} />} onClick={onAddModels}>
                            添加模型
                        </Button>
                    </div>
                    {models.error ? (
                        <Alert type="error" showIcon title={models.error.message} action={<Button onClick={() => void models.refetch()}>重新加载</Button>} />
                    ) : models.isPending ? (
                        <div className="platform-model-pending">
                            <Spin aria-label="正在读取模型" />
                        </div>
                    ) : (
                        <>
                            {screens.md === false && models.data?.items.length ? (
                                <div className="platform-mobile-models">
                                    {models.data.items.map((model) => (
                                        <section className="platform-mobile-model" key={model.id}>
                                            <div className="platform-mobile-model-heading">
                                                <span className="platform-model-name">
                                                    <strong>{model.displayName || model.modelKey}</strong>
                                                    <small>{model.modelKey}</small>
                                                </span>
                                                <Switch
                                                    aria-label={`启用模型 ${model.displayName || model.modelKey}`}
                                                    checked={model.enabled}
                                                    disabled={Boolean(busy)}
                                                    loading={busy === model.id}
                                                    onChange={(enabled) => void perform(model.id, () => setPlatformModelEnabled(channel.id, model.id, enabled))}
                                                />
                                            </div>
                                            <p>
                                                {MODEL_CAPABILITIES.find((item) => item.value === model.capability)?.label} · {model.protocol}
                                            </p>
                                            <span className="platform-model-readiness" data-state={model.readiness.state}>
                                                {readinessLabels[model.readiness.state]}
                                                <small>{model.readiness.reason}</small>
                                            </span>
                                            <div className="platform-model-prices">
                                                {model.variants.map((variant) => (
                                                    <span key={variant.id}>
                                                        {selectorLabel(variant.selector)}：{variant.price ? `${variant.price.unitCredits} 积分 · ${variant.price.published ? "已发布" : "未发布"}` : "未设置"}
                                                    </span>
                                                ))}
                                            </div>
                                            <div className="platform-row-actions">
                                                <Button onClick={() => setEditor({ kind: "model", model })}>编辑</Button>
                                                <Button onClick={() => setEditor({ kind: "price", model })}>价格与发布</Button>
                                            </div>
                                        </section>
                                    ))}
                                </div>
                            ) : (
                                <Table<PlatformModel>
                                    className="platform-model-table"
                                    rowKey="id"
                                    dataSource={models.data?.items || []}
                                    loading={models.isFetching}
                                    pagination={false}
                                    scroll={{ x: 780 }}
                                    locale={{ emptyText: <EmptyState icon={Boxes} title={search || capability || status ? "没有符合条件的模型" : "此渠道暂无模型"} description="获取上游目录或手动添加模型，配置完成后发布价格。" /> }}
                                    columns={[
                                        {
                                            title: "模型",
                                            key: "name",
                                            render: (_, model) => (
                                                <span className="platform-model-name">
                                                    <strong title={model.displayName}>{model.displayName || model.modelKey}</strong>
                                                    <small title={model.modelKey}>{model.modelKey}</small>
                                                </span>
                                            ),
                                        },
                                        {
                                            title: "能力 / 协议",
                                            key: "capability",
                                            render: (_, model) => (
                                                <span className="platform-model-name">
                                                    <span>{MODEL_CAPABILITIES.find((item) => item.value === model.capability)?.label || model.capability}</span>
                                                    <small title={model.protocol}>{model.protocol || "待选择协议"}</small>
                                                </span>
                                            ),
                                        },
                                        {
                                            title: "启用",
                                            key: "enabled",
                                            render: (_, model) => (
                                                <Switch
                                                    aria-label={`启用模型 ${model.displayName || model.modelKey}`}
                                                    checked={model.enabled}
                                                    disabled={Boolean(busy)}
                                                    loading={busy === model.id}
                                                    onChange={(enabled) => void perform(model.id, () => setPlatformModelEnabled(channel.id, model.id, enabled))}
                                                />
                                            ),
                                        },
                                        {
                                            title: "就绪状态",
                                            key: "readiness",
                                            render: (_, model) => (
                                                <span className="platform-model-readiness" data-state={model.readiness.state} title={model.readiness.reason}>
                                                    {readinessLabels[model.readiness.state]}
                                                    <small>{model.readiness.reason}</small>
                                                </span>
                                            ),
                                        },
                                        {
                                            title: "积分价格",
                                            key: "price",
                                            render: (_, model) => (
                                                <div className="platform-model-prices">
                                                    {model.variants.length ? (
                                                        model.variants.map((variant) => (
                                                            <span key={variant.id} title={selectorLabel(variant.selector)}>
                                                                {selectorLabel(variant.selector)}：{variant.price ? `${variant.price.unitCredits} 积分 · ${variant.price.published ? "已发布" : "未发布"}` : "未设置"}
                                                            </span>
                                                        ))
                                                    ) : (
                                                        <span>{model.price ? `${model.price.unitCredits} 积分` : "未设置"}</span>
                                                    )}
                                                </div>
                                            ),
                                        },
                                        {
                                            title: "操作",
                                            key: "actions",
                                            render: (_, model) => (
                                                <div className="platform-row-actions">
                                                    <Button size="small" onClick={() => setEditor({ kind: "model", model })}>
                                                        编辑
                                                    </Button>
                                                    <Button size="small" onClick={() => setEditor({ kind: "price", model })}>
                                                        价格与发布
                                                    </Button>
                                                </div>
                                            ),
                                        },
                                    ]}
                                />
                            )}
                            <PaginationBar current={page} pageSize={20} pageSizeOptions={[20]} total={models.data?.total || 0} onChange={setPage} />
                        </>
                    )}
                </div>
            )}
            {editor?.kind === "model" && <PlatformModelDrawer channelId={channel.id} model={editor.model} onClose={() => setEditor(null)} onSaved={done} />}
            {editor?.kind === "price" && <PlatformPriceDrawer model={editor.model} onClose={() => setEditor(null)} onSaved={done} />}
        </article>
    );
}
