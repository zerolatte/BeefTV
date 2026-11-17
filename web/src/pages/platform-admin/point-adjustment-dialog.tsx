import { useQuery } from "@tanstack/react-query";
import { Form, Input, InputNumber } from "antd";

import { AppModal } from "@/components/ui/product/app-modal";
import { WorkspaceErrorState, WorkspaceLoadingState } from "@/components/layout/workspace-state";
import type { PlatformAccount } from "@/services/api/platform-auth";
import { adjustPlatformAccountPoints, getPlatformAccountQuota, type PlatformPointAdjustmentInput } from "@/services/api/platform-admin";
import { useAdminRequestIntent, useCatalogSubmit, usePlatformAdminKey } from "./catalog-form";

type AdjustmentFields = Pick<PlatformPointAdjustmentInput, "points" | "reason">;

export function PlatformPointAdjustmentDialog({ account, onClose, onComplete }: { account: PlatformAccount; onClose: () => void; onComplete: () => void }) {
    const [form] = Form.useForm<AdjustmentFields>();
    const key = usePlatformAdminKey();
    const quota = useQuery({ queryKey: [...key, "quota", account.id], queryFn: ({ signal }) => getPlatformAccountQuota(account.id, signal), refetchOnWindowFocus: false });
    const requestIntent = useAdminRequestIntent<AdjustmentFields>();
    const mutation = useCatalogSubmit();
    const ready = quota.isFetchedAfterMount && Boolean(quota.data) && !quota.isError;

    const save = () => mutation.submit(async () => {
        const fields = await form.validateFields();
        if (!ready) throw new Error("额度读取尚未完成");
        const values = { points: fields.points, reason: fields.reason.trim() };
        await adjustPlatformAccountPoints(account.id, requestIntent(values));
    }, onComplete);

    return (
        <AppModal open scrollBody title="调整 AI 点数" onCancel={mutation.busy ? undefined : onClose} onOk={() => void save()} confirmLoading={mutation.busy} okText="确认调整" cancelText="取消" okButtonProps={{ disabled: !ready }} closable={!mutation.busy} maskClosable={!mutation.busy}>
            <div className="platform-admin-dialog">
                <p className="platform-admin-account-label">{account.displayName}（{account.username}）</p>
                {mutation.error ? <p role="alert" className="platform-admin-error">{mutation.error}</p> : null}
                {!quota.isFetchedAfterMount ? <WorkspaceLoadingState label="正在读取额度" rows={0} /> : null}
                {quota.isError ? <WorkspaceErrorState compact title="额度加载失败" description={quota.error.message} onRetry={() => void quota.refetch()} /> : null}
                {ready && quota.data ? <dl className="platform-admin-quota">
                    <div><dt>当前可用点数</dt><dd>{quota.data.points.available.toLocaleString()}</dd></div>
                    <div><dt>已结算 / 预留点数</dt><dd>{quota.data.points.settled.toLocaleString()} / {quota.data.points.reserved.toLocaleString()}</dd></div>
                </dl> : null}
                <Form form={form} layout="vertical" disabled={mutation.busy || !ready}>
                    <Form.Item name="points" label="调整点数" rules={[{ required: true, validator: (_, value) => Number.isSafeInteger(value) && value !== 0 ? Promise.resolve() : Promise.reject(new Error("请输入非零安全整数")) }]} extra="正数追加，负数扣减；扣减不能占用已结算或预留点数。"><InputNumber controls={false} className="platform-admin-number" /></Form.Item>
                    <Form.Item name="reason" label="调整原因" rules={[{ required: true, validator: (_, value) => typeof value === "string" && value.trim() && Array.from(value.trim()).length <= 500 ? Promise.resolve() : Promise.reject(new Error("请填写 1—500 字的调整原因")) }]}><Input.TextArea rows={3} /></Form.Item>
                </Form>
            </div>
        </AppModal>
    );
}
