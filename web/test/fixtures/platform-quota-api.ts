const time = "2026-01-01T00:00:00Z";

export function quotaFixture() {
    const reads: string[] = [];
    const writes: Array<{ path: string; body: Record<string, unknown> }> = [];
    const adjustments = new Map<string, Record<string, unknown>>();
    const reviews = new Map<string, Record<string, unknown>>();
    let reconciliations = 0;
    let available = 23;
    const users = [
        { id: "admin-1", username: "manager", displayName: "平台管理员", workspaceId: "workspace-admin", role: "admin", status: "active", createdAt: time },
        { id: "user-1", username: "alice", displayName: "测试用户", workspaceId: "workspace-user", role: "user", status: "active", createdAt: time },
    ];
    const states = ["reserved", "submitting", "accepted", "unknown", "settled", "released", "compensated"];
    const items = Array.from({ length: 21 }, (_, index) => ({
        id: `intent-${index + 1}`, accountId: index < 20 ? "user-1" : "user-2", taskId: `task-${index + 1}`, modelId: "text-model", priceVersionId: "price-1", operation: "text_generation", points: 3,
        state: states[index % states.length], reviewRequired: index === 3, submittedAt: time, finalizedAt: null, createdAt: time, updatedAt: time,
        prompt: "不应显示的上游提示词", result: "不应显示的生成内容",
    }));
    const json = (data: unknown, status = 200, msg = "ok", reason?: string) => Response.json({ code: status === 200 ? 0 : status, data, msg, reason }, { status });
    async function handle(request: Request) {
        const url = new URL(request.url);
        const path = url.pathname;
        if (path === "/test/state") return json({ reads, writes, available, applied: adjustments.size, reviews: reviews.size });
        if (!path.startsWith("/api/")) return;
        if (request.headers.get("x-platform-session") !== "test-session") return json(null, 401, "会话缺失");
        if (request.method === "GET") reads.push(path + url.search);
        if (path === "/api/admin/users") return json({ users, total: users.length, page: 1, pageSize: 20 });
        if (path === "/api/admin/users/user-1/quota") return json({ accountId: "user-1", points: { granted: available + 7, available, settled: 5, reserved: 2 }, storage: { limitBytes: 1000, usedBytes: 100, reservedBytes: 0 } });
        if (path === "/api/admin/users/user-1/quota-reconciliation") {
            reconciliations += 1;
            return json({ reconciliation: { accountId: "user-1", consistent: reconciliations > 1, ledgerEntries: 5, granted: reconciliations === 1 ? "900719925474099312345" : "30", settled: "5", reserved: "2" } });
        }
        if (path === "/api/admin/usage/intent-4/review" && request.method === "POST") {
            if (request.headers.get("x-csrf-token") !== "test-csrf") return json(null, 403, "CSRF 缺失");
            const body = await request.json() as Record<string, unknown>;
            writes.push({ path, body });
            if (Object.keys(body).some((field) => !["clientOperationId", "expectedState", "fact", "reason"].includes(field))) return json(null, 400, "未知字段");
            const key = String(body.clientOperationId || "");
            const previous = reviews.get(key);
            if (previous) return json({ review: previous });
            if (body.reason === "核对状态冲突" || body.expectedState !== items[3].state) return json(null, 409, "执行状态已经变化，请刷新后核对", "settlement_conflict");
            const review = { id: "review-1", intentId: "intent-4", actorAccountId: "admin-1", previousState: body.expectedState, fact: body.fact, reason: body.reason, createdAt: time };
            reviews.set(key, review);
            items[3].state = body.fact === "succeeded" ? "settled" : body.fact === "failed_consumed" ? "compensated" : "released";
            items[3].reviewRequired = false;
            return json({ review });
        }
        if (path === "/api/admin/users/user-1/point-adjustments" && request.method === "POST") {
            if (request.headers.get("x-csrf-token") !== "test-csrf") return json(null, 403, "CSRF 缺失");
            const body = await request.json() as Record<string, unknown>;
            writes.push({ path, body });
            if (Object.keys(body).some((field) => !["clientOperationId", "points", "reason"].includes(field))) return json(null, 400, "未知字段");
            const key = String(body.clientOperationId || "");
            if (!key) return json(null, 400, "需要幂等标识", "idempotency_required");
            const existing = adjustments.get(key);
            if (existing) {
                if (existing.points !== body.points || existing.reason !== body.reason) return json(null, 409, "幂等内容冲突", "idempotency_conflict");
                return json({ adjustment: existing });
            }
            if (available + Number(body.points) < 0) return json(null, 409, "调整不能突破已结算与预留下限或整数上限", "quota_adjustment_conflict");
            const adjustment = { id: `adjustment-${adjustments.size + 1}`, actorAccountId: "admin-1", accountId: "user-1", points: body.points, reason: body.reason, createdAt: time };
            adjustments.set(key, adjustment);
            available += Number(body.points);
            return json({ adjustment });
        }
        if (path === "/api/admin/usage") {
            const page = Number(url.searchParams.get("page") || 1);
            const pageSize = Number(url.searchParams.get("pageSize") || 20);
            const accountId = url.searchParams.get("accountId");
            if (accountId === "unavailable") return json(null, 503, "用量服务暂不可用");
            const filtered = accountId ? items.filter((item) => item.accountId === accountId) : items;
            return json({ items: filtered.slice((page - 1) * pageSize, page * pageSize), total: filtered.length, page, pageSize });
        }
        return json(null, 404, "未注册测试接口");
    }
    return { handle };
}
