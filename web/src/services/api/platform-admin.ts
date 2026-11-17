import { http } from "@/services/api/request";
import type { PlatformAccount } from "@/services/api/platform-auth";

export type PlatformAccountInput = {
    username: string;
    displayName: string;
    password: string;
    role: "admin" | "user";
    initialPoints: number;
    storageLimitBytes: number;
};

export type PlatformAccountPatch = Partial<Pick<PlatformAccount, "displayName" | "role">> & { status?: "active" | "disabled" };
export type PlatformAccountQuota = {
    accountId: string;
    points: { granted: number; settled: number; reserved: number; available: number };
    storage: { limitBytes: number; usedBytes: number; reservedBytes: number };
};

export type PlatformAudit = {
    id: string;
    actorAccountId: string;
    targetAccountId: string;
    action: string;
    difference: string;
    createdAt: string;
};

export type PlatformPointAdjustmentInput = { clientOperationId: string; points: number; reason: string };
export type PlatformPointAdjustment = { id: string; actorAccountId: string; accountId: string; points: number; reason: string; createdAt: string };
export type PlatformUsage = {
    id: string;
    accountId: string;
    taskId: string;
    modelId: string;
    priceVersionId: string;
    operation: string;
    points: number;
    state: "reserved" | "submitting" | "accepted" | "unknown" | "settled" | "released" | "compensated";
    reviewRequired: boolean;
    submittedAt?: string | null;
    finalizedAt?: string | null;
    createdAt: string;
    updatedAt: string;
};

export type PlatformReviewFact = "succeeded" | "not_accepted" | "failed_no_cost" | "failed_consumed";
export type PlatformReviewInput = { clientOperationId: string; expectedState: PlatformUsage["state"]; fact: PlatformReviewFact; reason: string };
export type PlatformExecutionReview = { id: string; intentId: string; actorAccountId: string; previousState: string; fact: PlatformReviewFact; reason: string; createdAt: string };
export type PlatformQuotaReconciliation = { accountId: string; consistent: boolean; ledgerEntries: number; granted: string; settled: string; reserved: string };

export function listPlatformAccounts(page: number, pageSize: number, signal?: AbortSignal) {
    return http.get<{ users: PlatformAccount[]; total: number; page: number; pageSize: number }>("/admin/users", { params: { page, pageSize }, signal });
}

export function createPlatformAccount(input: PlatformAccountInput) {
    return http.post<{ user: PlatformAccount }>("/admin/users", input, { timeout: 30_000 });
}

export function updatePlatformAccount(accountId: string, input: PlatformAccountPatch) {
    return http.patch<{ user: PlatformAccount }>(`/admin/users/${encodeURIComponent(accountId)}`, input);
}

export function resetPlatformAccountPassword(accountId: string, password: string) {
    return http.post<{ reset: true }>(`/admin/users/${encodeURIComponent(accountId)}/reset-password`, { password }, { timeout: 30_000 });
}

export function deletePlatformAccount(accountId: string) {
    return http.delete<{ id: string; status: "deleted" }>(`/admin/users/${encodeURIComponent(accountId)}`);
}

export function getPlatformAccountQuota(accountId: string, signal?: AbortSignal) {
    return http.get<PlatformAccountQuota>(`/admin/users/${encodeURIComponent(accountId)}/quota`, { signal });
}

export function updatePlatformAccountStorageQuota(accountId: string, limitBytes: number) {
    return http.put<PlatformAccountQuota>(`/admin/users/${encodeURIComponent(accountId)}/storage-quota`, { limitBytes });
}

export function listPlatformAuditLogs(page: number, pageSize: number, accountId?: string, signal?: AbortSignal) {
    return http.get<{ logs: PlatformAudit[]; total: number; page: number; pageSize: number }>("/admin/audit-logs", { params: { page, pageSize, ...(accountId ? { accountId } : {}) }, signal });
}

export function adjustPlatformAccountPoints(accountId: string, input: PlatformPointAdjustmentInput) {
    return http.post<{ adjustment: PlatformPointAdjustment }>(`/admin/users/${encodeURIComponent(accountId)}/point-adjustments`, input);
}

export function listPlatformUsage(page: number, pageSize: number, accountId?: string, signal?: AbortSignal) {
    return http.get<{ items: PlatformUsage[]; total: number; page: number; pageSize: number }>("/admin/usage", { params: { page, pageSize, ...(accountId ? { accountId } : {}) }, signal });
}

export function reviewPlatformUsage(intentId: string, input: PlatformReviewInput) {
    return http.post<{ review: PlatformExecutionReview }>(`/admin/usage/${encodeURIComponent(intentId)}/review`, input);
}

export function reconcilePlatformAccountQuota(accountId: string, signal?: AbortSignal) {
    return http.get<{ reconciliation: PlatformQuotaReconciliation }>(`/admin/users/${encodeURIComponent(accountId)}/quota-reconciliation`, { signal });
}
