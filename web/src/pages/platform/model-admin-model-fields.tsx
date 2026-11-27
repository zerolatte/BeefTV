import { useEffect, useRef, useState } from "react";
import { Alert, Button, Input, InputNumber, Select, Spin, Switch } from "antd";
import { Plus, Trash2 } from "lucide-react";
import { ModelCapabilityEditor } from "@/components/model-capability-editor";
import type { ModelCapabilityConfig } from "@/lib/model-capabilities";
import { getPlatformModelDefaults, type PlatformModelDraft, type PlatformProtocol, type PlatformVariantDraft } from "@/services/api/platform-models";
import { createPlatformVariant, mergeModelConfig, MODEL_CAPABILITIES, modelConfigVisualError, parsePlatformModelJSON } from "./model-admin-drafts";

type Props = { value: PlatformModelDraft; onChange: (value: PlatformModelDraft) => void; protocols: PlatformProtocol[]; disabled?: boolean; onInvalidChange?: (invalid: boolean) => void; focusIssue?: { field: string; token: number } };

export function PlatformModelFields({ value, onChange, protocols, disabled = false, onInvalidChange, focusIssue }: Props) {
    const [advanced, setAdvanced] = useState(false);
    const [json, setJSON] = useState("");
    const [jsonError, setJSONError] = useState("");
    const [defaults, setDefaults] = useState<Record<string, unknown> | null>(null);
    const [loading, setLoading] = useState(false);
    const [defaultError, setDefaultError] = useState("");
    const host = useRef<HTMLDivElement>(null);
    const jsonApplied = useRef<PlatformModelDraft | null>(null);
    useEffect(() => {
        if (jsonApplied.current === value) {
            jsonApplied.current = null;
            return;
        }
        if (advanced && !jsonError) setJSON(JSON.stringify(value, null, 2));
    }, [value, advanced, jsonError]);
    useEffect(() => {
        if (!focusIssue) return;
        if (focusIssue.field === "capabilityConfig" || focusIssue.field === "json") {
            if (!advanced) setJSON(JSON.stringify(value, null, 2));
            setAdvanced(true);
        }
        const labels: Record<string, string> = { modelKey: "模型标识", displayName: "模型显示名称", capability: "模型能力", protocol: "请求协议", capabilityConfig: "完整模型配置 JSON", json: "完整模型配置 JSON" };
        const timer = window.setTimeout(() => {
            const selector = labels[focusIssue.field] ? `[aria-label="${labels[focusIssue.field]}"]` : ".platform-variant-row input";
            const target = host.current?.querySelector<HTMLElement>(selector) || host.current;
            target?.scrollIntoView({ block: "center" });
            target?.focus();
        }, 0);
        return () => window.clearTimeout(timer);
    }, [focusIssue?.token]);
    useEffect(() => {
        onInvalidChange?.(Boolean(jsonError));
    }, [jsonError, onInvalidChange]);
    useEffect(() => {
        setDefaults(null);
        setDefaultError("");
        setLoading(false);
        if (!value.capability || !value.protocol) return;
        const controller = new AbortController();
        setLoading(true);
        void getPlatformModelDefaults({ model: value.modelKey, capability: value.capability, protocol: value.protocol, capabilityConfig: value.capabilityConfig }, controller.signal)
            .then((config) => {
                if (controller.signal.aborted) return;
                setDefaults(config);
            })
            .catch((error) => {
                if (!controller.signal.aborted) setDefaultError(error instanceof Error ? error.message : "无法读取模型能力");
            })
            .finally(() => {
                if (!controller.signal.aborted) setLoading(false);
            });
        return () => controller.abort();
    }, [value.capability, value.protocol, value.modelKey]);
    const patch = (next: Partial<PlatformModelDraft>) => onChange({ ...value, ...next });
    const available = protocols.filter((item) => item.capability === value.capability && item.enabled !== false);
    const protocolOptions = available.map((item) => ({ value: item.value, label: item.label }));
    if (value.protocol && !protocolOptions.some((item) => item.value === value.protocol)) protocolOptions.push({ value: value.protocol, label: `${value.protocol}（当前配置）` });
    const applyJSON = (text: string) => {
        setJSON(text);
        try {
            const next = parsePlatformModelJSON(text, value);
            jsonApplied.current = next;
            onChange(next);
            setJSONError("");
        } catch (error) {
            setJSONError(error instanceof Error ? error.message : "JSON 格式无效");
        }
    };
    const visualError = defaults ? modelConfigVisualError(value.capabilityConfig, defaults) : "";
    return (
        <div className="platform-model-fields" ref={host}>
            <div className="platform-model-editor-tools">
                <Switch checked={value.enabled} disabled={disabled || Boolean(jsonError)} onChange={(enabled) => patch({ enabled })} aria-label="启用模型" />
                <span>启用模型</span>
                <Button
                    type="text"
                    aria-expanded={advanced}
                    disabled={disabled}
                    onClick={() => {
                        if (advanced && jsonError) return;
                        if (!advanced) setJSON(JSON.stringify(value, null, 2));
                        setAdvanced(!advanced);
                    }}
                >
                    {advanced ? "返回可视化配置" : "高级 JSON"}
                </Button>
            </div>
            {advanced ? (
                <div className="platform-model-json">
                    <label>
                        完整模型配置
                        <Input.TextArea aria-label="完整模型配置 JSON" value={json} onChange={(event) => applyJSON(event.target.value)} rows={18} disabled={disabled} status={jsonError ? "error" : undefined} />
                    </label>
                    {jsonError && <Alert type="error" showIcon title={`JSON 尚未应用：${jsonError}`} />}
                    <p>修改会同步到可视化配置。格式错误时请先修正，再返回或保存。</p>
                </div>
            ) : (
                <>
                    <div className="platform-model-field-grid">
                        <label>
                            模型标识
                            <Input aria-label="模型标识" value={value.modelKey} onChange={(event) => patch({ modelKey: event.target.value })} disabled={disabled} />
                        </label>
                        <label>
                            显示名称
                            <Input aria-label="模型显示名称" value={value.displayName} onChange={(event) => patch({ displayName: event.target.value })} disabled={disabled} />
                        </label>
                        <label>
                            上游模型标识
                            <Input aria-label="上游模型标识" value={value.providerModelKey} placeholder="留空使用模型标识" onChange={(event) => patch({ providerModelKey: event.target.value })} disabled={disabled} />
                        </label>
                        <label>
                            模型能力
                            <Select aria-label="模型能力" placeholder="选择能力" value={value.capability || undefined} options={[...MODEL_CAPABILITIES]} disabled={disabled} onChange={(capability) => patch({ capability, protocol: "" })} />
                        </label>
                        <label className="platform-model-field-wide">
                            请求协议
                            <Select
                                aria-label="请求协议"
                                placeholder="选择服务端支持的协议"
                                value={value.protocol || undefined}
                                options={protocolOptions}
                                disabled={disabled || !value.capability}
                                showSearch
                                optionFilterProp="label"
                                onChange={(protocol) => patch({ protocol })}
                            />
                        </label>
                    </div>
                    <section className="platform-model-section">
                        <h3>能力与参数</h3>
                        {loading ? (
                            <Spin aria-label="正在读取模型能力" />
                        ) : defaultError ? (
                            <Alert type="error" title={defaultError} />
                        ) : visualError ? (
                            <Alert
                                type="error"
                                title={visualError}
                                action={
                                    <Button
                                        onClick={() => {
                                            setJSON(JSON.stringify(value, null, 2));
                                            setAdvanced(true);
                                        }}
                                    >
                                        打开高级 JSON
                                    </Button>
                                }
                            />
                        ) : !value.protocol ? (
                            <p>先选择能力与请求协议。</p>
                        ) : value.capability === "audio" ? (
                            <p>当前协议无需额外能力参数。</p>
                        ) : defaults && value.capability ? (
                            <ModelCapabilityEditor
                                capability={value.capability}
                                protocol={value.protocol}
                                disabled={disabled}
                                value={mergeModelConfig(defaults, value.capabilityConfig) as ModelCapabilityConfig}
                                serverDefaults={defaults as ModelCapabilityConfig}
                                onChange={(config) => patch({ capabilityConfig: mergeModelConfig(value.capabilityConfig, config as unknown as Record<string, unknown>) })}
                            />
                        ) : (
                            <p>当前协议尚无可视化能力参数。</p>
                        )}
                    </section>
                    <section className="platform-model-section">
                        <div className="platform-model-section-heading">
                            <h3>规格与价格</h3>
                            <Button icon={<Plus size={14} />} disabled={disabled} onClick={() => patch({ variants: [...value.variants, createPlatformVariant()] })}>
                                添加规格
                            </Button>
                        </div>
                        <p>价格可留空，0 表示免费。新增模型与规格保存后均未发布；发布价格后用户才能使用。</p>
                        {!value.variants.length && (
                            <Button disabled={disabled} onClick={() => patch({ variants: [createPlatformVariant()] })}>
                                配置默认规格与价格
                            </Button>
                        )}
                        <div className="platform-variant-list">
                            {value.variants.map((variant, index) => (
                                <VariantFields
                                    key={variant.id || index}
                                    index={index}
                                    value={variant}
                                    disabled={disabled}
                                    capability={value.capability}
                                    config={defaults ? (visualError ? defaults : mergeModelConfig(defaults, value.capabilityConfig)) : {}}
                                    onChange={(next) => patch({ variants: value.variants.map((item, i) => (i === index ? next : item)) })}
                                    onRemove={() => patch({ variants: value.variants.filter((_, i) => i !== index) })}
                                />
                            ))}
                        </div>
                    </section>
                </>
            )}
        </div>
    );
}

