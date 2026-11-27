import { useEffect, useRef, useState } from "react";
import { Alert } from "antd";
import { AppModal } from "@/components/ui/product/app-modal/app-modal";
import { deletePlatformChannel, type PlatformChannel } from "@/services/api/platform-models";

export function DeletePlatformChannelDialog({ channel, onClose, onDeleted }: { channel: PlatformChannel; onClose: () => void; onDeleted: () => void }) {
    const [busy, setBusy] = useState(false);
    const [error, setError] = useState("");
    const locked = useRef(false);
    const alive = useRef(true);
    useEffect(() => {
        alive.current = true;
        return () => { alive.current = false; };
    }, []);

    async function remove() {
        if (locked.current) return;
        locked.current = true;
        setBusy(true);
        setError("");
        try {
            await deletePlatformChannel(channel.id);
            if (alive.current) onDeleted();
        } catch (failure) {
            if (alive.current) setError(failure instanceof Error ? failure.message : "删除失败，请重试");
        } finally {
            locked.current = false;
            if (alive.current) setBusy(false);
        }
    }

    return (
        <AppModal
            flush scrollBody open
            className="platform-dialog platform-channel-delete-dialog"
            title="删除渠道"
            okText="删除渠道"
            cancelText="取消"
            okButtonProps={{ danger: true }}
            cancelButtonProps={{ disabled: busy }}
            closable={!busy}
            keyboard={!busy}
            confirmLoading={busy}
            onCancel={() => { if (!locked.current) onClose(); }}
            onOk={() => void remove()}
        >
            <div className="platform-form platform-channel-delete-content">
                <p>确定删除渠道「{channel.name}」吗？</p>
                <p>该渠道及其 {channel.totalModels} 个模型、规格和价格配置将从可用目录移除，用户无法再通过此渠道生成。此操作无法在页面中撤销。</p>
                <p>历史任务、已生成素材和积分流水会保留。有未完成任务或待核对积分时，需先处理完毕才能删除。</p>
                {error && <Alert type="error" showIcon title={error} />}
            </div>
        </AppModal>
    );
}
