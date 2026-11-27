import { useEffect, useRef, useState } from "react";
import { Alert, Button, Checkbox, Input, Select, Spin, Switch } from "antd";
import { Plus, Search, Trash2 } from "lucide-react";
import { useQuery } from "@tanstack/react-query";
import { ApiError } from "@/services/api/request";
import { AppDrawer } from "@/components/ui/product/app-drawer";
import { ChannelHeadersEditor, validateChannelHeaders } from "@/components/channel-headers-editor";
import {
    getPlatformModelDefaults,
    getPlatformProtocols,
    probePlatformChannel,
    savePlatformChannel,
    savePlatformModels,
    type PlatformChannel,
    type PlatformChannelDraft,
    type PlatformModelDraft,
    type PlatformProbeResult,
} from "@/services/api/platform-models";
import type { ChannelModelCatalogItem } from "@/lib/channel-model-catalog";
import { catalogModelMapping } from "@/lib/channel-model-catalog";
import { appendPlatformModelDrafts, createPlatformModelDraft, MODEL_CAPABILITIES, parseManualModelKeys, PlatformDraftError, platformModelSavePayload, resolvePlatformDraftDefaults, validatePlatformModelDrafts } from "./model-admin-drafts";
import { PlatformModelFields } from "./model-admin-model-fields";

