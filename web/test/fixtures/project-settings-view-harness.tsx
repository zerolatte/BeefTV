import { StrictMode, useEffect, useRef, useState } from "react";
import { createRoot } from "react-dom/client";
import { flushSync } from "react-dom";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { App } from "antd";
import { MemoryRouter, Route, Routes, useLocation } from "react-router";

import { setActiveUserScope } from "../../src/lib/user-scope";
import type { Project, ProjectDetail } from "../../src/services/api/projects";
import { useUserStore } from "../../src/stores/use-user-store";
import ProjectSettingsView from "../../src/pages/projects/detail/settings";
import { setRuntimeMode } from "../../src/services/workspace-mode";
import { activateWorkspaceSession } from "../../src/services/workspace-session";
import { flushRegisteredPlatformDrafts, inspectRegisteredPlatformDrafts } from "../../src/services/platform-workspace-drafts";
import { useChapterSave } from "../../src/pages/projects/detail/use-chapter-save";
import ProjectsPage from "../../src/pages/projects";
import { WorkspaceSidebarNav } from "../../src/components/layout/workspace-sidebar-nav";

type HarnessWindow = Window & {
    __projectSettingsHarness?: {
        toasts: string[];
        refreshCount: number;
        strictSetups: number;
        strictCleanups: number;
        switchProjectABA: () => void;
        refreshCurrent: () => void;
        inspectDrafts: () => ReturnType<typeof inspectRegisteredPlatformDrafts>;
        flushDrafts: () => ReturnType<typeof flushRegisteredPlatformDrafts>;
    };
};

const harnessWindow = window as HarnessWindow;
const platformMode = new URLSearchParams(window.location.search).has("platform");
const draftScope = platformMode ? "platform:workspace-a" : "owner-a";
setRuntimeMode(platformMode ? "platform" : "local");
if (platformMode) activateWorkspaceSession({ mode: "platform", accountId: "owner-a", workspaceId: "workspace-a", sessionId: "test-session" });

useUserStore.setState({
    hydrated: true,
    storageMode: "remote",
    user: {
        id: "owner-a",
        username: "remote-user",
        displayName: "账号 A",
        role: "user",
        status: "active",
    },
});
if (platformMode) useUserStore.getState().setFeatures({ ...useUserStore.getState().features, generationEnabled: false });
setActiveUserScope(draftScope);

const queryClient = new QueryClient({
    defaultOptions: {
        queries: { retry: false, refetchOnWindowFocus: false },
        mutations: { retry: false },
    },
});

function makeProject(id: string, status: Project["status"]): Project {
    return {
        id,
        userId: "owner-a",
        name: id === "project-a" ? "项目甲" : "项目乙",
        type: "short-drama",
        aspectRatio: "9:16",
        sourceType: "blank",
        description: "",
        stylePresetId: "",
        status,
        revision: 1,
        createdAt: "2026-10-02T00:00:00.000Z",
        updatedAt: "2026-10-02T00:00:00.000Z",
    };
}

function makeDetail(id: string, status: Project["status"] = "active"): ProjectDetail {
    return {
        project: makeProject(id, status),
        units: [],
        canvases: [],
        canvasUnitLinks: [],
        assets: [],
        assetFolders: [],
        workflows: [],
        shots: [],
        shotRevisions: [],
        shotArtifacts: [],
        shotReferences: [],
        assetCandidates: [],
        tasks: [],
    };
}

function recordToast(node: Node) {
    const text = node.textContent?.trim();
    if (text) harnessWindow.__projectSettingsHarness!.toasts.push(text);
}

function watchToasts() {
    const observer = new MutationObserver((mutations) => {
        for (const mutation of mutations) {
            for (const node of mutation.addedNodes) {
                if (!(node instanceof HTMLElement)) continue;
                if (node.classList.contains("ant-message-notice") || node.querySelector?.(".ant-message-notice")) {
                    recordToast(node);
                }
            }
        }
    });
    observer.observe(document.body, { childList: true, subtree: true });
}

harnessWindow.__projectSettingsHarness = {
    toasts: [],
    refreshCount: 0,
    strictSetups: 0,
    strictCleanups: 0,
    switchProjectABA: () => {},
    refreshCurrent: () => {},
    inspectDrafts: () => inspectRegisteredPlatformDrafts(draftScope),
    flushDrafts: () => flushRegisteredPlatformDrafts(draftScope),
};

