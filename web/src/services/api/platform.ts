import { abortPlatformRequests, bindPlatformUser, http } from "./request";

export type PlatformUser = { id: string; username: string; displayName: string; role: "admin" | "user"; status: "active" | "disabled"; mustChangePassword: boolean };
export type CreditAccount = { available: number; frozen: number };
export type CreditQuote = { id: string; unitCredits: number; units: number; totalCredits: number; expiresAt: string; priceVersion: number };
export type CreditLedger = { id: string; kind: string; amount: number; reason: string; createdAt: string };
export type CreditUsage = { id: string; userId: string; taskId: string; modelId: string; units: number; reserved: number; charged: number; refunded: number; state: string; createdAt: string };
export * from "./platform-models";
export type Page<T> = { items: T[]; total: number };

export const getPlatformSession = () => http.get<{ user: PlatformUser }>("/auth/session");
function broadcastSessionChange() {
    const channel = new BroadcastChannel("beeftv-platform-session");
    channel.postMessage("changed"); channel.close();
}
export async function platformLogin(username: string, password: string) {
    const result = await http.post<{ user: PlatformUser }>("/auth/login", { username, password });
    bindPlatformUser(result.user.id); broadcastSessionChange(); return result;
}
export async function changePlatformPassword(oldPassword: string, newPassword: string) {
    const result = await http.post("/auth/password", { oldPassword, newPassword });
    broadcastSessionChange(); return result;
}
export async function platformLogout() {
    await http.post("/auth/logout", {});
    abortPlatformRequests();
    broadcastSessionChange();
    window.location.assign("/");
}
export const getCredits = () => http.get<CreditAccount>("/credits");
export const getCreditLedger = (page: number) => http.get<Page<CreditLedger>>("/credits/ledger", { params: { page, pageSize: 30 } });
export const getAccounts = (page: number, q: string) => http.get<Page<PlatformUser>>("/admin/accounts", { params: { page, q, pageSize: 30 } });
export const createAccount = (username: string, displayName: string) => http.post<{ temporaryPassword: string }>("/admin/accounts", { username, displayName });
export const updateAccount = (user: PlatformUser, status: PlatformUser["status"]) => http.patch(`/admin/accounts/${user.id}`, { displayName: user.displayName, status });
export const resetAccountPassword = (id: string) => http.post<{ temporaryPassword: string }>(`/admin/accounts/${id}/reset-password`, {});
export const adjustCredits = (id: string, amount: number, reason: string, operationId: string) => http.post(`/admin/accounts/${id}/credits`, { amount, reason, operationId });
export const getCreditUsage = (page: number, state: string) => http.get<Page<CreditUsage>>("/admin/usage", { params: { page, state, pageSize: 30 } });
export const resolveCreditUsage = (id: string, units: number, reason: string) => http.post(`/admin/usage/${id}/resolve`, { units, reason });
