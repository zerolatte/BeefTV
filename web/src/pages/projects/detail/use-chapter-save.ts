import { useEffect, useRef } from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { App } from "antd";
import { updateProjectUnit, type ProjectDetail } from "@/services/api/projects";
import { getActiveUserScope } from "@/lib/user-scope";
import { getRuntimeMode } from "@/services/workspace-mode";
import { registerPlatformDraftPort } from "@/services/platform-workspace-drafts";

type ChapterDraft = { projectId: string; unitId?: string; title: string; sourceText: string; status: "ready" | "draft"; expectedRevision?: number };

export function useChapterSave(input: ChapterDraft & {
    dirty: boolean;
    onSaved: (revision: number, unchanged: boolean) => void;
    refreshProject: () => void;
}) {
    const { message } = App.useApp();
    const queryClient = useQueryClient();
    const live = useRef(input);
    const mounted = useRef(true);
    const pendingSave = useRef<Promise<void> | null>(null);
    live.current = input;
    useEffect(() => {
        mounted.current = true;
        return () => { mounted.current = false; };
    }, []);

    const mutation = useMutation({
        mutationFn: async (signal?: AbortSignal) => {
            const draft: ChapterDraft = { ...live.current };
            if (!draft.unitId || !draft.expectedRevision) throw new Error("章节版本尚未加载，请重新读取章节后保存");
            const saved = await updateProjectUnit(draft.projectId, draft.unitId, {
                expectedRevision: draft.expectedRevision,
                title: draft.title.trim(),
                sourceText: draft.sourceText,
                status: draft.status,
            }, signal);
            return { ...saved, draft };
        },
        onSuccess: ({ unit, projectRevision, draft }) => {
            const current = live.current;
            if (!mounted.current || current.projectId !== draft.projectId || current.unitId !== draft.unitId) return;
            queryClient.setQueryData(["project-unit", draft.projectId, unit.id], { unit, projectRevision });
            queryClient.setQueryData<{ units: ProjectDetail["units"]; canvasCounts: Record<string, number> }>(["project", draft.projectId, "units"], (cached) => cached ? {
                ...cached,
                units: cached.units.map((item) => item.id === unit.id ? { ...item, title: unit.title, wordCount: unit.wordCount, status: unit.status, updatedAt: unit.updatedAt } : item),
            } : cached);
            const unchanged = current.title === draft.title && current.sourceText === draft.sourceText;
            // 退出同步紧随请求完成检查草稿，不依赖 React 下一次渲染才变为已保存。
            live.current = { ...current, expectedRevision: projectRevision, dirty: unchanged ? false : current.dirty };
            current.onSaved(projectRevision, unchanged);
            current.refreshProject();
            message.success("章节已保存");
        },
        onError: (error) => {
            if (mounted.current) message.error(error instanceof Error ? error.message : "章节保存失败");
        },
    });
    const save = (signal?: AbortSignal) => {
        signal?.throwIfAborted();
        if (pendingSave.current) return pendingSave.current;
        if (!live.current.title.trim()) return Promise.reject(new Error("请输入章节标题后再保存"));
        const pending = mutation.mutateAsync(signal).then(() => undefined);
        pendingSave.current = pending;
        const clearPending = () => { if (pendingSave.current === pending) pendingSave.current = null; };
        void pending.then(clearPending, clearPending);
        return pending;
    };
    const saveRef = useRef(save);
    saveRef.current = save;
    const draftScope = getActiveUserScope();
    useEffect(() => {
        if (getRuntimeMode() !== "platform" || !input.unitId) return;
        const id = `project-chapter:${input.projectId}:${input.unitId}`;
        return registerPlatformDraftPort({
            id,
            scope: draftScope,
            inspect: () => ({ id, label: "章节正文", hasUnsynced: live.current.dirty || Boolean(pendingSave.current) }),
            flush: (signal) => saveRef.current(signal),
        });
    }, [draftScope, input.projectId, input.unitId]);
    return { isPending: mutation.isPending, mutate: () => { void save().catch(() => undefined); } };
}
