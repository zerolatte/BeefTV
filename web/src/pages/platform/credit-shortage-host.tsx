import { useEffect, useState } from "react";
import { Button } from "antd";
import { AppModal } from "@/components/ui/product/app-modal/app-modal";
import { dismissPlatformCreditShortage, subscribePlatformCreditShortages, type PlatformCreditShortage } from "@/services/platform-credit-shortage";

export function PlatformCreditShortageHost({ navigate }: { navigate: (path: string) => void }) {
    const [current, setCurrent] = useState<PlatformCreditShortage>();
    useEffect(() => subscribePlatformCreditShortages(setCurrent), []);
    const available = current?.availableCredits;
    const shortage = available === undefined || !current ? undefined : Math.max(0, current.requiredCredits - available);

    return (
        <AppModal
            flush
            open={Boolean(current)}
            title={null}
            footer={null}
            closable={false}
            width={480}
            aria-labelledby="platform-credit-shortage-title"
            onCancel={dismissPlatformCreditShortage}
        >
            <div className="p-6 text-[var(--user-ink)]">
                <h2 id="platform-credit-shortage-title" className="mb-4 text-base font-medium leading-[22px]">积分不足，暂时无法生成</h2>
                <div className="space-y-3 text-sm leading-6">
                    <p>{current && current.requestCount > 1 ? `本次未提交的 ${current.requestCount} 项生成共需要` : "本次生成需要"} <strong>{current?.requiredCredits} 积分</strong>。</p>
                    {current?.balanceState === "loading" ? <p role="status">正在读取可用积分…</p> : available === undefined ? (
                        <p>当前可用积分暂时无法读取，请前往“我的积分”查看。</p>
                    ) : (
                        <p>
                            当前可用 <strong>{available} 积分</strong>
                            {shortage ? <>，还差 <strong>{shortage} 积分</strong></> : null}。
                        </p>
                    )}
                    {shortage === 0 ? <p>当前余额已更新，请返回创作后重新点击生成。</p> : <p>请联系管理员补充积分后，再重新点击生成。</p>}
                </div>
                <div className="mt-6 flex flex-wrap justify-end gap-3">
                    <Button onClick={dismissPlatformCreditShortage}>返回创作</Button>
                    <Button type="primary" onClick={() => {
                        dismissPlatformCreditShortage();
                        navigate("/credits");
                    }}>查看我的积分</Button>
                </div>
            </div>
        </AppModal>
    );
}
