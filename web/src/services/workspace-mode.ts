import { useUserStore } from "@/stores/use-user-store";

let capabilitySnapshot: WorkspaceCapabilityContract | null = null;

export type WorkspaceCapabilityContract = {
    contractVersion: number;
    profile: "local" | "platform";
    capabilities: {
        localAssets: boolean;
        providerCalls: boolean;
    };
};

/** Central runtime boundary shared by local workspace UI and hosted sync. */
export function isLocalWorkspaceMode() {
    return getRuntimeMode() === "local";
}

export function getRuntimeMode() {
    return useUserStore.getState().runtimeMode;
}

export function setRuntimeMode(mode: "unknown" | "local" | "platform", instanceId?: string) {
    useUserStore.getState().setRuntimeMode(mode);
    if (mode !== "platform" || instanceId !== undefined) useUserStore.setState({ platformInstanceId: instanceId });
}

/**
 * Single capability projection for UI boundaries. Keep the hosted profile
 * available, but make the local-first contract explicit instead of repeating
 * storageMode/username checks throughout pages.
 */
export type WorkspaceCapabilities = { local: boolean; localAssets: boolean; providerCalls: boolean };

export function workspaceCapabilities(): WorkspaceCapabilities {
    const local = isLocalWorkspaceMode();
    return {
        local,
        localAssets: capabilitySnapshot?.capabilities.localAssets ?? local,
        providerCalls: capabilitySnapshot?.capabilities.providerCalls ?? local,
    };
}

export function setWorkspaceCapabilitySnapshot(snapshot: WorkspaceCapabilityContract | null) {
    capabilitySnapshot = snapshot;
    if (snapshot) setRuntimeMode(snapshot.profile);
}

export function resetWorkspaceMode() {
    capabilitySnapshot = null;
    setRuntimeMode("unknown");
}
