import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { Alert, App, Button, Form, Input, InputNumber, Table, Dropdown } from "antd";
import { AppModal } from "@/components/ui/product/app-modal/app-modal";
import { usePlatformMutation } from "./use-platform-mutation";
import { PaginationBar } from "@/components/layout/workspace-page";
import { EmptyState } from "@/components/ui/product/empty-state/empty-state";
import { StatusBadge } from "@/components/ui/base/badges/status-badge";
import { Users, Plus } from "lucide-react";
import { PlatformPanel } from "./platform-panel";
import { adjustCredits, createAccount, getAccounts, resetAccountPassword, updateAccount, type PlatformUser } from "@/services/api/platform";

type Action = { kind: "create" | "credits" | "reset" | "status"; user?: PlatformUser; operationId: string };
export function AccountAdmin() {
    const [page, setPage] = useState(1);
    const [q, setQ] = useState("");
    const [action, setAction] = useState<Action>();
    const [password, setPassword] = useState("");
    const { busy, submit } = usePlatformMutation();
    const [form] = Form.useForm();
    const { message } = App.useApp();
    const users = useQuery({ queryKey: ["platform-accounts", page, q], queryFn: () => getAccounts(page, q) });
    function open(kind: Action["kind"], user?: PlatformUser) { form.resetFields(); setAction({ kind, user, operationId: crypto.randomUUID() }); }
    async function save() {
        if (!action || busy) return;
        await submit(async () => {
            const values = await form.validateFields();
            if (action.kind === "create") return (await createAccount(values.username, values.displayName)).temporaryPassword;
            if (action.kind === "credits") await adjustCredits(action.user!.id, values.amount, values.reason, action.operationId);
            if (action.kind === "reset") return (await resetAccountPassword(action.user!.id)).temporaryPassword;
            if (action.kind === "status") await updateAccount(action.user!, action.user!.status === "active" ? "disabled" : "active");
        }, (temporaryPassword) => {
            if (temporaryPassword) setPassword(temporaryPassword);
            setAction(undefined); void users.refetch(); void message.success("操作已保存");
        });
    }
    return <>
        <PlatformPanel pagination={<PaginationBar current={page} pageSize={30} pageSizeOptions={[30]} total={users.data?.total ?? 0} onChange={setPage} />}>
        <div className="platform-toolbar"><div className="platform-toolbar-main"><Input.Search aria-label="搜索账号" placeholder="搜索用户名或显示名称" onSearch={(value) => { setPage(1); setQ(value); }} style={{ maxWidth: 320 }} /></div><Button type="primary" icon={<Plus size={16} />} onClick={() => open("create")}>开通账号</Button></div>
        {users.error && <Alert type="error" title={users.error.message} action={<Button onClick={() => void users.refetch()}>重试</Button>} />}
        <Table className="platform-table" rowKey="id" dataSource={users.data?.items} loading={users.isPending} pagination={false} scroll={{ x: 600 }} locale={{ emptyText: <EmptyState className="platform-empty" icon={Users} title={users.error ? "账号读取失败" : q ? "未找到匹配的账号" : "暂无账号"} description={users.error ? "请重试加载账号列表" : q ? "试试其他用户名或显示名称" : "开通账号后，用户即可进入创作工作区"} /> }} columns={[
            { title: "用户名", dataIndex: "username" }, { title: "显示名称", dataIndex: "displayName" },
            { title: "角色", dataIndex: "role", render: (role: string) => role === "admin" ? "管理员" : "普通用户" },
            { title: "状态", render: (_, user: PlatformUser) => <StatusBadge tone={user.status === "disabled" ? "neutral" : user.mustChangePassword ? "warning" : "success"} label={user.status === "disabled" ? "已停用" : user.mustChangePassword ? "待首次改密" : "正常"} /> },
            { title: "操作", render: (_, user: PlatformUser) => <Dropdown menu={{ items: [
                { key: "credits", label: "发放 / 收回积分", onClick: () => open("credits", user) },
                ...(user.role !== "admin" ? [{ key: "reset", label: "重置临时密码", onClick: () => open("reset", user) }, { key: "status", label: user.status === "active" ? "停用账号" : "启用账号", danger: user.status === "active", onClick: () => open("status", user) }] : []),
            ] }}><Button>管理</Button></Dropdown> },
        ]} />
        </PlatformPanel>
        <AppModal flush scrollBody className="platform-dialog" title={action?.kind === "create" ? "开通普通用户" : action?.kind === "credits" ? "调整积分" : action?.kind === "reset" ? "重置密码" : "更改账号状态"} open={Boolean(action)} onCancel={() => { if (!busy) setAction(undefined); }} onOk={() => void save()} confirmLoading={busy} destroyOnHidden>
            <Form form={form} layout="vertical" className="platform-form" preserve={false}>
                {action?.kind === "create" && <><Form.Item name="username" label="用户名" rules={[{ required: true, pattern: /^[a-zA-Z0-9][a-zA-Z0-9_.-]{2,63}$/, message: "请输入 3～64 位字母、数字、点、下划线或连字符" }]}><Input autoComplete="off" /></Form.Item><Form.Item name="displayName" label="显示名称" rules={[{ required: true, max: 80 }]}><Input /></Form.Item><p>新账号初始积分为 0，首次登录必须修改临时密码。</p></>}
                {action?.kind === "credits" && <><p>账号：{action.user?.username}。正数发放，负数收回，只能收回可用积分。</p><Form.Item name="amount" label="积分数量" rules={[{ required: true }]}><InputNumber precision={0} min={-1000000000000} max={1000000000000} /></Form.Item><Form.Item name="reason" label="调整原因" rules={[{ required: true, max: 500 }]}><Input.TextArea /></Form.Item></>}
                {action?.kind === "reset" && <p>重置 {action.user?.username} 的密码会撤销现有登录，用户下次登录必须改密。</p>}
                {action?.kind === "status" && <p>{action.user?.status === "active" ? "停用后立即撤销登录并取消尚未开始的任务。已提交上游的任务继续核对结算。" : "启用后，用户可用原密码重新登录。"}</p>}
            </Form>
        </AppModal>
        <AppModal flush scrollBody className="platform-dialog" open={Boolean(password)} title="临时密码" onCancel={() => setPassword("")} onOk={() => setPassword("")} cancelButtonProps={{ hidden: true }} okText="已保存" destroyOnHidden><div className="platform-form space-y-3"><p>请安全交给用户。关闭后无法再次查看，可以重新生成。</p><Input.TextArea readOnly value={password} autoSize /><Button onClick={() => { void navigator.clipboard.writeText(password).then(() => message.success("已复制"), () => message.error("复制失败，请手动复制")); }}>复制</Button></div></AppModal>
    </>;
}
