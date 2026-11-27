import { useEffect, useRef, useState } from "react";
import { Alert, Button, InputNumber, Spin, Switch } from "antd";
import { useQuery } from "@tanstack/react-query";
import { ApiError } from "@/services/api/request";
import { AppDrawer } from "@/components/ui/product/app-drawer";
import { getPlatformModelDefaults, getPlatformProtocols, saveModelPrices, savePlatformModel, type PlatformModel, type ModelPrice } from "@/services/api/platform-models";
import { PlatformDraftError, platformModelSavePayload, platformModelToDraft, resolvePlatformDraftDefaults, selectorLabel, validatePlatformModelDrafts } from "./model-admin-drafts";
import { PlatformModelFields } from "./model-admin-model-fields";

export function PlatformModelDrawer({ channelId, model, onClose, onSaved }: { channelId: string; model: PlatformModel; onClose: () => void; onSaved: () => void }) {
    const [draft, setDraft] = useState(() => platformModelToDraft(model));
    const [busy, setBusy] = useState(false);
    const [invalidJSON, setInvalidJSON] = useState(false);
    const [focusIssue, setFocusIssue] = useState<{ field: string; token: number }>();
    const [error, setError] = useState("");
    const alive = useRef(true);
    const locked = useRef(false);
    useEffect(() => {
        alive.current = true;
        return () => {
            alive.current = false;
        };
    }, []);
    const protocols = useQuery({ queryKey: ["platform-protocols"], queryFn: ({ signal }) => getPlatformProtocols(signal) });
    async function save() {
        if (locked.current) return;
        if (invalidJSON) {
            setError("请先修正高级 JSON，再保存模型");
            setFocusIssue((current) => ({ field: "json", token: (current?.token || 0) + 1 }));
            return;
        }
        const issue = validatePlatformModelDrafts([draft]);
        if (issue) {
            setError(issue.message);
            setFocusIssue((current) => ({ field: issue.field, token: (current?.token || 0) + 1 }));
            return;
        }
        locked.current = true;
        setBusy(true);
        setError("");
        try {
            const [resolved] = await resolvePlatformDraftDefaults([draft], getPlatformModelDefaults);
            if (!alive.current) return;
            setDraft(resolved);
            await savePlatformModel(channelId, model.id, platformModelSavePayload(resolved, model));
            if (alive.current) onSaved();
        } catch (failure) {
            if (alive.current) {
                setError(failure instanceof Error ? failure.message : "保存模型失败");
                if (failure instanceof PlatformDraftError || (failure instanceof ApiError && failure.status === 400)) {
                    setFocusIssue((current) => ({ field: "capabilityConfig", token: (current?.token || 0) + 1 }));
                }
            }
        } finally {
            if (alive.current) {
                locked.current = false;
                setBusy(false);
            }
        }
    }
    return (
        <AppDrawer
            open
            size="min(1040px, 100vw)"
            title={`编辑模型 · ${model.displayName || model.modelKey}`}
            rootClassName="platform-model-drawer"
            flush
            onClose={() => {
                if (!busy) onClose();
            }}
            keyboard={!busy}
            closable={{ disabled: busy }}
            mask={{ closable: !busy }}
            footer={
                <div className="platform-model-drawer-footer">
                    <span>价格发布状态需在“价格与发布”中单独调整</span>
                    <Button disabled={busy} onClick={onClose}>
                        取消
                    </Button>
                    <Button type="primary" loading={busy} onClick={() => void save()}>
                        保存模型
                    </Button>
                </div>
            }
        >
            <div className="platform-model-drawer-content">
                {error && <Alert type="error" showIcon title={error} />}
                {protocols.error && <Alert type="error" title={protocols.error.message} action={<Button onClick={() => void protocols.refetch()}>重试</Button>} />}
                {protocols.isLoading ? <Spin /> : <PlatformModelFields value={draft} onChange={setDraft} protocols={protocols.data || []} disabled={busy} focusIssue={focusIssue} onInvalidChange={setInvalidJSON} />}
            </div>
        </AppDrawer>
    );
}

