import { Checkbox, Form, Input, Radio } from "antd";

import { AppModal } from "@/components/ui/product/app-modal";
import { reviewPlatformUsage, type PlatformReviewFact, type PlatformReviewInput, type PlatformUsage } from "@/services/api/platform-admin";
import { useAdminRequestIntent, useCatalogSubmit } from "./catalog-form";

const factOptions: Array<{ value: PlatformReviewFact; label: string }> = [
    { value: "succeeded", label: "至少一份成功结果（结算固定点数）" },
    { value: "not_accepted", label: "确认上游未受理（释放预留）" },
    { value: "failed_no_cost", label: "完全失败且确认未消耗（释放预留）" },
    { value: "failed_consumed", label: "完全失败但上游已消耗（结算并补偿）" },
];

export function PlatformExecutionReviewDialog({ usage, onClose, onComplete }: { usage: PlatformUsage; onClose: () => void; onComplete: () => void }) {
    const [form] = Form.useForm<{ fact: PlatformReviewFact; reason: string; verified: boolean }>();
    const mutation = useCatalogSubmit();
    const requestIntent = useAdminRequestIntent<Omit<PlatformReviewInput, "clientOperationId">>();
    const save = () => mutation.submit(async () => {
        const fields = await form.validateFields();
        await reviewPlatformUsage(usage.id, requestIntent({ expectedState: usage.state, fact: fields.fact, reason: fields.reason.trim() }));
    }, onComplete);
    return <AppModal open scrollBody title="人工核对执行结果" onCancel={mutation.busy ? undefined : onClose} onOk={() => void save()} confirmLoading={mutation.busy} okText="确认核对" cancelText="取消" closable={!mutation.busy} maskClosable={!mutation.busy}>
        <div className="platform-admin-dialog">
            <dl className="platform-admin-quota">
                <div><dt>账号</dt><dd>{usage.accountId}</dd></div>
                <div><dt>任务</dt><dd>{usage.taskId}</dd></div>
                <div><dt>预留点数</dt><dd>{usage.points.toLocaleString()}</dd></div>
            </dl>
            {mutation.error ? <p role="alert" className="platform-admin-error">{mutation.error}</p> : null}
            <Form form={form} layout="vertical" disabled={mutation.busy}>
                <Form.Item name="fact" label="已核实的终局事实" rules={[{ required: true, message: "请选择已核实的终局事实" }]}><Radio.Group className="platform-admin-review-facts">{factOptions.map((option) => <Radio key={option.value} value={option.value}>{option.label}</Radio>)}</Radio.Group></Form.Item>
                <Form.Item name="reason" label="核对依据" rules={[{ required: true, validator: (_, value) => typeof value === "string" && value.trim() && Array.from(value.trim()).length <= 500 ? Promise.resolve() : Promise.reject(new Error("请填写 1—500 字的核对依据")) }]} extra="说明核对来源和终局结论，不包含提示词或凭据。"><Input.TextArea rows={4} /></Form.Item>
                <Form.Item name="verified" valuePropName="checked" rules={[{ validator: (_, value) => value === true ? Promise.resolve() : Promise.reject(new Error("请先确认已核实终局依据")) }]}><Checkbox>已核实终局依据，确认按上述事实处理点数</Checkbox></Form.Item>
            </Form>
        </div>
    </AppModal>;
}
