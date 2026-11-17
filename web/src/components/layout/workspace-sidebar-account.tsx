import { LoaderCircle, UserRound } from "lucide-react";
import { useState } from "react";

import { useBeefTVConnection } from "@/components/layout/beeftv-connection-provider";
import { AccountSettingsModal } from "@/components/account-settings/account-settings-modal";
import { cn } from "@/lib/utils";
import { isBeefTVAuthenticated } from "@/services/api/beeftv-connection";
import { useUserStore } from "@/stores/use-user-store";
import { WorkspaceAccountMenu } from "@/components/layout/workspace-account-menu";

export function WorkspaceSidebarAccount({ collapsed }: { collapsed: boolean }) {
    const mode = useUserStore((state) => state.runtimeMode);
    if (mode === "platform") return <PlatformSidebarAccount collapsed={collapsed} />;
    return <LocalSidebarAccount collapsed={collapsed} />;
}

function PlatformSidebarAccount({ collapsed }: { collapsed: boolean }) {
    const user = useUserStore((state) => state.user);
    return (
        <div className="flex min-h-11 min-w-0 items-center gap-2 px-2 py-1">
            <WorkspaceAccountMenu />
            {!collapsed && user ? <span className="min-w-0 truncate text-[14px] leading-[22px]" style={{ color: "var(--user-ink)" }} title={user.displayName || user.username}>{user.displayName || user.username}</span> : null}
        </div>
    );
}

function LocalSidebarAccount({ collapsed }: { collapsed: boolean }) {
    const [open, setOpen] = useState(false);
    const account = useBeefTVConnection();
    const { connection, loading } = account;
    const connected = isBeefTVAuthenticated(connection);
    const pending = connection?.state === "pending" || connection?.state === "connecting";
    const name = connection?.account?.display_name || connection?.account?.username || connection?.account?.email || "BeefTV 账户";
    const label = loading ? "正在读取账户" : connected ? name : pending ? "等待登录确认" : "登录 BeefTV";

    return <>
        <button
            type="button"
            className={cn("app-workspace-nav-link app-workspace-official-account group relative flex min-h-11 w-full min-w-0 items-center justify-between gap-2 rounded-[var(--r-md)] px-3 py-2 text-[var(--fs-body)] text-foreground/62 hover:bg-surface-hover hover:text-foreground", collapsed && "is-collapsed")}
            style={collapsed ? undefined : { paddingLeft: 10 }}
            disabled={loading}
            aria-label={connected ? `BeefTV 账户：${name}` : label}
            title={connected ? `BeefTV 账户：${name}` : label}
            onClick={() => setOpen(true)}
        >
            <span className="app-workspace-nav-main flex min-w-0 items-center gap-2.5">
                <span className="app-workspace-nav-icon" aria-hidden="true">
                    {pending ? <LoaderCircle className="size-4 motion-safe:animate-spin" strokeWidth={1.8} /> : <UserRound className="size-4" strokeWidth={1.8} />}
                </span>
                {!collapsed ? <span className="app-workspace-nav-title truncate">{label}</span> : null}
            </span>
        </button>
        <AccountSettingsModal open={open} onClose={() => setOpen(false)} />
    </>;
}
