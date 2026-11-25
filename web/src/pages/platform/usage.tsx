import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { Alert, App, Button, Form, Input, InputNumber, Select, Table } from "antd";
import { AppModal } from "@/components/ui/product/app-modal/app-modal";
import { usePlatformMutation } from "./use-platform-mutation";
import { PaginationBar } from "@/components/layout/workspace-page";
import { EmptyState } from "@/components/ui/product/empty-state/empty-state";
import { StatusBadge } from "@/components/ui/base/badges/status-badge";
import { ListChecks, RefreshCw } from "lucide-react";
import { PlatformPanel } from "./platform-panel";
import { getCreditUsage, resolveCreditUsage, type CreditUsage } from "@/services/api/platform";

export function UsageAdmin() {
    const [page, setPage] = useState(1);
    const [state, setState] = useState("");
    const [selected, setSelected] = useState<CreditUsage>();
    const { busy, submit } = usePlatformMutation();
    const [form] = Form.useForm();
    const { message } = App.useApp();
    const usage = useQuery({ queryKey: ["platform-usage", page, state], queryFn: () => getCreditUsage(page, state), refetchInterval: 10000 });
    async function resolve() {
        if (!selected || busy) return;
        await submit(async () => {
            const value = await form.validateFields();
            await resolveCreditUsage(selected.id, value.units, value.reason);
        }, () => { setSelected(undefined); void usage.refetch(); void message.success("核对结算已完成"); });
    }
    return <>
        <PlatformPanel pagination={<PaginationBar current={page} pageSize={30} pageSizeOptions={[30]} total={usage.data?.total ?? 0} onChange={setPage} />}>
        <div className="platform-toolbar"><div className="platform-toolbar-main"><Select aria-label="用量状态" value={state} onChange={(value) => { setState(value); setPage(1); }} options={[{ value: "", label: "全部状态" }, { value: "reserved", label: "已冻结" }, { value: "review", label: "待核对" }, { value: "settled", label: "已结算" }]} /></div><Button icon={<RefreshCw size={16} />} onClick={() => void usage.refetch()}>刷新</Button></div>
        {usage.error && <Alert type="error" title={usage.error.message} />}
        <Table className="platform-table" rowKey="id" loading={usage.isPending} dataSource={usage.data?.items} pagination={false} scroll={{ x: 900 }} locale={{ emptyText: <EmptyState className="platform-empty" icon={ListChecks} title={usage.error ? "用量读取失败" : "暂无用量记录"} description={usage.error ? "请刷新重试" : "生成任务产生的积分用量会显示在这里"} /> }} columns={[
            { title: "用户标识", dataIndex: "userId", ellipsis: true, className: "platform-identifier" }, { title: "任务标识", dataIndex: "taskId", ellipsis: true, className: "platform-identifier" },
            { title: "预占", dataIndex: "reserved", align: "right", className: "platform-number" }, { title: "扣除", dataIndex: "charged", align: "right", className: "platform-number" }, { title: "退回", dataIndex: "refunded", align: "right", className: "platform-number" },
            { title: "状态", dataIndex: "state", render: (value: string) => <StatusBadge tone={value === "review" ? "warning" : value === "settled" ? "success" : "neutral"} label={({ reserved: "已冻结", review: "待核对", settled: "已结算" })[value] || value} /> },
            { title: "操作", render: (_, row: CreditUsage) => row.state === "review" ? <Button onClick={() => { form.resetFields(); setSelected(row); }}>核对</Button> : null },
        ]} />
        </PlatformPanel>
        <AppModal flush scrollBody className="platform-dialog" open={Boolean(selected)} title="核对冻结积分" confirmLoading={busy} onCancel={() => { if (!busy) setSelected(undefined); }} onOk={() => void resolve()} destroyOnHidden><Form form={form} layout="vertical" className="platform-form" preserve={false}>
            <p>请先向上游核实结果。按确认的有效产出数量扣除积分，剩余积分退回。此页不展示用户创作内容。</p>
            <Form.Item name="units" label="确认计费数量（0 表示确认无产出）" rules={[{ required: true }]}><InputNumber min={0} max={selected?.units} precision={0} /></Form.Item>
            <Form.Item name="reason" label="核对依据" rules={[{ required: true, max: 500 }]}><Input.TextArea /></Form.Item>
        </Form></AppModal>
    </>;
}
