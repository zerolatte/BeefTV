import { Button, Form, Input, InputNumber, Switch } from "antd";
import { Minus, Plus } from "lucide-react";

import { AppModal } from "@/components/ui/product/app-modal";
import { createPlatformChannel, rotatePlatformChannelCredential, updatePlatformChannel, type PlatformChannel, type PlatformChannelInput } from "@/services/api/platform-catalog";
import { useCatalogSubmit } from "./catalog-form";

export type ChannelAction = { kind: "create" } | { kind: "edit" | "credential"; channel: PlatformChannel };

export function PlatformChannelDialog({ action, onClose, onComplete }: { action: ChannelAction; onClose: () => void; onComplete: () => void }) {
    const [form] = Form.useForm<PlatformChannelInput>();
    const mutation = useCatalogSubmit();
    const channel = action.kind === "create" ? undefined : action.channel;
    const credentials = action.kind !== "edit";
    const save = () => mutation.submit(async () => {
        const values = await form.validateFields();
        const metadata = { name: values.name, publicAlias: values.publicAlias || "", sortOrder: values.sortOrder, concurrencyLimit: values.concurrencyLimit, enabled: values.enabled };
        if (action.kind === "create") await createPlatformChannel({ ...metadata, credentials: values.credentials });
        else if (action.kind === "credential") await rotatePlatformChannelCredential(action.channel.id, values.credentials);
        else await updatePlatformChannel(action.channel.id, metadata);
    }, onComplete);
    return (
        <AppModal open scrollBody title={action.kind === "create" ? "创建平台渠道" : action.kind === "edit" ? "编辑平台渠道" : "轮换渠道凭据"} onCancel={mutation.busy ? undefined : onClose} onOk={() => void save()} confirmLoading={mutation.busy} closable={!mutation.busy} maskClosable={!mutation.busy} okText={action.kind === "credential" ? "保存新版本" : "保存"} cancelText="取消">
            <div className="platform-admin-dialog">
                {channel ? <p className="platform-admin-account-label">{channel.name}</p> : null}
                {mutation.error ? <p role="alert" className="platform-admin-error">{mutation.error}</p> : null}
                <Form form={form} layout="vertical" initialValues={{ name: channel?.name, publicAlias: channel?.publicAlias, sortOrder: channel?.sortOrder ?? 0, concurrencyLimit: channel?.concurrencyLimit ?? 0, enabled: channel?.enabled ?? true }} disabled={mutation.busy}>
                    {action.kind !== "credential" ? <>
                        <Form.Item name="name" label="渠道名称" rules={[{ required: true, whitespace: true, message: "请输入渠道名称" }]}><Input maxLength={80} /></Form.Item>
                        <Form.Item name="publicAlias" label="公开别名"><Input maxLength={80} /></Form.Item>
                        <div className="platform-admin-field-row">
                            <Form.Item name="sortOrder" label="排序" rules={[{ required: true, type: "integer", message: "请输入整数排序" }]}><InputNumber className="platform-admin-number" min={-100000} max={100000} /></Form.Item>
                            <Form.Item name="concurrencyLimit" label="并发上限" rules={[{ required: true, type: "integer", message: "请输入 0—999 的整数" }]} extra="0 使用平台全局设置。"><InputNumber className="platform-admin-number" min={0} max={999} /></Form.Item>
                        </div>
                        <Form.Item name="enabled" label="启用渠道" valuePropName="checked"><Switch /></Form.Item>
                    </> : null}
                    {credentials ? <>
                        <Form.Item name={["credentials", "baseUrl"]} label="上游地址" rules={[{ required: true, message: "请输入上游地址" }]}><Input autoComplete="off" /></Form.Item>
                        <Form.Item name={["credentials", "apiKey"]} label="API Key" rules={[{ required: true, message: "请输入 API Key" }]}><Input.Password autoComplete="new-password" /></Form.Item>
                        <Form.Item name={["credentials", "secretKey"]} label="Secret Key"><Input.Password autoComplete="new-password" /></Form.Item>
                        <Form.List name={["credentials", "headers"]}>
                            {(fields, { add, remove }) => <>
                                {fields.map(({ key, name, ...rest }) => <div className="platform-admin-header-row" key={key}>
                                    <Form.Item {...rest} name={[name, "name"]} label="请求头名称" rules={[{ required: true, message: "请输入名称" }]}><Input autoComplete="off" /></Form.Item>
                                    <Form.Item {...rest} name={[name, "value"]} label="请求头值" rules={[{ required: true, message: "请输入值" }]}><Input.Password autoComplete="new-password" /></Form.Item>
                                    <Button aria-label="移除请求头" icon={<Minus className="size-4" />} onClick={() => remove(name)} />
                                </div>)}
                                <Button icon={<Plus className="size-4" />} onClick={() => add()} disabled={fields.length >= 32}>添加请求头</Button>
                            </>}
                        </Form.List>
                    </> : null}
                </Form>
            </div>
        </AppModal>
    );
}