export function PlatformPriceDrawer({ model, onClose, onSaved }: { model: PlatformModel; onClose: () => void; onSaved: () => void }) {
    const variants = model.variants.length ? model.variants : [{ id: "", selector: {}, enabled: true, price: model.price || null }];
    const [prices, setPrices] = useState(() => variants.map((variant) => ({ variantId: variant.id, unitCredits: variant.price?.unitCredits ?? null, published: variant.price?.published ?? false })));
    const savedPrices = useRef(prices);
    const [busy, setBusy] = useState(false);
    const [error, setError] = useState("");
    const alive = useRef(true);
    const locked = useRef(false);
    useEffect(() => {
        alive.current = true;
        return () => {
            alive.current = false;
        };
    }, []);
    async function save() {
        if (locked.current) return;
        if (prices.some((price) => price.published && price.unitCredits === null)) {
            setError("发布前请填写对应规格的积分价格");
            return;
        }
        if (prices.some((price, index) => price.unitCredits === null && savedPrices.current[index].unitCredits !== null)) {
            setError("已有价格不能清空，可关闭发布状态");
            return;
        }
        if (prices.some((price) => price.unitCredits !== null && (!Number.isSafeInteger(price.unitCredits) || price.unitCredits < 0))) {
            setError("积分价格必须为非负整数");
            return;
        }
        const changed = prices.filter((price, index) => price.unitCredits !== null && (price.unitCredits !== savedPrices.current[index].unitCredits || price.published !== savedPrices.current[index].published));
        if (!changed.length) {
            onClose();
            return;
        }
        locked.current = true;
        setBusy(true);
        setError("");
        try {
            await saveModelPrices(changed.map((price) => ({ modelId: model.id, ...price }) as ModelPrice));
            if (alive.current) onSaved();
        } catch (failure) {
            if (alive.current) setError(failure instanceof Error ? failure.message : "保存价格失败");
        } finally {
            if (alive.current) {
                locked.current = false;
                setBusy(false);
            }
        }
    }
    return (
        <AppDrawer
            open
            size="min(640px, 100vw)"
            title={`价格与发布 · ${model.displayName || model.modelKey}`}
            rootClassName="platform-model-drawer"
            flush
            onClose={() => {
                if (!busy) onClose();
            }}
            keyboard={!busy}
            closable={{ disabled: busy }}
            mask={{ closable: !busy }}
            footer={
                <div className="platform-model-drawer-footer">
                    <Button disabled={busy} onClick={onClose}>
                        取消
                    </Button>
                    <Button type="primary" loading={busy} onClick={() => void save()}>
                        保存价格与发布
                    </Button>
                </div>
            }
        >
            <div className="platform-model-drawer-content">
                {error && <Alert type="error" showIcon title={error} />}
                <p className="platform-model-muted">价格变更会使未提交的旧报价失效，已冻结任务继续按原价格结算。仅已启用的渠道、模型和已发布规格可供用户生成。</p>
                {variants.map((variant, index) => (
                    <section className="platform-variant-row" key={variant.id}>
                        <h3>{selectorLabel(variant.selector)}</h3>
                        <div className="platform-model-field-grid">
                            <label>
                                积分价格
                                <InputNumber
                                    aria-label={`规格 ${index + 1} 发布积分价格`}
                                    min={0}
                                    max={1_000_000_000}
                                    precision={0}
                                    placeholder="未设置"
                                    value={prices[index].unitCredits}
                                    disabled={busy}
                                    onChange={(unitCredits) => setPrices((current) => current.map((item, at) => (at === index ? { ...item, unitCredits, published: unitCredits === null ? false : item.published } : item)))}
                                />
                            </label>
                            <label>
                                向用户发布
                                <Switch
                                    aria-label={`发布规格 ${index + 1}`}
                                    checked={prices[index].published}
                                    disabled={busy || !variant.enabled || prices[index].unitCredits === null}
                                    onChange={(published) => setPrices((current) => current.map((item, at) => (at === index ? { ...item, published } : item)))}
                                />
                            </label>
                        </div>
                        {!variant.enabled && <p className="platform-model-muted">此规格已停用，启用后才能发布。</p>}
                    </section>
                ))}
            </div>
        </AppDrawer>
    );
}