function Harness() {
    const [detail, setDetail] = useState(() => makeDetail("project-a"));
    const projectIdRef = useRef(detail.project.id);
    projectIdRef.current = detail.project.id;

    useEffect(() => {
        harnessWindow.__projectSettingsHarness!.strictSetups += 1;
        return () => {
            harnessWindow.__projectSettingsHarness!.strictCleanups += 1;
        };
    }, []);

    harnessWindow.__projectSettingsHarness!.switchProjectABA = () => {
        flushSync(() => {
            setDetail(makeDetail("project-b"));
        });
        flushSync(() => {
            setDetail(makeDetail("project-a"));
        });
    };

    const refreshProject = () => {
        harnessWindow.__projectSettingsHarness!.refreshCount += 1;
        const projectId = projectIdRef.current;
        void fetch(`/api/projects/${encodeURIComponent(projectId)}`)
            .then((response) => response.json())
            .then((envelope: { data?: { project?: Project } | Project }) => {
                const payload = envelope.data;
                const project = payload && typeof payload === "object" && "project" in payload
                    ? payload.project
                    : payload as Project | undefined;
                if (!project?.id) return;
                setDetail((current) => current.project.id === project.id ? { ...current, project: { ...current.project, ...project } } : current);
            });
    };
    harnessWindow.__projectSettingsHarness!.refreshCurrent = refreshProject;

    return (
        <div>
            <div data-testid="project-id">{detail.project.id}</div>
            <div data-testid="project-status">{detail.project.status}</div>
            <div data-testid="project-revision">{detail.project.revision}</div>
            <div data-testid="refresh-count">{harnessWindow.__projectSettingsHarness!.refreshCount}</div>
            <ProjectSettingsView detail={detail} refreshProject={refreshProject} onCreateCanvas={() => {}} />
        </div>
    );
}

function ChapterHarness() {
    const [title, setTitle] = useState("第一章");
    const [sourceText, setSourceText] = useState("<p>原正文</p>");
    const [dirty, setDirty] = useState(false);
    const [revision, setRevision] = useState(1);
    const save = useChapterSave({
        projectId: "project-a", unitId: "unit-a", title, sourceText, status: "ready", expectedRevision: revision, dirty,
        onSaved: (nextRevision, unchanged) => { setRevision(nextRevision); if (unchanged) setDirty(false); },
        refreshProject: () => { harnessWindow.__projectSettingsHarness!.refreshCount += 1; },
    });
    return <div>
        <div data-testid="project-id">project-a</div>
        <div data-testid="chapter-revision">{revision}</div>
        <label>章节标题<input value={title} onChange={(event) => { setTitle(event.target.value); setDirty(true); }} /></label>
        <label>章节正文<textarea value={sourceText} onChange={(event) => { setSourceText(event.target.value); setDirty(true); }} /></label>
        <button disabled={!dirty || save.isPending} onClick={() => save.mutate()}>{dirty ? "保存章节" : "章节已保存"}</button>
    </div>;
}

function ProjectLibraryHarness() {
    const { pathname } = useLocation();
    return <div>
        <div data-testid="project-id">project-a</div>
        <div data-testid="route-path">{pathname}</div>
        <aside style={{ width: 240 }}><WorkspaceSidebarNav collapsed={false} onNavigate={() => undefined} onOpenSearch={() => undefined} onExpand={() => undefined} onCollapse={() => undefined} /></aside>
        <Routes>
            <Route path="/projects" element={<ProjectsPage />} />
            <Route path="/projects/:id/overview" element={<div>项目创建完成</div>} />
            <Route path="/canvas" element={<div>画布列表</div>} />
            <Route path="/assets" element={<div>素材列表</div>} />
        </Routes>
    </div>;
}

watchToasts();
createRoot(document.getElementById("root")!).render(
    <StrictMode>
        <QueryClientProvider client={queryClient}>
            <App>
                {new URLSearchParams(window.location.search).has("library") ? <MemoryRouter initialEntries={["/projects"]}><ProjectLibraryHarness /></MemoryRouter> : new URLSearchParams(window.location.search).has("chapter") ? <ChapterHarness /> : <Harness />}
            </App>
        </QueryClientProvider>
    </StrictMode>,
);
