import { useQuery } from "@tanstack/react-query";
import { Form, InputNumber, Select, Table } from "antd";

import { AppModal } from "@/components/ui/product/app-modal";
import { WorkspaceErrorState, WorkspaceState } from "@/components/layout/workspace-state";
import { getPlatformLogicalModel, listPlatformModelPrices, publishPlatformModelPrice, type PlatformLogicalModel } from "@/services/api/platform-catalog";
import { useCatalogSubmit, usePlatformAdminKey } from "./catalog-form";

export function PlatformModelPrices({ model, onClose, onComplete }: { model: PlatformLogicalModel; onClose: () => void; onComplete: () => void }) {
    const key = usePlatformAdminKey();
    const detail = useQuery({ queryKey: [...key, "model", model.id], queryFn: ({ signal }) => getPlatformLogicalModel(model.id, signal) });
    const history = useQuery({ queryKey: [...key, "prices", model.id], queryFn: ({ signal }) => listPlatformModelPrices(model.id, signal) });
    const [form] = Form.useForm<{ operation: string; points: number }>();
    const mutation = useCatalogSubmit();
    const save = () => mutation.submit(async () => {
        const value = await form.validateFields();
        await publishPlatformModelPrice(model.id, value.operation, value.points);
    }, () => { form.resetFields(); void history.refetch(); onComplete(); });
    const failure = detail.error || history.error;
    return (
        <AppModal open scrollBody width={800} title={`固定点数价格：${model.name}`} onCancel={mutation.busy ? undefined : onClose} onOk={() => void save()} confirmLoading={mutation.busy} okText="发布新价格" cancelText="关闭" okButtonProps={{ disabled: !detail.data || history.isError }} closable={!mutation.busy} maskClosable={!mutation.busy}>
            <div className="platform-admin-dialog">
                {mutation.error ? <p role="alert" className="platform-admin-error">{mutation.error}</p> : null}
                {failure ? <WorkspaceErrorState compact title="价格加载失败" description={failure.message} onRetry={() => { void detail.refetch(); void history.refetch(); }} /> : null}
                <Form form={form} layout="vertical" disabled={mutation.busy}>
                    <Form.Item name="operation" label="模型操作" rules={[{ required: true, message: "请选择操作" }]}><Select loading={detail.isLoading} options={detail.data?.capabilitySpec.operations?.map((operation) => ({ value: operation, label: operation }))} /></Form.Item>
                    <Form.Item name="points" label="固定点数" rules={[{ required: true, validator: (_, value) => Number.isSafeInteger(value) && value > 0 ? Promise.resolve() : Promise.reject(new Error("请输入 1—9,007,199,254,740,991 的整数")) }]} extra="新价格只影响后续任务；历史价格保留。"><InputNumber className="platform-admin-number" controls={false} /></Form.Item>
                </Form>
                <Table rowKey="id" loading={history.isLoading} pagination={false} dataSource={history.data?.prices || []} scroll={{ x: 560 }} locale={{ emptyText: <WorkspaceState compact title="尚未发布价格" /> }} columns={[
                    { title: "操作", dataIndex: "operation", key: "operation" },
                    { title: "版本", dataIndex: "version", key: "version" },
                    { title: "点数", dataIndex: "points", key: "points" },
                    { title: "发布时间", dataIndex: "createdAt", key: "createdAt", render: (value: string) => new Date(value).toLocaleString() },
                ]} />
            </div>
        </AppModal>
    );
}
