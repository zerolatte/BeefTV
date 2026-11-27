import { useEffect, useRef, useState } from "react";
import { Alert, App, Button, Form, Input, Spin } from "antd";
import { Cloud, HardDrive } from "lucide-react";
import { getStorageSettings, probeStorage, saveStorageSettings, type OSSDraft, type StorageProvider, type StorageSettings } from "@/services/api/platform-storage";
import { PlatformPanel } from "./platform-panel";
import "./storage.css";

function bytes(value: number) {
    if (value < 1024) return `${value} B`;
    const unit = Math.min(4, Math.floor(Math.log(value) / Math.log(1024)));
    return `${(value / 1024 ** unit).toFixed(1)} ${["B", "KB", "MB", "GB", "TB"][unit]}`;
}

export function StorageAdmin() {
    const [form] = Form.useForm<OSSDraft>();
    const { message } = App.useApp();
    const [saved, setSaved] = useState<StorageSettings>();
    const [provider, setProvider] = useState<StorageProvider>("local");
    const [dirty, setDirty] = useState(false);
    const [ossDirty, setOSSDirty] = useState(false);
    const [busy, setBusy] = useState<"load" | "probe" | "save" | undefined>("load");
    const [error, setError] = useState("");
    const [probeResult, setProbeResult] = useState("");
    const request = useRef<AbortController | undefined>(undefined);
    const revision = useRef(0);
    const operation = useRef<"load" | "probe" | "save" | undefined>(undefined);
    const errorElement = useRef<HTMLDivElement>(null);

    function apply(value: StorageSettings) {
        setSaved(value); setProvider(value.provider); setDirty(false); setOSSDirty(false);
        form.setFieldsValue({ endpoint: value.oss?.endpoint ?? "", region: value.oss?.region ?? "", bucket: value.oss?.bucket ?? "", accessKeyId: "", accessKeySecret: "" });
    }
    function begin(kind: "load" | "probe" | "save") {
        request.current?.abort();
        const controller = new AbortController();
        request.current = controller;
        const id = ++revision.current;
        operation.current = kind; setBusy(kind); setError(""); setProbeResult("");
        return { controller, id, current: () => !controller.signal.aborted && revision.current === id };
    }
    function finish(id: number) {
        if (revision.current === id) { operation.current = undefined; setBusy(undefined); }
    }
    async function load() {
        if (operation.current) return;
        const run = begin("load");
        try { const value = await getStorageSettings(run.controller.signal); if (run.current()) apply(value); }
        catch (err) { if (run.current()) setError(err instanceof Error ? err.message : "存储设置读取失败"); }
        finally { finish(run.id); }
    }
    useEffect(() => {
        void load();
        return () => { ++revision.current; request.current?.abort(); operation.current = undefined; };
        // 表单生命周期只读取一次；刷新由显式按钮触发，避免覆盖正在输入的草稿。
        // eslint-disable-next-line react-hooks/exhaustive-deps
    }, []);
    useEffect(() => { if (error) errorElement.current?.focus(); }, [error]);

    function cancelProbe() {
        if (operation.current === "probe") {
            request.current?.abort(); ++revision.current; operation.current = undefined; setBusy(undefined);
        }
    }
    function changed(connection: boolean) {
        cancelProbe();
        setDirty(true); if (connection) setOSSDirty(true);
        setProbeResult(""); setError("");
    }
    async function validateOSS() {
        try { return await form.validateFields(); }
        catch (err) {
            const fields = (err as { errorFields?: { name: string[] }[] }).errorFields;
            if (fields?.[0]) form.scrollToField(fields[0].name, { focus: true, block: "center" });
            return undefined;
        }
    }
    async function test() {
        if (operation.current || !saved) return;
        const draft = await validateOSS();
        if (!draft || operation.current) return;
        const run = begin("probe");
        try {
            const result = await probeStorage(draft, run.controller.signal);
            if (run.current()) setProbeResult(`${result.message}，耗时 ${(result.elapsedMs / 1000).toFixed(1)} 秒`);
        } catch (err) { if (run.current()) setError(err instanceof Error ? err.message : "连接测试失败"); }
        finally { finish(run.id); }
    }
    async function save() {
        if (operation.current || !saved || !dirty) return;
        const includeOSS = provider === "oss";
        const draft = includeOSS ? await validateOSS() : undefined;
        if ((includeOSS && !draft) || operation.current) return;
        const run = begin("save");
        try {
            const value = await saveStorageSettings({ provider, expectedVersion: saved.version, oss: draft }, run.controller.signal);
            if (run.current()) {
                const retained = provider === "local" && ossDirty ? form.getFieldsValue(true) : undefined;
                apply(value);
                if (retained) { form.setFieldsValue(retained); setOSSDirty(true); }
                void message.success("存储设置已保存");
            }
        } catch (err) { if (run.current()) setError(err instanceof Error ? err.message : "保存失败，请重试"); }
        finally { finish(run.id); }
    }
    const locked = busy === "save" || busy === "load";
    const footer = <div className="storage-footer"><span>{dirty ? "有未保存的设置" : "设置已保存"}</span><Button type="primary" loading={busy === "save"} disabled={!saved || !dirty || Boolean(busy)} onClick={() => void save()}>保存设置</Button></div>;

    return <div className="platform-storage"><PlatformPanel pagination={footer}>
        {error && <div ref={errorElement} tabIndex={-1}><Alert showIcon type="error" title={error} action={<Button disabled={Boolean(busy)} onClick={() => void load()}>{saved ? "刷新已保存配置" : "重试"}</Button>} /></div>}
        {!saved && busy === "load" && <Spin aria-label="正在读取存储设置" />}
        {saved && <>
            <div className="storage-current"><span>当前保存策略</span><strong>{saved.provider === "local" ? "本地存储" : "阿里云 OSS"}</strong><span>仅影响后续文件，历史文件继续从原位置读取。</span></div>
            <div className="storage-statistics">{(["local", "oss"] as const).map((value) => {
                const stats = saved.statistics.find((item) => item.provider === value);
                const Icon = value === "local" ? HardDrive : Cloud;
                return <div className="storage-stat" key={value}><Icon size={20} aria-hidden /><span>{value === "local" ? "本地文件" : "OSS 文件"}</span><strong>{(stats?.count ?? 0).toLocaleString()} 个 · {bytes(stats?.bytes ?? 0)}</strong></div>;
            })}</div>
            <section className="storage-section"><h2>保存位置</h2><div className="storage-options">{(["local", "oss"] as const).map((value) => {
                const Icon = value === "local" ? HardDrive : Cloud;
                return <button type="button" key={value} aria-pressed={provider === value} disabled={locked} onClick={() => { if (provider !== value) { setProvider(value); changed(false); } }}><Icon size={20} aria-hidden /><span>{value === "local" ? "本地存储" : "阿里云 OSS"}</span></button>;
            })}</div><p>OSS 写入失败时自动保存到本地。OSS 文件不会永久保留本地副本；历史 OSS 文件访问失败时需要重试。</p></section>
            <section className="storage-section" hidden={provider !== "oss"}><h2>OSS 连接配置</h2><Form form={form} layout="vertical" disabled={locked} onValuesChange={() => changed(true)} preserve className="storage-form">
                <Form.Item name="endpoint" label="Endpoint" rules={[{ required: true, message: "请输入阿里云官方 HTTPS Endpoint" }]}><Input placeholder="https://oss-cn-shanghai.aliyuncs.com" autoComplete="off" /></Form.Item>
                <div className="storage-fields"><Form.Item name="region" label="Region" rules={[{ required: true, message: "请输入区域" }]}><Input placeholder="cn-shanghai" autoComplete="off" /></Form.Item><Form.Item name="bucket" label="Bucket" rules={[{ required: true, message: "请输入私有 Bucket 名称" }]}><Input placeholder="私有 Bucket 名称" autoComplete="off" /></Form.Item></div>
                <div className="storage-fields"><Form.Item name="accessKeyId" label={`AccessKey ID${saved.oss?.hasAccessKeyId ? " · 已配置" : ""}`} rules={[{ required: !saved.oss?.hasAccessKeyId, message: "请输入 AccessKey ID" }]}><Input.Password autoComplete="new-password" placeholder={saved.oss?.hasAccessKeyId ? "留空保留已有密钥" : "输入 AccessKey ID"} /></Form.Item><Form.Item name="accessKeySecret" label={`AccessKey Secret${saved.oss?.hasAccessKeySecret ? " · 已配置" : ""}`} dependencies={["accessKeyId"]} rules={[({ getFieldValue }) => ({ validator(_, value) { return (value || (saved.oss?.hasAccessKeySecret && !getFieldValue("accessKeyId"))) ? Promise.resolve() : Promise.reject(new Error("请输入匹配的 Secret；更换 ID 时需同时填写")); } })]}><Input.Password autoComplete="new-password" placeholder={saved.oss?.hasAccessKeySecret ? "留空保留已有密钥" : "输入 AccessKey Secret"} /></Form.Item></div>
                <p>密钥加密保存，仅显示配置状态。保存 OSS 设置时将重新验证写入、读取和删除权限。</p>
                <div className="storage-probe"><Button loading={busy === "probe"} disabled={locked || busy === "probe"} onClick={() => void test()}>测试连接</Button>{busy === "probe" && <Button onClick={() => { cancelProbe(); setProbeResult("连接测试已取消"); }}>取消测试</Button>}</div>
            </Form>{probeResult && <Alert type="info" showIcon title={probeResult} />}</section>
            <section className="storage-section"><h2>最近自动回退</h2>{saved.recentFallbacks.length ? <ul className="storage-fallbacks">{saved.recentFallbacks.map((item, index) => <li key={`${item.resourceId}-${item.createdAt}-${index}`}><span>{item.reason}</span><time dateTime={item.createdAt}>{new Date(item.createdAt).toLocaleString()}</time><span className="storage-resource-id" title={item.resourceId}>资源 {item.resourceId}</span></li>)}</ul> : <p>暂无自动回退记录。</p>}</section>
        </>}
    </PlatformPanel></div>;
}
