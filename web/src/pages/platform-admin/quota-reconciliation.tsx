import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { Alert, Button } from "antd";

import { WorkspaceErrorState } from "@/components/layout/workspace-state";
import { reconcilePlatformAccountQuota } from "@/services/api/platform-admin";
import { usePlatformAdminKey } from "./catalog-form";

export function PlatformQuotaReconciliation({ accountId, disabled }: { accountId: string; disabled: boolean }) {
    const key = usePlatformAdminKey();
    const [requested, setRequested] = useState(false);
    const query = useQuery({ queryKey: [...key, "reconciliation", accountId], queryFn: ({ signal }) => reconcilePlatformAccountQuota(accountId, signal), enabled: requested, refetchOnWindowFocus: false });
    const result = requested && query.isFetchedAfterMount && !query.isError ? query.data?.reconciliation : undefined;
    return <div className="platform-admin-reconciliation">
        <Button disabled={disabled} loading={query.isFetching} onClick={() => requested ? void query.refetch() : setRequested(true)}>核对点数账本</Button>
        {requested && query.isError ? <WorkspaceErrorState compact title="账本核对失败" description={query.error.message} onRetry={() => void query.refetch()} /> : null}
        {result ? <>
            <Alert showIcon type={result.consistent ? "success" : "warning"} message={result.consistent ? "账本与额度账户一致" : "账本与额度账户不一致"} />
            <dl className="platform-admin-quota">
                <div><dt>流水条数</dt><dd>{result.ledgerEntries.toLocaleString()}</dd></div>
                <div><dt>发放与调整净额</dt><dd>{result.granted}</dd></div>
                <div><dt>已结算净额</dt><dd>{result.settled}</dd></div>
                <div><dt>未结算预留</dt><dd>{result.reserved}</dd></div>
            </dl>
        </> : null}
    </div>;
}
