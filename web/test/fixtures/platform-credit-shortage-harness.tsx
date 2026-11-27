import { createRoot } from "react-dom/client";
import { useCallback, useRef, useState } from "react";
import { App, Button, ConfigProvider } from "antd";
import { MemoryRouter, useLocation, useNavigate } from "react-router";
import { PlatformCreditShortageHost } from "@/pages/platform/credit-shortage-host";
import { getAntThemeConfig } from "@/lib/app-theme";
import { applySkinTheme } from "@/lib/skin-themes";
import { createGenerationTask } from "@/services/api/task-center";
import { isPlatformCreditShortageHandled } from "@/services/platform-credit-shortage";
import { setWorkspaceCapabilitySnapshot } from "@/services/workspace-mode";
import { useCanvasGenerationBatches } from "@/pages/canvas/use-canvas-generation-batches";
import { createModelChannel, defaultConfig, useConfigStore } from "@/stores/use-config-store";
import { CanvasNodeType, type CanvasNodeData } from "@/types/canvas";
import type { CanvasNodeGenerationOptions } from "@/pages/canvas/use-canvas-generation-executor";

const dark = new URLSearchParams(location.search).get("theme") === "dark";
document.documentElement.classList.toggle("dark", dark);
applySkinTheme("classic", dark ? "dark" : "light");
setWorkspaceCapabilitySnapshot({ contractVersion: 1, profile: "platform", capabilities: { localAssets: true, providerCalls: true } });

function Harness() {
    const [busy, setBusy] = useState(false);
    const [result, setResult] = useState("");
    const { message } = App.useApp();
    const navigate = useNavigate();
    const route = useLocation();
    const submit = async () => {
        setBusy(true);
        try {
            const task = await createGenerationTask({ type: "canvas_image", prompt: "测试图片", input: { mode: "image", config: { channelId: "test", model: "image" }, metadata: { clientOperationId: crypto.randomUUID() } } });
            setResult(task.id);
        } catch (error) {
            if (!isPlatformCreditShortageHandled(error)) message.error(error instanceof Error ? error.message : "生成失败");
        } finally { setBusy(false); }
    };
    return <>
        <Button autoInsertSpace={false} loading={busy} onClick={submit}>生成</Button>
        <output data-testid="busy">{String(busy)}</output>
        <output data-testid="result">{result}</output>
        <output data-testid="route">{route.pathname}</output>
        <PlatformCreditShortageHost navigate={navigate} />
    </>;
}

function BatchHarness() {
    const [nodes, setNodes] = useState<CanvasNodeData[]>([
        { id: "source", type: CanvasNodeType.Script, title: "脚本", position: { x: 0, y: 0 }, width: 320, height: 200 },
        { id: "target", type: CanvasNodeType.Image, title: "结果", position: { x: 400, y: 0 }, width: 320, height: 200, metadata: { prompt: "保留提示词", status: "idle" } },
    ]);
    const nodesRef = useRef(nodes);
    nodesRef.current = nodes;
    const [submits, setSubmits] = useState(0);
    const generate = useCallback(async (_id: string, _mode: unknown, _prompt: string, options?: CanvasNodeGenerationOptions) => {
        setSubmits((count) => count + 1);
        options?.onCreditShortage?.();
    }, []);
    const { enqueueGenerationBatch } = useCanvasGenerationBatches({ projectId: "canvas", projectLoaded: true, nodes, nodesRef, setNodes, handleGenerateNode: generate });
    return <>
        <Button onClick={() => enqueueGenerationBatch("source", "storyboard_image", [{ rowId: "row", nodeId: "target" }])}>提交批次</Button>
        <output data-testid="submits">{submits}</output>
        <output data-testid="batch-status">{nodes[0].metadata?.generationBatches?.[0]?.items[0]?.status}</output>
        <output data-testid="batch-error">{nodes[0].metadata?.generationBatches?.[0]?.items[0]?.errorCode}</output>
    </>;
}

const batch = new URLSearchParams(location.search).get("scenario") === "batch";
if (batch) {
    const channel = createModelChannel({ id: "test", name: "测试", enabled: true, apiFormat: "openai", models: ["image"] });
    useConfigStore.setState({ config: { ...defaultConfig, channels: [channel], imageModel: "test::image", model: "test::image" }, isAiConfigReady: () => true });
}
createRoot(document.getElementById("root")!).render(<ConfigProvider theme={getAntThemeConfig(dark, "classic")}><App><MemoryRouter initialEntries={["/create"]}>{batch ? <BatchHarness /> : <Harness />}</MemoryRouter></App></ConfigProvider>);
