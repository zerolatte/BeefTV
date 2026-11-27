import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { Alert, App, Button, Dropdown, Form, Input, Table } from "antd";
import { MoreHorizontal, ReceiptText } from "lucide-react";
import { EmptyState } from "@/components/ui/product/empty-state/empty-state";
import { PageHeader, PaginationBar } from "@/components/layout/workspace-page";
import { AppModal } from "@/components/ui/product/app-modal/app-modal";
import { changePlatformPassword, getCredits, getCreditLedger, platformLogout } from "@/services/api/platform";
import "./platform.css";

const kinds: Record<string, string> = { adjust: "管理员调整", reserve: "冻结", charge: "结算", refund: "退回" };
export default function CreditsPage() {
    const [page, setPage] = useState(1);
    const [passwordOpen, setPasswordOpen] = useState(false);
    const [saving, setSaving] = useState(false);
    const [form] = Form.useForm();
    const { message } = App.useApp();
    const account = useQuery({ queryKey: ["platform-credits"], queryFn: ({ signal }) => getCredits({ signal }), refetchInterval: 10000 });
    const ledger = useQuery({ queryKey: ["platform-ledger", page], queryFn: () => getCreditLedger(page), refetchInterval: 10000 });
    async function savePassword() {
        if (saving) return;
        try { const value = await form.validateFields(); setSaving(true); await changePlatformPassword(value.oldPassword, value.newPassword); window.location.reload(); }
        catch (error) { if (error instanceof Error) void message.error(error.message); }
        finally { setSaving(false); }
    }
    return <main className="platform-page">
        <PageHeader title="我的积分" actions={<Dropdown menu={{ items: [{ key: "password", label: "修改密码", onClick: () => setPasswordOpen(true) }, { key: "logout", label: "退出登录", onClick: () => { void platformLogout().catch((error: Error) => message.error(error.message)); } }] }}><Button aria-label="账号操作" icon={<MoreHorizontal size={16} />} /></Dropdown>} />
        <div className="platform-content">
            {(account.error || ledger.error) && <Alert type="error" showIcon title={(account.error || ledger.error)?.message} action={<Button onClick={() => { void account.refetch(); void ledger.refetch(); }}>重试</Button>} />}
            <div className="platform-balance-grid"><section className="platform-balance-card"><p>可用积分</p><strong>{account.data?.available ?? "—"}</strong></section><section className="platform-balance-card"><p>冻结积分</p><strong>{account.data?.frozen ?? "—"}</strong></section></div>
            <p className="platform-note">积分长期有效，由管理员发放或收回。生成时先冻结，按有效产出结算；状态不明的任务会等待核对。冻结、结算和退回是不同账务步骤，请以当前余额为准。</p>
            <h2 className="platform-section-title">积分流水</h2>
            <Table className="platform-table" rowKey="id" loading={ledger.isPending} dataSource={ledger.data?.items} pagination={false} scroll={{ x: 640 }} columns={[
                { title: "时间", dataIndex: "createdAt", render: (value: string) => new Date(value).toLocaleString() },
                { title: "类型", dataIndex: "kind", render: (value: string) => kinds[value] || value },
                { title: "积分", dataIndex: "amount", align: "right", className: "platform-number" }, { title: "说明", dataIndex: "reason" },
            ]} locale={{ emptyText: <EmptyState className="platform-empty" icon={ReceiptText} title={ledger.error ? "流水读取失败" : "暂无积分流水"} description={ledger.error ? "请重试加载积分流水" : "积分发放、冻结与结算记录会显示在这里"} /> }} />
        </div>
        <PaginationBar current={page} pageSize={30} pageSizeOptions={[30]} total={ledger.data?.total ?? 0} onChange={setPage} />
        <AppModal flush scrollBody className="platform-dialog" open={passwordOpen} title="修改密码" onCancel={() => { if (!saving) setPasswordOpen(false); }} onOk={() => void savePassword()} confirmLoading={saving} destroyOnHidden>
            <Form form={form} layout="vertical" className="platform-form" preserve={false}>
                <Form.Item name="oldPassword" label="当前密码" rules={[{ required: true }]}><Input.Password autoComplete="current-password" /></Form.Item>
                <Form.Item name="newPassword" label="新密码" rules={[{ required: true, min: 12, max: 512 }]}><Input.Password autoComplete="new-password" /></Form.Item>
                <Form.Item name="confirm" label="确认新密码" dependencies={["newPassword"]} rules={[{ required: true }, ({ getFieldValue }) => ({ validator: (_, value) => value === getFieldValue("newPassword") ? Promise.resolve() : Promise.reject(new Error("两次密码不一致")) })]}><Input.Password autoComplete="new-password" /></Form.Item>
            </Form>
        </AppModal>
    </main>;
}
