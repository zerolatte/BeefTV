import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { Button, Form, Input, Select, Switch, Table } from "antd";

import { ModelCapabilityEditor } from "@/components/model-capability-editor";
import { AppModal } from "@/components/ui/product/app-modal";
import { ListToolbar } from "@/components/layout/workspace-page";
import { WorkspaceErrorState, WorkspaceState } from "@/components/layout/workspace-state";
import { createPlatformChannelModel, getPlatformProtocols, listPlatformChannelModels, updatePlatformChannelModel, type PlatformChannel, type PlatformChannelModel, type PlatformChannelModelInput, type PlatformChannelModelVariant } from "@/services/api/platform-catalog";
import { capabilityNames, parseJSONField, useCatalogSubmit, usePlatformAdminKey } from "./catalog-form";

function ChannelModelForm({ channel, model, onClose, onComplete }: { channel: PlatformChannel; model?: PlatformChannelModel; onClose: () => void; onComplete: () => void }) {
    const [form] = Form.useForm<PlatformChannelModelInput & { variantsJSON: string }>();
    const key = usePlatformAdminKey();
    const protocols = useQuery({ queryKey: [...key, "protocols"], queryFn: ({ signal }) => getPlatformProtocols(signal) });
    const protocol = Form.useWatch("protocol", form);
    const selected = protocols.data?.protocols.find((item) => item.id === protocol);
    const capability = selected?.capability;
    const mutation = useCatalogSubmit();
    const save = () => mutation.submit(async () => {
        const values = await form.validateFields();
        if (!selected) throw new Error("请先选择服务端允许的协议");
        const input: PlatformChannelModelInput = {
            modelKey: values.modelKey, providerModelKey: values.providerModelKey, displayName: values.displayName, icon: values.icon || "",
            capability: selected.capability, protocol: values.protocol, enabled: values.enabled, capabilityConfig: values.capabilityConfig,
            variants: parseJSONField<PlatformChannelModelVariant[]>(values.variantsJSON, "array", "规格映射").map(({ selector, resolution, videoSeconds, providerModelKey, enabled }) => ({ selector, resolution, videoSeconds, providerModelKey, enabled })),
        };
        if (model) await updatePlatformChannelModel(channel.id, model.id, input);
        else await createPlatformChannelModel(channel.id, input);
    }, onComplete);
    return (
        <AppModal open scrollBody width={800} title={model ? "编辑渠道模型" : "新增渠道模型"} onCancel={mutation.busy ? undefined : onClose} onOk={() => void save()} confirmLoading={mutation.busy} okButtonProps={{ disabled: !protocols.data }} okText="保存" cancelText="取消" closable={!mutation.busy} maskClosable={!mutation.busy}>
            <div className="platform-admin-dialog">
                {mutation.error ? <p role="alert" className="platform-admin-error">{mutation.error}</p> : null}
                {protocols.isError ? <WorkspaceErrorState compact title="协议加载失败" description={protocols.error.message} onRetry={() => void protocols.refetch()} /> : null}
                <Form form={form} layout="vertical" disabled={mutation.busy} initialValues={{ ...model, enabled: model?.enabled ?? true, variantsJSON: JSON.stringify(model?.variants || [], null, 2) }}>
                    <Form.Item name="modelKey" label="渠道模型标识" rules={[{ required: true, message: "请输入渠道模型标识" }]}><Input maxLength={120} /></Form.Item>
                    <Form.Item name="providerModelKey" label="上游模型标识" rules={[{ required: true, message: "请输入上游模型标识" }]}><Input maxLength={120} /></Form.Item>
                    <Form.Item name="displayName" label="显示名" rules={[{ required: true, message: "请输入显示名" }]}><Input maxLength={160} /></Form.Item>
                    <Form.Item name="icon" label="图标标识"><Input maxLength={80} /></Form.Item>
                    <Form.Item name="protocol" label="协议" rules={[{ required: true, message: "请选择协议" }]} extra="仅显示平台已注册的协议。"><Select loading={protocols.isLoading} onChange={() => form.setFieldValue("capabilityConfig", undefined)} options={protocols.data?.protocols.map((item) => ({ value: item.id, label: `${item.name} · ${capabilityNames[item.capability] || item.capability}` }))} /></Form.Item>
                    <Form.Item name="enabled" label="启用模型" valuePropName="checked"><Switch /></Form.Item>
                    {capability === "text" || capability === "image" || capability === "video" ? <Form.Item name="capabilityConfig" label="模型能力"><ModelCapabilityEditor capability={capability} protocol={protocol} disabled={mutation.busy} /></Form.Item> : null}
                    <Form.Item name="variantsJSON" label="规格映射 JSON" extra="可为空数组；有多个上游规格时填写 selector、providerModelKey、enabled。"><Input.TextArea rows={4} /></Form.Item>
                </Form>
            </div>
        </AppModal>
    );
}

export function PlatformChannelModels({ channel, onClose }: { channel: PlatformChannel; onClose: () => void }) {
    const key = usePlatformAdminKey();
    const query = useQuery({ queryKey: [...key, "channel-models", channel.id], queryFn: ({ signal }) => listPlatformChannelModels(channel.id, signal) });
    const [editor, setEditor] = useState<{ model?: PlatformChannelModel }>();
    return (
        <AppModal open scrollBody title={`渠道模型：${channel.name}`} footer={null} onCancel={onClose} width={900}>
            <div className="platform-admin-dialog">
                <ListToolbar trailing={<Button loading={query.isFetching} onClick={() => void query.refetch()}>刷新</Button>}><Button type="primary" onClick={() => setEditor({})}>新增渠道模型</Button></ListToolbar>
                {query.isError ? <WorkspaceErrorState title="渠道模型加载失败" description={query.error.message} onRetry={() => void query.refetch()} /> : <Table<PlatformChannelModel> rowKey="id" loading={query.isLoading} pagination={false} dataSource={query.data?.models || []} scroll={{ x: 720 }} locale={{ emptyText: <WorkspaceState compact title="暂无渠道模型" /> }} columns={[
                    { title: "模型", key: "name", render: (_, row) => row.displayName || row.modelKey },
                    { title: "渠道模型 ID", dataIndex: "id", key: "id", ellipsis: true },
                    { title: "协议", dataIndex: "protocol", key: "protocol" },
                    { title: "状态", key: "status", render: (_, row) => row.enabled ? "启用" : "停用" },
                    { title: "操作", key: "action", render: (_, row) => <Button onClick={() => setEditor({ model: row })}>编辑</Button> },
                ]} />}
            </div>
            {editor ? <ChannelModelForm channel={channel} model={editor.model} onClose={() => setEditor(undefined)} onComplete={() => { setEditor(undefined); void query.refetch(); }} /> : null}
        </AppModal>
    );
}