export type ChannelEditorMode = "create" | "edit" | "models";
export function PlatformChannelDrawer({ channel, mode, onClose, onSaved }: { channel?: PlatformChannel; mode: ChannelEditorMode; onClose: () => void; onSaved: (channel?: PlatformChannel) => void }) {
    const [connection, setConnection] = useState<PlatformChannelDraft>({ name: channel?.name || "", baseUrl: channel?.baseUrl || "", apiKey: "", enabled: channel?.enabled ?? true });
    const [models, setModels] = useState<PlatformModelDraft[]>([]);
    const [selectedDrafts, setSelectedDrafts] = useState<Set<number>>(new Set());
    const [active, setActive] = useState<number | null>(null);
    const [catalog, setCatalog] = useState<ChannelModelCatalogItem[]>([]);
    const [existingModelIds, setExistingModelIds] = useState<string[]>([]);
    const [selected, setSelected] = useState<string[]>([]);
    const [catalogQuery, setCatalogQuery] = useState("");
    const [manual, setManual] = useState("");
    const [catalogFormat, setCatalogFormat] = useState<"openai" | "gemini">("openai");
    const [bulkCapability, setBulkCapability] = useState<PlatformModelDraft["capability"]>("");
    const [bulkProtocol, setBulkProtocol] = useState("");
    const [busy, setBusy] = useState<"probe" | "catalog" | "save" | "">("");
    const [error, setError] = useState("");
    const [notice, setNotice] = useState("");
    const [invalidJSON, setInvalidJSON] = useState(false);
    const [focusIssue, setFocusIssue] = useState<{ field: string; token: number }>();
    const [advancedConnection, setAdvancedConnection] = useState(false);
    const [replaceHeaders, setReplaceHeaders] = useState(false);
    const [headersDraft, setHeadersDraft] = useState<{ name: string; value: string }[]>([]);
    const controller = useRef<AbortController | null>(null);
    const alive = useRef(true);
    const locked = useRef(false);
    const autoProbeStarted = useRef(false);
    const fields = useRef<HTMLDivElement>(null);
    const protocols = useQuery({ queryKey: ["platform-protocols"], queryFn: ({ signal }) => getPlatformProtocols(signal) });
    useEffect(() => {
        alive.current = true;
        return () => {
            alive.current = false;
            controller.current?.abort();
        };
    }, []);
    const clearCatalog = () => {
        controller.current?.abort();
        setCatalog([]);
        setSelected([]);
        setNotice("");
    };
    const patchConnection = (next: Partial<PlatformChannelDraft>) => {
        setConnection((current) => ({ ...current, ...next }));
        clearCatalog();
    };
    const connectionPayload = () => ({ ...connection, ...(replaceHeaders ? { headers: headersDraft } : {}) });
    async function probe(discover: boolean) {
        if (locked.current) return;
        if (!channel && !connection.baseUrl.trim()) {
            setError("请填写模型服务地址");
            return;
        }
        controller.current?.abort();
        const request = new AbortController();
        controller.current = request;
        locked.current = true;
        setBusy(discover ? "catalog" : "probe");
        setError("");
        setNotice("");
        try {
            const { baseUrl, apiKey, secretKey, headers } = connectionPayload();
            const result: PlatformProbeResult = await probePlatformChannel({ ...(mode === "models" ? {} : { baseUrl, apiKey, secretKey, headers }), channelId: channel?.id, catalogFormat }, request.signal);
            if (!alive.current || request.signal.aborted) return;
            setExistingModelIds(result.existingModelIds);
            if (discover) {
                setCatalog(result.models);
                setSelected([]);
            }
            setNotice(`${discover ? "已获取" : "连接成功，发现"} ${result.models.length} 个模型，耗时 ${result.durationMs} 毫秒。${discover ? "勾选后加入配置草稿。" : "本次仅查询目录，未提交生成任务。"}`);
        } catch (failure) {
            if (alive.current && !request.signal.aborted) setError(failure instanceof Error ? failure.message : "连接模型服务失败");
        } finally {
            if (alive.current) {
                locked.current = false;
                setBusy("");
            }
        }
    }
    useEffect(() => {
        if (mode !== "models") return;
        const timer = window.setTimeout(() => {
            if (!autoProbeStarted.current) {
                autoProbeStarted.current = true;
                void probe(true);
            }
        }, 0);
        return () => window.clearTimeout(timer);
    }, []);
    function add(items: PlatformModelDraft[]) {
        if (invalidJSON) {
            setError("请先修正当前模型的高级 JSON");
            return;
        }
        const existing = new Set(existingModelIds.map((id) => id.toLocaleLowerCase()));
        const accepted = items.filter((item) => !existing.has(item.modelKey.trim().toLocaleLowerCase()));
        const next = appendPlatformModelDrafts(models, accepted);
        setError("");
        setFocusIssue(undefined);
        setModels(next);
        setNotice(`当前草稿共 ${next.length} 个模型。${accepted.length < items.length ? "已有模型已跳过（包含停用模型）。" : ""}`);
        setSelectedDrafts((current) => new Set([...current, ...next.slice(models.length).map((_, index) => models.length + index)]));
        if (next.length > models.length) setActive(models.length);
    }
    async function save() {
        if (locked.current) return;
        setError("");
        if (invalidJSON) {
            setError("请先修正当前模型的高级 JSON");
            setFocusIssue((current) => ({ field: "json", token: (current?.token || 0) + 1 }));
            return;
        }
        if (mode !== "models" && (!connection.name.trim() || !connection.baseUrl.trim())) {
            setError("请填写渠道名称和模型服务地址");
            return;
        }
        const headerError = replaceHeaders ? validateChannelHeaders(headersDraft) : "";
        if (headerError) {
            setError(headerError);
            setAdvancedConnection(true);
            return;
        }
        if (mode === "models" && !models.length) {
            setError("请先获取或手动添加模型");
            return;
        }
        const issue = validatePlatformModelDrafts(models);
        if (issue) {
            setActive(issue.modelIndex);
            setError(issue.message);
            setFocusIssue((current) => ({ field: issue.field, token: (current?.token || 0) + 1 }));
            return;
        }
        locked.current = true;
        setBusy("save");
        try {
            const resolved = await resolvePlatformDraftDefaults(models, getPlatformModelDefaults);
            if (!alive.current) return;
            setModels(resolved);
            const payload = resolved.map((model) => platformModelSavePayload(model));
            const result =
                mode === "models" && channel
                    ? (await savePlatformModels(channel.id, payload), channel)
                    : await savePlatformChannel(channel?.id || "", { ...connectionPayload(), name: connection.name.trim(), baseUrl: connection.baseUrl.trim(), ...(mode === "create" ? { initialModels: payload } : {}) });
            if (alive.current) onSaved(result);
        } catch (failure) {
            if (alive.current) {
                setError(failure instanceof Error ? failure.message : "保存失败，请重试");
                const index = failure instanceof PlatformDraftError ? failure.modelIndex : failure instanceof ApiError ? failure.details?.modelIndex : undefined;
                if (typeof index === "number" && Number.isInteger(index) && models[index]) {
                    setActive(index);
                    setFocusIssue((current) => ({ field: "capabilityConfig", token: (current?.token || 0) + 1 }));
                } else {
                    fields.current?.closest(".ant-drawer-body")?.scrollTo({ top: 0 });
                }
            }
        } finally {
            if (alive.current) {
                locked.current = false;
                setBusy("");
            }
        }
    }
    const catalogVisible = catalog.filter((item) => `${item.id} ${item.displayName || ""}`.toLocaleLowerCase().includes(catalogQuery.trim().toLocaleLowerCase()));
    const disabled = Boolean(busy);
    return (
        <AppDrawer
            open
            size="min(1120px, 100vw)"
            title={mode === "create" ? "新增渠道与模型" : mode === "edit" ? `编辑渠道 · ${channel?.name}` : `添加模型 · ${channel?.name}`}
            rootClassName="platform-model-drawer"
            flush
            onClose={() => {
                if (busy !== "save") {
                    controller.current?.abort();
                    onClose();
                }
            }}
            keyboard={busy !== "save"}
            closable={{ disabled: busy === "save" }}
            mask={{ closable: busy !== "save" }}
            footer={
                <div className="platform-model-drawer-footer">
                    <span>{models.length ? `待保存 ${models.length} 个模型` : mode === "create" ? "可仅保存渠道，稍后添加模型" : ""}</span>
                    <Button disabled={busy === "save"} onClick={onClose}>
                        取消
                    </Button>
                    <Button type="primary" loading={busy === "save"} disabled={disabled && busy !== "save"} onClick={() => void save()}>
                        {mode === "models" ? "添加所选模型" : "保存渠道"}
                    </Button>
                </div>
            }
        >
            <div className="platform-model-drawer-content">
                {error && <Alert type="error" showIcon title={error} />}
                {protocols.error && (
                    <Alert
                        type="error"
                        showIcon
                        title={protocols.error.message}
                        action={
                            <Button size="small" onClick={() => void protocols.refetch()}>
                                重试
                            </Button>
                        }
                    />
                )}
                {notice && <Alert type="success" showIcon title={notice} />}
                {mode !== "models" && (
                    <section className="platform-model-section">
                        <h3>连接信息</h3>
                        <div className="platform-model-field-grid">
                            <label>
                                渠道名称
                                <Input aria-label="渠道名称" value={connection.name} maxLength={80} disabled={disabled} onChange={(event) => patchConnection({ name: event.target.value })} />
                            </label>
                            <label>
                                模型服务地址
                                <Input aria-label="模型服务地址" value={connection.baseUrl} placeholder="https://…" disabled={disabled} onChange={(event) => patchConnection({ baseUrl: event.target.value })} />
                            </label>
                            <label className="platform-model-field-wide">
                                {channel ? "新密钥（留空保留原值）" : "API 密钥"}
                                <Input.Password
                                    aria-label="渠道 API 密钥"
                                    autoComplete="new-password"
                                    value={connection.apiKey}
                                    disabled={disabled}
                                    placeholder={channel?.hasApiKey ? "已保存密钥" : "填写服务商密钥"}
                                    onChange={(event) => patchConnection({ apiKey: event.target.value })}
                                />
                            </label>
                        </div>
                        <div className="platform-model-inline">
                            <Switch aria-label="启用渠道" checked={connection.enabled} disabled={disabled} onChange={(enabled) => patchConnection({ enabled })} />
                            <span>启用渠道</span>
                            <Button type="text" aria-expanded={advancedConnection} onClick={() => setAdvancedConnection(!advancedConnection)}>
                                高级连接设置
                            </Button>
                        </div>
                        {advancedConnection && (
                            <div className="platform-model-section">
                                <label>
                                    Secret Key（需要签名认证时填写）
                                    <Input.Password
                                        aria-label="渠道 Secret Key"
                                        autoComplete="new-password"
                                        value={connection.secretKey || ""}
                                        disabled={disabled}
                                        placeholder={channel ? "留空保留原值" : "可选"}
                                        onChange={(event) => patchConnection({ secretKey: event.target.value })}
                                    />
                                </label>
                                <Checkbox
                                    checked={replaceHeaders}
                                    disabled={disabled}
                                    onChange={(event) => {
                                        setReplaceHeaders(event.target.checked);
                                        clearCatalog();
                                    }}
                                >
                                    {channel ? "替换已保存的自定义请求头" : "设置自定义请求头"}
                                </Checkbox>
                                {replaceHeaders ? (
                                    <ChannelHeadersEditor
                                        value={headersDraft}
                                        disabled={disabled}
                                        onChange={(value) => {
                                            setHeadersDraft(value);
                                            clearCatalog();
                                        }}
                                    />
                                ) : channel?.hasHeaders ? (
                                    <p>已保存请求头。勾选后填写完整内容；空列表会清空原请求头。</p>
                                ) : null}
                            </div>
                        )}
                    </section>
                )}
                <div className="platform-model-inline">
                    <Select
                        aria-label="模型目录格式"
                        value={catalogFormat}
                        options={[
                            { value: "openai", label: "OpenAI 兼容目录" },
                            { value: "gemini", label: "Gemini 目录" },
                        ]}
                        disabled={disabled}
                        onChange={(value) => {
                            setCatalogFormat(value);
                            clearCatalog();
                        }}
                    />
                    <Button loading={busy === "probe"} disabled={disabled && busy !== "probe"} onClick={() => void probe(false)}>
                        测试连接
                    </Button>
                    {mode !== "edit" && (
                        <Button icon={<Search size={15} />} loading={busy === "catalog"} disabled={disabled && busy !== "catalog"} onClick={() => void probe(true)}>
                            获取模型
                        </Button>
                    )}
                </div>
                {mode !== "edit" && (
                    <>
                        {catalog.length > 0 && (
                            <section className="platform-model-section">
                                <div className="platform-model-section-heading">
                                    <h3>上游模型目录</h3>
                                    <span>已选 {selected.length} 个</span>
                                </div>
                                <Input.Search aria-label="搜索上游模型" placeholder="搜索模型名称或标识" value={catalogQuery} onChange={(event) => setCatalogQuery(event.target.value)} />
                                <div className="platform-catalog-list">
                                    {catalogVisible.map((item) => {
                                        const existing = existingModelIds.some((id) => id.toLocaleLowerCase() === item.id.toLocaleLowerCase()) || models.some((model) => model.modelKey.toLocaleLowerCase() === item.id.toLocaleLowerCase());
                                        return (
                                            <label className="platform-catalog-row" key={item.id}>
                                                <Checkbox
                                                    checked={selected.includes(item.id)}
                                                    disabled={disabled || existing}
                                                    onChange={(event) => setSelected((current) => (event.target.checked ? [...current, item.id] : current.filter((id) => id !== item.id)))}
                                                />
                                                <span>
                                                    <strong>{item.displayName || item.id}</strong>
                                                    {item.displayName && <small>{item.id}</small>}
                                                </span>
                                                <span>{existing ? "已添加" : item.modelType ? MODEL_CAPABILITIES.find((option) => option.value === item.modelType)?.label : "待配置能力"}</span>
                                            </label>
                                        );
                                    })}
                                </div>
                                <Button
                                    disabled={disabled || !selected.length}
                                    onClick={() => {
                                        add(
                                            catalog
                                                .filter((item) => selected.includes(item.id))
                                                .map((item) => {
                                                    const mapping = catalogModelMapping(item, { providerNameFallback: false });
                                                    const capability = item.modelType || mapping.capability || "";
                                                    const protocol = (protocols.data || []).find((candidate) => candidate.value === mapping.protocol && candidate.capability === capability)?.value || "";
                                                    return {
                                                        ...createPlatformModelDraft(item.id),
                                                        displayName: item.displayName || item.id,
                                                        capability,
                                                        protocol,
                                                        capabilityConfig: item.imageCapabilities ? { version: 1, image: item.imageCapabilities } : item.videoCapabilities ? { version: 1, video: item.videoCapabilities } : {},
                                                    };
                                                }),
                                        );
                                        setSelected([]);
                                    }}
                                >
                                    加入所选模型
                                </Button>
                            </section>
                        )}
                        <section className="platform-model-section">
                            <h3>手动批量添加</h3>
                            <Input.TextArea aria-label="批量模型标识" placeholder="每行一个模型标识，也支持逗号分隔" value={manual} rows={3} disabled={disabled} onChange={(event) => setManual(event.target.value)} />
                            <Button
                                icon={<Plus size={14} />}
                                disabled={disabled || !manual.trim()}
                                onClick={() => {
                                    add(parseManualModelKeys(manual).map((key) => createPlatformModelDraft(key)));
                                    setManual("");
                                }}
                            >
                                添加到草稿
                            </Button>
                        </section>
                        {models.length > 0 && (
                            <section className="platform-model-section">
                                <div className="platform-model-section-heading">
                                    <h3>模型草稿</h3>
                                    <span>
                                        {models.length} 个 · 已选 {selectedDrafts.size} 个
                                    </span>
                                </div>
                                <div className="platform-model-inline">
                                    <Select
                                        aria-label="批量模型能力"
                                        placeholder="批量选择能力"
                                        value={bulkCapability || undefined}
                                        options={[...MODEL_CAPABILITIES]}
                                        disabled={disabled || invalidJSON}
                                        onChange={(capability) => {
                                            setBulkCapability(capability);
                                            setBulkProtocol("");
                                        }}
                                    />
                                    <Select
                                        aria-label="批量请求协议"
                                        placeholder="批量选择协议"
                                        value={bulkProtocol || undefined}
                                        options={(protocols.data || []).filter((item) => item.capability === bulkCapability && item.enabled !== false).map((item) => ({ value: item.value, label: item.label }))}
                                        disabled={disabled || !bulkCapability || invalidJSON}
                                        onChange={setBulkProtocol}
                                    />
                                    <Button
                                        disabled={disabled || !bulkCapability || !bulkProtocol || invalidJSON || !selectedDrafts.size}
                                        onClick={() => setModels((current) => current.map((model, index) => (selectedDrafts.has(index) ? { ...model, capability: bulkCapability, protocol: bulkProtocol } : model)))}
                                    >
                                        应用到选中草稿
                                    </Button>
                                </div>
                                <p>批量应用只修改能力与协议，已填写的能力参数、规格和价格会保留。</p>
                                <div className="platform-draft-tabs">
                                    {models.map((model, index) => (
                                        <div key={index} className="platform-draft-tab">
                                            <Checkbox
                                                aria-label={`选择模型草稿 ${index + 1}`}
                                                checked={selectedDrafts.has(index)}
                                                disabled={disabled || invalidJSON}
                                                onChange={(event) =>
                                                    setSelectedDrafts((current) => {
                                                        const next = new Set(current);
                                                        if (event.target.checked) next.add(index);
                                                        else next.delete(index);
                                                        return next;
                                                    })
                                                }
                                            />
                                            <button
                                                type="button"
                                                aria-pressed={active === index}
                                                disabled={disabled || invalidJSON}
                                                onClick={() => {
                                                    setFocusIssue(undefined);
                                                    setActive(index);
                                                }}
                                            >
                                                {model.displayName || model.modelKey || `模型 ${index + 1}`}
                                            </button>
                                            <Button
                                                type="text"
                                                danger
                                                aria-label={`移除模型草稿 ${index + 1}`}
                                                icon={<Trash2 size={14} />}
                                                disabled={disabled || invalidJSON}
                                                onClick={() => {
                                                    setModels((current) => current.filter((_, at) => at !== index));
                                                    setSelectedDrafts((current) => new Set([...current].filter((at) => at !== index).map((at) => (at > index ? at - 1 : at))));
                                                    setActive(null);
                                                    setFocusIssue(undefined);
                                                }}
                                            />
                                        </div>
                                    ))}
                                </div>
                                <div ref={fields}>
                                    {protocols.isLoading ? (
                                        <Spin />
                                    ) : (
                                        active !== null &&
                                        models[active] && (
                                            <PlatformModelFields
                                                key={active}
                                                value={models[active]}
                                                protocols={protocols.data || []}
                                                disabled={disabled}
                                                focusIssue={focusIssue}
                                                onInvalidChange={setInvalidJSON}
                                                onChange={(model) => setModels((current) => current.map((item, index) => (index === active ? model : item)))}
                                            />
                                        )
                                    )}
                                </div>
                            </section>
                        )}
                    </>
                )}
            </div>
        </AppDrawer>
    );
}
