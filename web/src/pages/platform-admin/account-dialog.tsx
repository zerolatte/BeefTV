import { useRef, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { Form, Input, InputNumber, Select } from "antd";

import { AppModal } from "@/components/ui/product/app-modal";
import { WorkspaceErrorState, WorkspaceLoadingState } from "@/components/layout/workspace-state";
import type { PlatformAccount } from "@/services/api/platform-auth";
import { createPlatformAccount, deletePlatformAccount, getPlatformAccountQuota, resetPlatformAccountPassword, updatePlatformAccount, updatePlatformAccountStorageQuota, type PlatformAccountInput } from "@/services/api/platform-admin";
import { captureWorkspaceSession, invalidateWorkspaceSession, isWorkspaceSessionCurrent } from "@/services/workspace-session";
import { useUserStore } from "@/stores/use-user-store";
import { PlatformQuotaReconciliation } from "./quota-reconciliation";

export type AccountAction = { kind: "create" } | { kind: "edit" | "quota" | "password" | "status" | "delete"; account: PlatformAccount };
type FormValues = PlatformAccountInput & { limitBytes: number };
const roleOptions = [{ value: "user", label: "普通用户" }, { value: "admin", label: "管理员" }];
const titles = { create: "创建账号", edit: "编辑账号", quota: "账号额度", password: "重置密码", status: "调整账号状态", delete: "注销账号" };
const integerRule = { required: true, validator: (_: unknown, value: unknown) => typeof value === "number" && Number.isSafeInteger(value) && value >= 0 ? Promise.resolve() : Promise.reject(new Error("请输入不小于 0 的安全整数")) };
const passwordRule = { required: true, validator: (_: unknown, value: unknown) => typeof value === "string" && Array.from(value).length >= 12 && Array.from(value).length <= 128 ? Promise.resolve() : Promise.reject(new Error("密码长度须为 12—128 个字符")) };

export function PlatformAccountDialog({ action, onClose, onComplete }: { action: AccountAction; onClose: () => void; onComplete: () => void }) {
    const [form] = Form.useForm<FormValues>();
    const [busy, setBusy] = useState(false);
    const saving = useRef(false);
    const [error, setError] = useState<string>();
    const account = action.kind === "create" ? undefined : action.account;
    const actor = useUserStore((state) => state.user);
    const instanceId = useUserStore((state) => state.platformInstanceId);
    const quota = useQuery({ queryKey: ["platform-admin", instanceId, actor?.id, "quota", account?.id], queryFn: ({ signal }) => getPlatformAccountQuota(account!.id, signal), enabled: action.kind === "quota", refetchOnWindowFocus: false });
    const hasForm = ["create", "edit", "password", "quota"].includes(action.kind);

    const save = async () => {
        if (saving.current) return;
        saving.current = true;
        let values: FormValues;
        try { values = hasForm ? await form.validateFields() : {} as FormValues; } catch { saving.current = false; return; }
        const session = captureWorkspaceSession();
        setBusy(true);
        setError(undefined);
        try {
            if (action.kind === "create") await createPlatformAccount({ username: values.username, displayName: values.displayName, password: values.password, role: values.role, initialPoints: values.initialPoints, storageLimitBytes: values.storageLimitBytes });
            else if (action.kind === "edit") await updatePlatformAccount(action.account.id, { displayName: values.displayName, role: values.role });
            else if (action.kind === "quota") await updatePlatformAccountStorageQuota(action.account.id, values.limitBytes);
            else if (action.kind === "password") await resetPlatformAccountPassword(action.account.id, values.password);
            else if (action.kind === "status") await updatePlatformAccount(action.account.id, { status: action.account.status === "active" ? "disabled" : "active" });
            else await deletePlatformAccount(action.account.id);
            if (!isWorkspaceSessionCurrent(session)) return;
            onComplete();
            if (account && account.id === actor?.id && (action.kind === "password" || (action.kind === "edit" && values.role !== account.role))) invalidateWorkspaceSession("account_access_changed");
        } catch (failure) {
            if (isWorkspaceSessionCurrent(session)) setError(failure instanceof Error ? failure.message : "操作失败，请重试。");
        } finally {
            saving.current = false;
            setBusy(false);
        }
    };

    return (
        <AppModal open scrollBody title={titles[action.kind]} onCancel={busy ? undefined : onClose} onOk={() => void save()} confirmLoading={busy} okText={action.kind === "create" ? "创建账号" : action.kind === "delete" ? "确认注销" : "保存"} cancelText="取消" closable={!busy} maskClosable={!busy} okButtonProps={{ danger: action.kind === "delete" || (action.kind === "status" && account?.status === "active"), disabled: action.kind === "quota" && (!quota.data || !quota.isFetchedAfterMount || quota.isError) }}>
            <div className="platform-admin-dialog">
                {account ? <p className="platform-admin-account-label">{account.displayName}（{account.username}）</p> : null}
                {error ? <p role="alert" className="platform-admin-error">{error}</p> : null}
                {action.kind === "quota" && quota.isLoading ? <WorkspaceLoadingState label="正在读取额度" rows={0} /> : null}
                {action.kind === "quota" && quota.isError ? <WorkspaceErrorState compact title="额度加载失败" description={quota.error.message} onRetry={() => void quota.refetch()} /> : null}
                <Form form={form} layout="vertical" initialValues={{ role: account?.role || "user", displayName: account?.displayName }} disabled={busy}>
                    {action.kind === "create" ? <Form.Item name="username" label="用户名" rules={[{ required: true, message: "请输入用户名" }]} extra="3—32 位，以字母开头，可包含字母、数字、点、下划线和短横线。创建后不可修改。"><Input autoComplete="off" maxLength={32} /></Form.Item> : null}
                    {action.kind === "create" || action.kind === "edit" ? <>
                        <Form.Item name="displayName" label="显示名" rules={[{ required: true, whitespace: true, message: "请输入显示名" }]}><Input maxLength={80} /></Form.Item>
                        <Form.Item name="role" label="角色" rules={[{ required: true }]}><Select options={roleOptions} /></Form.Item>
                    </> : null}
                    {action.kind === "create" || action.kind === "password" ? <Form.Item name="password" label={action.kind === "create" ? "初始密码" : "新密码"} rules={[passwordRule]} extra="用户下次登录必须修改此密码。"><Input.Password autoComplete="new-password" /></Form.Item> : null}
                    {action.kind === "create" ? <>
                        <Form.Item name="initialPoints" label="初始 AI 点数" rules={[integerRule]}><InputNumber controls={false} className="platform-admin-number" /></Form.Item>
                        <Form.Item name="storageLimitBytes" label="存储上限（字节）" rules={[integerRule]} extra="必须明确填写；0 表示不允许新增存储。1 GiB = 1,073,741,824 字节。"><InputNumber controls={false} className="platform-admin-number" /></Form.Item>
                    </> : null}
                    {action.kind === "quota" && quota.data && quota.isFetchedAfterMount ? <>
                        <dl className="platform-admin-quota">
                            <div><dt>可用 AI 点数</dt><dd>{quota.data.points.available.toLocaleString()}</dd></div>
                            <div><dt>已结算 / 预留点数</dt><dd>{quota.data.points.settled.toLocaleString()} / {quota.data.points.reserved.toLocaleString()}</dd></div>
                            <div><dt>已用 / 预留存储（字节）</dt><dd>{quota.data.storage.usedBytes.toLocaleString()} / {quota.data.storage.reservedBytes.toLocaleString()}</dd></div>
                        </dl>
                        <Form.Item name="limitBytes" label="存储上限（字节）" initialValue={quota.data.storage.limitBytes} rules={[integerRule]} extra="调低上限不会删除已有数据；空间不足时会拒绝新增存储。"><InputNumber controls={false} className="platform-admin-number" /></Form.Item>
                    </> : null}
                </Form>
                {action.kind === "quota" && account ? <PlatformQuotaReconciliation accountId={account.id} disabled={busy} /> : null}
                {action.kind === "status" ? <p>{account?.status === "active" ? "禁用后，用户现有会话立即失效，且不能重新登录。" : "启用后，用户可以重新登录。"}</p> : null}
                {action.kind === "delete" ? <p>注销后该账号不能再登录。用户名与工作区仍被保留，不会分配给其他用户。</p> : null}
            </div>
        </AppModal>
    );
}
