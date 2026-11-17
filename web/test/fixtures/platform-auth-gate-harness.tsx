import { StrictMode, useEffect, useState } from "react";
import { createRoot } from "react-dom/client";
import { App, Modal } from "antd";

import { PlatformAuthGate, usePlatformAuthActions } from "@/components/platform/platform-auth-gate";
import { invalidateWorkspaceSession } from "@/services/workspace-session";
import { setRuntimeMode } from "@/services/workspace-mode";

declare global {
    interface Window {
        platformTest: {
            mounts: number;
            unmounts: number;
            hydrations: number;
            flushes: number;
            clears: number;
            failFlush: boolean;
            failClear: boolean;
            draft: string;
            invalidate: () => void;
        };
    }
}

window.platformTest = {
    mounts: 0,
    unmounts: 0,
    hydrations: 0,
    flushes: 0,
    clears: 0,
    failFlush: false,
    failClear: false,
    draft: "",
    invalidate: () => invalidateWorkspaceSession("test_expired"),
};
setRuntimeMode("platform", "test-platform");

function Workspace() {
    const [draft, setDraft] = useState("");
    const [modalOpen, setModalOpen] = useState(false);
    const auth = usePlatformAuthActions();
    useEffect(() => {
        window.platformTest.mounts += 1;
        return () => { window.platformTest.unmounts += 1; };
    }, []);
    return (
        <section>
            <h1>创作工作区</h1>
            <label>本地草稿<input value={draft} onChange={(event) => { setDraft(event.target.value); window.platformTest.draft = event.target.value; }} /></label>
            <button onClick={auth?.requestLogout}>退出账号</button>
            <button onClick={() => setModalOpen(true)}>编辑浮层草稿</button>
            <Modal open={modalOpen} footer={null} onCancel={() => setModalOpen(false)}>
                <label>浮层草稿<input defaultValue="浮层未保存内容" /></label>
            </Modal>
        </section>
    );
}

createRoot(document.getElementById("root")!).render(<StrictMode><App><PlatformAuthGate><Workspace /></PlatformAuthGate></App></StrictMode>);
