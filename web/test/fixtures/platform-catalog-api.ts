type Write = { path: string; method: string; body: Record<string, unknown> };
const time = "2026-01-01T00:00:00Z";
export function catalogFixture() {
    const writes: Write[] = [];
    const reads: string[] = [];
    const channel = { id: "channel-1", name: "测试渠道", publicAlias: "", sortOrder: 0, concurrencyLimit: 2, enabled: true, credentialVersion: 1, credentialVersionId: "credential-1", upstreamHost: "provider.invalid", hasApiKey: true, hasSecretKey: false, headerNames: [], createdAt: time, updatedAt: time };
    const models = Array.from({ length: 21 }, (_, index) => ({ id: `model-${index + 1}`, code: `model.${index + 1}`, name: `平台模型 ${index + 1}`, icon: "", description: "模型描述", capability: "text", enabled: true, sortOrder: index, activeRevisionId: `revision-${index + 1}`, createdAt: time, updatedAt: time }));
    let grants = ["model-21"];
    const prices = [{ id: "price-1", logicalModelId: "model-1", operation: "text_generation", version: 1, points: 3, createdAt: time }];
    const channelModels: Record<string, unknown>[] = [];
    const capabilitySpec = { version: 1, capability: "text", operations: ["text_generation"] };
    const json = (data: unknown, status = 200, msg = "ok", reason?: string) => Response.json({ code: status === 200 ? 0 : status, data, msg, reason }, { status });
    const users = [
        { id: "admin-1", username: "manager", displayName: "平台管理员", workspaceId: "workspace-admin", role: "admin", status: "active", createdAt: time },
        { id: "user-1", username: "alice", displayName: "测试用户", workspaceId: "workspace-user", role: "user", status: "active", createdAt: time },
    ];
    async function handle(request: Request) {
        const url = new URL(request.url);
        const path = url.pathname;
        if (path === "/test/state") return json({ writes, reads });
        if (!path.startsWith("/api/")) return;
        if (request.headers.get("x-platform-session") !== "test-session") return json(null, 401, "会话缺失");
        const write = request.method !== "GET";
        if (write && request.headers.get("x-csrf-token") !== "test-csrf") return json(null, 403, "CSRF 缺失");
        const body = write ? await request.json() as Record<string, unknown> : {};
        if (write) writes.push({ path, method: request.method, body }); else reads.push(path);
        const exact = (allowed: string[]) => Object.keys(body).every((field) => allowed.includes(field));
        if (path === "/api/models/catalog") return json({ source: "frontend", channels: [], models: [{ ...models[0], capabilitySpec, capabilityProfiles: [], legacyModelIds: [], defaultOptions: {}, available: true, pointPrices: { text_generation: 3 } }] });
        if (path === "/api/admin/users") return json({ users, total: users.length, page: 1, pageSize: 20 });
        if (path === "/api/admin/model-protocols") return json({ protocols: [{ id: "chat-completion", name: "文本协议", capability: "text" }] });
        if (path === "/api/admin/channels") {
            if (write) {
                if (!exact(["name", "publicAlias", "sortOrder", "concurrencyLimit", "enabled", "credentials"])) return json(null, 400, "未知渠道字段");
                return json({ channel: { ...channel, name: body.name } });
            }
            return json({ channels: [channel], total: 1, page: 1, pageSize: 20 });
        }
        if (path === "/api/admin/channels/channel-1") { Object.assign(channel, body); return json({ channel }); }
        if (path === "/api/admin/channels/channel-1/credentials") {
            if (!exact(["baseUrl", "apiKey", "secretKey", "headers"])) return json(null, 400, "未知凭据字段");
            channel.credentialVersion += 1;
            return json({ channel });
        }
        if (path === "/api/admin/channels/channel-1/models") {
            if (write) {
                if (!exact(["modelKey", "providerModelKey", "displayName", "icon", "capability", "protocol", "enabled", "capabilityConfig", "variants"])) return json(null, 400, "未知渠道模型字段");
                const model = { id: "channel-model-1", channelId: "channel-1", ...body };
                channelModels.push(model);
                return json({ model });
            }
            return json({ models: channelModels });
        }
        if (path === "/api/admin/models") {
            if (write) return json({ model: { ...models[0], ...body }, revision: { id: "new", version: 1 }, capabilitySpec, routes: [], defaultOptions: {} });
            const page = Number(url.searchParams.get("page") || 1), pageSize = Number(url.searchParams.get("pageSize") || 20);
            return json({ models: models.slice((page - 1) * pageSize, page * pageSize), total: models.length, page, pageSize });
        }
        if (path === "/api/admin/models/model-1") {
            if (write) {
                if (!exact(["code", "name", "icon", "description", "enabled", "sortOrder", "expectedRevisionId", "capabilitySpec", "defaultOptions", "routes"])) return json(null, 400, "未知模型字段");
                if (body.name === "冲突草稿") return json(null, 409, "模型已更新，请刷新后再发布", "revision_conflict");
            }
            return json({ model: models[0], revision: { id: "revision-1", version: 1, createdAt: time }, capabilitySpec, defaultOptions: { temperature: 0.2 }, routes: [{ id: "route-1", channelModelId: "channel-model-1", enabled: true, priority: 7, weight: 3 }] });
        }
        if (path === "/api/admin/models/model-1/prices") {
            if (write) { const price = { ...prices[0], id: `price-${prices.length + 1}`, version: prices.length + 1, points: Number(body.points) }; prices.unshift(price); return json({ price }); }
            return json({ prices });
        }
        if (path === "/api/admin/users/user-1/model-grants") {
            if (write) grants = body.logicalModelIds as string[];
            return json({ logicalModelIds: grants });
        }
        return json(null, 404, `未允许的路径：${path}`);
    }
    return { handle };
}