function VariantFields({
    value,
    index,
    disabled,
    capability,
    config,
    onChange,
    onRemove,
}: {
    value: PlatformVariantDraft;
    index: number;
    disabled: boolean;
    capability: PlatformModelDraft["capability"];
    config: Record<string, unknown>;
    onChange: (value: PlatformVariantDraft) => void;
    onRemove: () => void;
}) {
    const [priceError, setPriceError] = useState("");
    const entries = Object.entries(value.selector);
    const limits = config as unknown as ModelCapabilityConfig;
    const video = limits.video;
    const common =
        capability === "image"
            ? [
                  { key: "operation", label: "生成方式", values: ["text_to_image", ...(limits.image?.references?.maxImages ? ["image_to_image"] : [])] },
                  { key: "size", label: "尺寸", values: limits.image?.size?.values || [] },
                  { key: "quality", label: "质量", values: limits.image?.quality?.values || [] },
              ]
            : capability === "video"
              ? [
                    { key: "operation", label: "生成方式", values: video?.operations || [] },
                    { key: "vquality", label: "分辨率", values: video?.resolutions || [] },
                    { key: "videoSeconds", label: "时长（秒）", values: (video?.duration?.values || []).map(String) },
                ]
              : [];
    const operationLabels: Record<string, string> = {
        text_to_image: "文生图",
        image_to_image: "图生图",
        text_to_video: "文生视频",
        image_to_video: "图生视频",
        video_to_video: "视频生视频",
        reference_to_video: "参考生视频",
        audio_to_video: "音频生视频",
        draft_to_final: "草稿转成片",
        extend: "延长视频",
        inpaint: "局部重绘",
    };
    const commonKeys = new Set([...common.map((item) => item.key), ...(capability === "video" ? ["imageCount"] : [])]);
    const setCondition = (key: string, next: string | undefined) => {
        const selector = { ...value.selector };
        if (next) selector[key] = next;
        else delete selector[key];
        onChange({ ...value, selector });
    };
    return (
        <div className="platform-variant-row">
            <div className="platform-model-section-heading">
                <strong>规格 {index + 1}</strong>
                <Button type="text" danger aria-label={`删除规格 ${index + 1}`} icon={<Trash2 size={15} />} disabled={disabled} onClick={onRemove} />
            </div>
            <div className="platform-model-field-grid">
                {common.map((condition) => (
                    <label key={condition.key}>
                        {condition.label}
                        {condition.key === "videoSeconds" && video?.duration?.selection === "range" ? (
                            <InputNumber
                                aria-label={`规格 ${index + 1} ${condition.label}`}
                                value={value.selector[condition.key] ? Number(value.selector[condition.key]) : null}
                                min={video.duration.min}
                                max={video.duration.max}
                                step={video.duration.step}
                                disabled={disabled}
                                placeholder="不限定"
                                onChange={(next) => setCondition(condition.key, next === null ? undefined : String(next))}
                            />
                        ) : (
                            <Select
                                aria-label={`规格 ${index + 1} ${condition.label}`}
                                value={value.selector[condition.key] ? String(value.selector[condition.key]) : undefined}
                                placeholder="不限定"
                                allowClear
                                disabled={disabled}
                                options={[...new Set([...condition.values, ...(value.selector[condition.key] ? [String(value.selector[condition.key])] : [])])].map((entry) => ({
                                    value: entry,
                                    label: condition.key === "operation" ? operationLabels[entry] || entry : entry,
                                }))}
                                onChange={(next) => setCondition(condition.key, next)}
                            />
                        )}
                    </label>
                ))}
                {capability === "video" && (
                    <label>
                        参考图片数量
                        <InputNumber
                            aria-label={`规格 ${index + 1} 参考图片数量`}
                            value={value.selector.imageCount ? Number(value.selector.imageCount) : null}
                            min={1}
                            max={video?.references?.maxImages || undefined}
                            precision={0}
                            disabled={disabled}
                            placeholder="不限定"
                            onChange={(next) => setCondition("imageCount", next === null ? undefined : String(next))}
                        />
                    </label>
                )}
            </div>
            <div className="platform-variant-selectors">
                {entries
                    .filter(([key]) => !commonKeys.has(key))
                    .map(([key, entry], at) => (
                        <div className="platform-selector-pair" key={at}>
                            <Input
                                aria-label={`规格 ${index + 1} 条件 ${at + 1} 名称`}
                                value={key}
                                placeholder="高级参数名"
                                disabled={disabled}
                                onChange={(event) => onChange({ ...value, selector: Object.fromEntries(entries.map((item) => (item[0] === key ? [event.target.value, item[1]] : item))) })}
                            />
                            <Input
                                aria-label={`规格 ${index + 1} 条件 ${at + 1} 值`}
                                value={String(entry)}
                                placeholder="参数值"
                                disabled={disabled}
                                onChange={(event) => onChange({ ...value, selector: { ...value.selector, [key]: event.target.value } })}
                            />
                            <Button
                                type="text"
                                aria-label={`删除规格 ${index + 1} 条件 ${at + 1}`}
                                icon={<Trash2 size={14} />}
                                disabled={disabled}
                                onClick={() => onChange({ ...value, selector: Object.fromEntries(entries.filter((item) => item[0] !== key)) })}
                            />
                        </div>
                    ))}
                <Button
                    size="small"
                    disabled={disabled}
                    onClick={() => {
                        let key = "参数";
                        while (key in value.selector) key += "1";
                        onChange({ ...value, selector: { ...value.selector, [key]: "" } });
                    }}
                >
                    添加匹配条件
                </Button>
                {!entries.length && <span className="platform-model-muted">无条件时为默认规格</span>}
            </div>
            <div className="platform-model-field-grid">
                <label>
                    上游模型标识
                    <Input aria-label={`规格 ${index + 1} 上游模型标识`} value={value.providerModelKey} disabled={disabled} placeholder="留空使用模型配置" onChange={(event) => onChange({ ...value, providerModelKey: event.target.value })} />
                </label>
                <label>
                    积分价格
                    <InputNumber
                        aria-label={`规格 ${index + 1} 积分价格`}
                        min={0}
                        max={1_000_000_000}
                        precision={0}
                        value={value.price?.unitCredits ?? null}
                        placeholder="未设置"
                        disabled={disabled}
                        status={priceError ? "error" : undefined}
                        onChange={(unitCredits) => {
                            if (unitCredits === null && value.id && value.price) {
                                setPriceError("已有价格不能清空，可在价格与发布中关闭发布状态");
                                return;
                            }
                            setPriceError("");
                            onChange({ ...value, price: unitCredits === null ? null : { unitCredits } });
                        }}
                    />
                    {priceError && <span role="alert">{priceError}</span>}
                </label>
            </div>
            <div className="platform-model-inline">
                <Switch aria-label={`启用规格 ${index + 1}`} checked={value.enabled} disabled={disabled} onChange={(enabled) => onChange({ ...value, enabled })} />
                <span>启用规格</span>
            </div>
        </div>
    );
}
