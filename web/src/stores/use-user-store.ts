import { create } from "zustand";

export type LocalUser = {
    id: string;
    workspaceId?: string;
    username: string;
    email?: string;
    displayName: string;
    avatarUrl?: string;
    identityProvider?: string;
    identityId?: string;
    identityUsername?: string;
    role: "admin" | "user";
    status: "active" | "disabled";
    lastLoginAt?: string;
    createdAt?: string;
    updatedAt?: string;
};

export type RuntimeLimits = {
    activeTaskLimit: number;
    resourceUploadMB: number;
    recycleBinRetentionDays?: number;
};

export type FeatureAvailability = {
    shortDramaEnabled: boolean;
    taskCenterEnabled: boolean;
    customChannelsEnabled: boolean;
    frontendModelsEnabled: boolean;
    pluginCenterEnabled: boolean;
    systemPluginsVisibleToUsers: boolean;
    assistantEnabled?: boolean;
    proposalEnabled?: boolean;
    pluginsEnabled?: boolean;
    skillsEnabled?: boolean;
    eagleEnabled?: boolean;
    localAgentEnabled?: boolean;
    creationEnabled?: boolean;
    generationEnabled?: boolean;
    configured?: boolean;
    updatedBy?: string;
    updatedAt?: string;
};

export const defaultFeatureAvailability: FeatureAvailability = {
    shortDramaEnabled: true,
    taskCenterEnabled: true,
    customChannelsEnabled: true,
    frontendModelsEnabled: false,
    pluginCenterEnabled: true,
    systemPluginsVisibleToUsers: true,
    assistantEnabled: true,
    proposalEnabled: true,
    pluginsEnabled: true,
    skillsEnabled: true,
    eagleEnabled: true,
    localAgentEnabled: true,
    creationEnabled: true,
    generationEnabled: true,
};

export type PlatformAuthState = "unresolved" | "anonymous" | "authenticated" | "password-change-required" | "reauth-required";

type UserStore = {
    hydrated: boolean;
    runtimeMode: "unknown" | "local" | "platform";
    platformInstanceId?: string;
    authState: PlatformAuthState;
    authReason?: string;
    user: LocalUser | null;
    storageMode: "local" | "remote";
    runtimeLimits: RuntimeLimits;
    features: FeatureAvailability;
    setUser: (user: LocalUser | null) => void;
    setStorageMode: (mode: "local" | "remote") => void;
    setRuntimeLimits: (limits?: RuntimeLimits) => void;
    setFeatures: (features?: FeatureAvailability) => void;
    setHydrated: (hydrated: boolean) => void;
    setRuntimeMode: (mode: "unknown" | "local" | "platform") => void;
    setAuthState: (state: PlatformAuthState, reason?: string) => void;
    clearSession: () => void;
};

export const useUserStore = create<UserStore>()((set) => ({
    hydrated: false,
    runtimeMode: "unknown",
    authState: "unresolved",
    authReason: undefined,
    user: null,
    storageMode: "remote",
    runtimeLimits: { activeTaskLimit: 5, resourceUploadMB: 50, recycleBinRetentionDays: 30 },
    features: defaultFeatureAvailability,
    setUser: (user) => set({ user }),
    setStorageMode: (storageMode) => set({ storageMode }),
    setRuntimeLimits: (runtimeLimits) => set({ runtimeLimits: runtimeLimits || { activeTaskLimit: 5, resourceUploadMB: 50, recycleBinRetentionDays: 30 } }),
    setFeatures: (features) => set({ features: features ? { ...defaultFeatureAvailability, ...features } : defaultFeatureAvailability }),
    setHydrated: (hydrated) => set({ hydrated }),
    setRuntimeMode: (runtimeMode) => set({ runtimeMode }),
    setAuthState: (authState, authReason) => set({ authState, authReason }),
    clearSession: () => set({ user: null, authState: "anonymous", authReason: undefined, storageMode: "remote", runtimeLimits: { activeTaskLimit: 5, resourceUploadMB: 50, recycleBinRetentionDays: 30 }, features: defaultFeatureAvailability }),
}));
