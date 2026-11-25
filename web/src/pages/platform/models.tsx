import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { Alert, App, Button, Dropdown, Form, Input, InputNumber, Select, Switch, Table } from "antd";
import { MoreHorizontal, Plus, Boxes } from "lucide-react";
import { EmptyState } from "@/components/ui/product/empty-state/empty-state";
import { StatusBadge } from "@/components/ui/base/badges/status-badge";
import { PlatformPanel } from "./platform-panel";
import { AppModal } from "@/components/ui/product/app-modal/app-modal";
import { PaginationBar } from "@/components/layout/workspace-page";
import { usePlatformMutation } from "./use-platform-mutation";
import { getModelPrices, getPlatformChannels, getPlatformModels, importPlatformModels, saveModelPrice, savePlatformChannel, savePlatformModel, type PlatformChannel, type PlatformModel } from "@/services/api/platform";

type Editor = { kind: "channel"; channel?: PlatformChannel } | { kind: "model"; model?: PlatformModel } | { kind: "price"; model: PlatformModel } | { kind: "import" };
export function ModelAdmin() {
    const [channelId, setChannelId] = useState("");
    const [channelPage, setChannelPage] = useState(1);
    const [editor, setEditor] = useState<Editor>();
    const { busy, submit } = usePlatformMutation();
    const [form] = Form.useForm();
    const { message } = App.useApp();
    const channels = useQuery({ queryKey: ["platform-channels", channelPage], queryFn: () => getPlatformChannels(channelPage) });
    const models = useQuery({ queryKey: ["platform-models", channelId], queryFn: () => getPlatformModels(channelId), enabled: Boolean(channelId) });
    const prices = useQuery({ queryKey: ["platform-prices"], queryFn: getModelPrices });
    const channel = channels.data?.channels.find((item) => item.id === channelId);
    function open(value: Editor) {
        form.resetFields();
        if (value.kind === "channel") form.setFieldsValue({ name: value.channel?.name, baseUrl: value.channel?.baseUrl, enabled: value.channel?.enabled ?? true });
        if (value.kind === "model") form.setFieldsValue({ ...value.model, enabled: value.model?.enabled ?? true, capability: value.model?.capability || "text", protocol: value.model?.protocol || "chat-completion", capabilityJSON: JSON.stringify(value.model?.capabilityConfig || {}, null, 2), variantsJSON: JSON.stringify(value.model?.variants || [], null, 2) });
        if (value.kind === "price") {
            const variantId = value.model.variants?.find((variant) => variant.enabled)?.id || "";
            const price = prices.data?.find((item) => item.modelId === value.model.id && item.variantId === variantId);
            form.setFieldsValue({ variantId, unitCredits: price?.unitCredits ?? 1, published: price?.published ?? false });
        }
        setEditor(value);
    }
    async function save() {
        if (!editor || busy) return;
        await submit(async () => {
            const values = await form.validateFields();
            if (editor.kind === "channel") await savePlatformChannel(editor.channel?.id || "", { ...values, apiKey: values.apiKey || "" });
            if (editor.kind === "model") {
                const { capabilityJSON, variantsJSON, ...rest } = values;
                let capabilityConfig: unknown;
                try { capabilityConfig = JSON.parse(capabilityJSON); } catch { throw new Error("模型能力配置不是有效的 JSON"); }
                let variants: unknown;
                try { variants = JSON.parse(variantsJSON); } catch { throw new Error("模型规格不是有效的 JSON"); }
                if (!Array.isArray(variants)) throw new Error("模型规格必须为数组");
                await savePlatformModel(channelId, editor.model?.id || "", { ...rest, capabilityConfig, variants });
            }
            if (editor.kind === "price") await saveModelPrice({ ...values, modelId: editor.model.id });
            if (editor.kind === "import") await importPlatformModels(channelId, String(values.models).split(/[\n,]/).map((value) => value.trim()).filter(Boolean));
        }, () => {
            setEditor(undefined); void channels.refetch(); void models.refetch(); void prices.refetch(); void message.success("已保存");
        });
    }
    const error = channels.error || models.error || prices.error;
    return <>
        <PlatformPanel pagination={(channels.data?.total ?? 0) > 30 && <PaginationBar current={channelPage} pageSize={30} pageSizeOptions={[30]} total={channels.data?.total ?? 0} onChange={(page) => { setChannelPage(page); setChannelId(""); }} />}>
        <div className="platform-toolbar">
            <div className="platform-toolbar-main"><Select aria-label="模型渠道" placeholder="选择渠道" value={channelId || undefined} onChange={setChannelId} options={channels.data?.channels.map((item) => ({ value: item.id, label: `${item.name}${item.enabled ? "" : "（停用）"}` }))} style={{ width: 240, maxWidth: "100%" }} /></div>
            <Button type={channel ? "default" : "primary"} icon={<Plus size={16} />} onClick={() => open({ kind: "channel" })}>新增渠道</Button>
            {channel && <><Button type="primary" icon={<Plus size={16} />} onClick={() => open({ kind: "model" })}>新增模型</Button><Dropdown menu={{ items: [{ key: "edit", label: "编辑渠道", onClick: () => open({ kind: "channel", channel }) }, { key: "import", label: "导入模型名称", onClick: () => open({ kind: "import" }) }] }}><Button aria-label="渠道更多操作" icon={<MoreHorizontal size={16} />} /></Dropdown></>}
        </div>
        {error && <Alert type="error" title={error.message} />}
        <p className="platform-note">渠道密钥仅保存在服务端。模型启用且对应规格的积分价格已发布后，用户才能生成。</p>
        <Table className="platform-table" rowKey="id" loading={models.isFetching} dataSource={models.data || []} pagination={false} scroll={{ x: 700 }} locale={{ emptyText: <EmptyState className="platform-empty" icon={Boxes} title={error ? "模型配置读取失败" : channelId ? "此渠道暂无模型" : "选择一个模型渠道"} description={error ? "请重新加载模型配置" : channelId ? "添加模型并发布积分价格后，用户即可使用" : "选择已有渠道，或新增渠道连接模型服务"} /> }} columns={[
            { title: "模型", render: (_, row: PlatformModel) => row.displayName || row.modelKey }, { title: "能力", dataIndex: "capability", render: (value: string) => ({ text: "文本", image: "图片", video: "视频", audio: "音频" })[value] || value },
            { title: "状态", dataIndex: "enabled", render: (value: boolean) => <StatusBadge tone={value ? "success" : "neutral"} label={value ? "启用" : "停用"} /> },
            { title: "发布价格", render: (_, row: PlatformModel) => prices.data?.filter((price) => price.modelId === row.id && price.published).map((price) => `${price.unitCredits} 积分`).join(" / ") || "未发布" },
            { title: "操作", render: (_, row: PlatformModel) => <div className="platform-row-actions"><Button onClick={() => open({ kind: "model", model: row })}>编辑</Button><Button onClick={() => open({ kind: "price", model: row })}>价格与发布</Button></div> },
        ]} />
        </PlatformPanel>
        <AppModal flush scrollBody className="platform-dialog" width={editor?.kind === "model" ? 640 : 480} open={Boolean(editor)} title={editor?.kind === "channel" ? "模型渠道" : editor?.kind === "model" ? "模型能力" : editor?.kind === "price" ? "价格与发布" : "导入模型"} onCancel={() => { if (!busy) setEditor(undefined); }} onOk={() => void save()} confirmLoading={busy} destroyOnHidden>
            <Form form={form} layout="vertical" className="platform-form" preserve={false}>
                {editor?.kind === "channel" && <>
                    <Form.Item name="name" label="渠道名称" rules={[{ required: true, max: 80 }]}><Input /></Form.Item>
                    <Form.Item name="baseUrl" label="模型服务地址" rules={[{ required: true, type: "url" }]}><Input placeholder="https://…" /></Form.Item>
                    <Form.Item name="apiKey" label={editor.channel ? "新密钥（留空保留原值）" : "API 密钥"}><Input.Password autoComplete="new-password" /></Form.Item>
                    <Form.Item name="enabled" label="启用渠道" valuePropName="checked"><Switch /></Form.Item>
                </>}
                {editor?.kind === "model" && <>
                    <Form.Item name="modelKey" label="模型标识" rules={[{ required: true }]}><Input /></Form.Item>
                    <Form.Item name="displayName" label="显示名称" rules={[{ required: true }]}><Input /></Form.Item>
                    <Form.Item name="providerModelKey" label="上游模型标识"><Input /></Form.Item>
                    <Form.Item name="capability" label="能力" rules={[{ required: true }]}><Select options={[{ value: "text", label: "文本" }, { value: "image", label: "图片" }, { value: "video", label: "视频" }, { value: "audio", label: "音频" }]} /></Form.Item>
                    <Form.Item name="protocol" label="请求协议标识" rules={[{ required: true }]}><Input /></Form.Item>
                    <h3 className="platform-form-section">能力与规格</h3>
                    <Form.Item name="capabilityJSON" label="能力与规格配置（JSON）" rules={[{ required: true }]}><Input.TextArea className="platform-code" autoSize={{ minRows: 6, maxRows: 14 }} /></Form.Item>
                    <Form.Item name="variantsJSON" label="规格映射（JSON 数组）" help="每项包含 selector、providerModelKey 和 enabled；无规格时填 []。" rules={[{ required: true }]}><Input.TextArea className="platform-code" autoSize={{ minRows: 4, maxRows: 12 }} /></Form.Item>
                    <Form.Item name="enabled" label="启用模型" valuePropName="checked"><Switch /></Form.Item>
                </>}
                {editor?.kind === "price" && <>
                    <Form.Item name="variantId" label="计费规格"><Select options={editor.model.variants?.length ? editor.model.variants.filter((variant) => variant.enabled).map((variant) => ({ value: variant.id, label: Object.entries(variant.selector || {}).map(([key, value]) => `${key}: ${value}`).join("，") || "默认规格" })) : [{ value: "", label: "默认规格" }]} onChange={(value) => { const price = prices.data?.find((item) => item.modelId === editor.model.id && item.variantId === value); form.setFieldsValue({ unitCredits: price?.unitCredits ?? 1, published: price?.published ?? false }); }} /></Form.Item>
                    <Form.Item name="unitCredits" label={editor.model.capability === "text" ? "每次调用积分" : editor.model.capability === "image" ? "每张图片积分" : "每个结果积分"} rules={[{ required: true }]}><InputNumber min={0} max={1000000000} precision={0} /></Form.Item>
                    <Form.Item name="published" label="向用户发布" valuePropName="checked"><Switch /></Form.Item>
                    <p>价格变更会使未提交的旧报价失效，已冻结任务继续按原价格结算。</p>
                </>}
                {editor?.kind === "import" && <Form.Item name="models" label="上游模型名称（每行一个）" rules={[{ required: true }]}><Input.TextArea autoSize={{ minRows: 5, maxRows: 12 }} /></Form.Item>}
            </Form>
        </AppModal>
    </>;
}
