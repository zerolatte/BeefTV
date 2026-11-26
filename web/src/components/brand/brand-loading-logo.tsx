import { instituteBrand } from "./institute-brand";
import { isEmbeddedRuntime } from "@/lib/embedded-runtime";

/** 启动与页面切换共用品牌标识，独立启动阶段无需加载工作区状态。 */
export function BrandLoadingLogo({ compact = false }: { compact?: boolean }) {
    const variant = compact ? " is-compact" : "";
    if (isEmbeddedRuntime()) return <span className={`beeftv-loading-logo${variant}`} aria-hidden="true" />;
    return (
        <img
            className={`brand-loading-logo${variant}`}
            src={instituteBrand.wordmark}
            alt=""
            aria-hidden="true"
            width={432}
            height={168}
            draggable={false}
        />
    );
}
