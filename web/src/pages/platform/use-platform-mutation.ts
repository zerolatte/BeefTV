import { useEffect, useRef, useState } from "react";
import { App } from "antd";

// 在表单校验开始前防重入，页面卸载后不再回写弹窗或显示旧操作提示。
export function usePlatformMutation() {
    const [busy, setBusy] = useState(false);
    const locked = useRef(false);
    const mounted = useRef(true);
    const { message } = App.useApp();
    useEffect(() => { mounted.current = true; return () => { mounted.current = false; }; }, []);
    async function submit<T>(work: () => Promise<T>, success: (value: T) => void) {
        if (locked.current) return;
        locked.current = true;
        setBusy(true);
        try {
            const result = await work();
            if (mounted.current) success(result);
        } catch (error) {
            if (mounted.current && error instanceof Error) void message.error(error.message);
        } finally {
            locked.current = false;
            if (mounted.current) setBusy(false);
        }
    }
    return { busy, submit };
}
