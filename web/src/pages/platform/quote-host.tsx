import { useEffect, useState } from "react";
import { AppModal } from "@/components/ui/product/app-modal/app-modal";
import { subscribePlatformQuotes } from "@/services/platform-quote";
import type { CreditQuote } from "@/services/api/platform";

type Pending = { quote: CreditQuote; resolve: (confirmed: boolean) => void };
export function PlatformQuoteHost() {
    const [queue, setQueue] = useState<Pending[]>([]);
    useEffect(() => subscribePlatformQuotes(setQueue), []);
    const current = queue[0];
    const settle = (confirmed: boolean) => { current?.resolve(confirmed); };
    return <AppModal flush open={Boolean(current)} title="确认生成积分" okText="确认并生成" cancelText="取消" onOk={() => settle(true)} onCancel={() => settle(false)}>
        <div className="p-6 space-y-3">
            <p>本次生成 {current?.quote.units} 个计费单位，单价 {current?.quote.unitCredits} 积分。</p>
            <p>提交时冻结 <strong>{current?.quote.totalCredits} 积分</strong>，按有效产出结算。确认无产出退回积分，状态不明时保留冻结等待核对。</p>
            <p>报价有效至 {current && new Date(current.quote.expiresAt).toLocaleTimeString()}。</p>
        </div>
    </AppModal>;
}
