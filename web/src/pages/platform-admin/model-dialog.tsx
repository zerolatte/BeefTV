import { useEffect, useRef } from "react";
import { useQuery } from "@tanstack/react-query";
import { Form, Input, InputNumber, Switch } from "antd";

import { AppModal } from "@/components/ui/product/app-modal";
import { WorkspaceErrorState, WorkspaceLoadingState } from "@/components/layout/workspace-state";
import { getPlatformLogicalModel, publishPlatformLogicalModel, type PlatformLogicalModel, type PlatformLogicalModelInput } from "@/services/api/platform-catalog";
import { parseJSONField, useCatalogSubmit, usePlatformAdminKey } from "./catalog-form";

type ModelForm = Pick<PlatformLogicalModelInput, "code" | "name" | "icon" | "description" | "enabled" | "sortOrder"> & { capabilitySpecJSON: string; defaultOptionsJSON: string; routesJSON: string };

export function PlatformModelDialog({ model, onClose, onComplete }: { model?: PlatformLogicalModel; onClose: () => void; onComplete: () => void }) {
    const [form] = Form.useForm<ModelForm>();
    const key = usePlatformAdminKey();
    const detail = useQuery({ queryKey: [...key, "model", model?.id], queryFn: ({ signal }) => getPlatformLogicalModel(model!.id, signal), enabled: Boolean(model), refetchOnWindowFocus: false });
    const revision = useRef<string | undefined>(undefined);
    const initialized = useRef(false);
    const mutation = useCatalogSubmit();
    useEffect(() => {
        if (!detail.data || !detail.isFetchedAfterMount || initialized.current) return;
        const { model: current, capabilitySpec, defaultOptions, routes, revision: loadedRevision } = detail.data;
        initialized.current = true;
        revision.current = loadedRevision.id;
        form.setFieldsValue({ code: current.code, name: current.name, icon: current.icon, description: current.description, enabled: current.enabled, sortOrder: current.sortOrder, capabilitySpecJSON: JSON.stringify(capabilitySpec, null, 2), defaultOptionsJSON: JSON.stringify(defaultOptions, null, 2), routesJSON: JSON.stringify(routes.map(({ channelModelId, enabled, priority, weight }) => ({ channelModelId, enabled, priority, weight })), null, 2) });
    }, [detail.data, detail.isFetchedAfterMount, form]);
    const save = () => mutation.submit(async () => {
        const { capabilitySpecJSON, defaultOptionsJSON, routesJSON, ...values } = await form.validateFields();
        if (model && !revision.current) throw new Error("模型版本尚未读取完成");
        await publishPlatformLogicalModel(model?.id, {
            ...values, icon: values.icon || "", description: values.description || "", expectedRevisionId: revision.current,
            capabilitySpec: parseJSONField(capabilitySpecJSON, "object", "模型能力"),
            defaultOptions: parseJSONField(defaultOptionsJSON, "object", "默认选项"),
            routes: parseJSONField(routesJSON, "array", "路由"),
        });
    }, onComplete);
    return (
        <AppModal open scrollBody width={720} title={model ? "发布模型新版本" : "发布平台模型"} onCancel={mutation.busy ? undefined : onClose} onOk={() => void save()} confirmLoading={mutation.busy} okButtonProps={{ disabled: Boolean(model && (!detail.data || !detail.isFetchedAfterMount || detail.isError)) }} okText="发布" cancelText="取消" closable={!mutation.busy} maskClosable={!mutation.busy}>
            <div className="platform-admin-dialog">
                {mutation.error ? <p className="platform-admin-error" role="alert">{mutation.error}</p> : null}
                {model && detail.isLoading ? <WorkspaceLoadingState label="正在读取模型版本" rows={0} /> : null}
                {detail.isError ? <WorkspaceErrorState compact title="模型加载失败" description={detail.error.message} onRetry={() => void detail.refetch()} /> : null}
                <Form form={form} layout="vertical" disabled={mutation.busy || Boolean(model && (!detail.data || !detail.isFetchedAfterMount))} initialValues={{ enabled: true, sortOrder: 0, defaultOptionsJSON: "{}", routesJSON: "[]" }}>
                    <Form.Item name="code" label="稳定模型标识" rules={[{ required: true, message: "请输入稳定模型标识" }]} extra="3—80 位小写字母、数字及 ._-，以字母开头；创建后不可修改。"><Input disabled={Boolean(model)} maxLength={80} /></Form.Item>
                    <Form.Item name="name" label="模型名称" rules={[{ required: true, whitespace: true, message: "请输入模型名称" }]}><Input maxLength={120} /></Form.Item>
                    <Form.Item name="description" label="描述"><Input.TextArea rows={2} maxLength={500} /></Form.Item>
                    <div className="platform-admin-field-row">
                        <Form.Item name="icon" label="图标标识"><Input maxLength={80} /></Form.Item>
                        <Form.Item name="sortOrder" label="排序" rules={[{ required: true, type: "integer", message: "请输入整数排序" }]}><InputNumber className="platform-admin-number" min={-100000} max={100000} /></Form.Item>
                    </div>
                    <Form.Item name="enabled" label="启用模型" valuePropName="checked"><Switch /></Form.Item>
                    <Form.Item name="capabilitySpecJSON" label="模型能力 JSON" rules={[{ required: true, message: "请输入模型能力" }]} extra="必须明确 version、capability 和 operations；能力范围由服务端按所选渠道校验。"><Input.TextArea rows={6} placeholder='{"version":1,"capability":"text","operations":["text_generation"]}' /></Form.Item>
                    <Form.Item name="defaultOptionsJSON" label="默认选项 JSON"><Input.TextArea rows={4} /></Form.Item>
                    <Form.Item name="routesJSON" label="路由 JSON" rules={[{ required: true, message: "请配置路由" }]} extra='至少一条启用路由：[{"channelModelId":"渠道模型 ID","enabled":true,"priority":0,"weight":1}]'><Input.TextArea rows={5} /></Form.Item>
                </Form>
            </div>
        </AppModal>
    );
}
