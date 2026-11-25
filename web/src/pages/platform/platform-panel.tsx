import type { ReactNode } from "react";

/** 平台列表共享滚动区域，分页留在滚动区域外。 */
export function PlatformPanel({ children, pagination }: { children: ReactNode; pagination?: ReactNode }) {
    return <section className="platform-panel"><div className="platform-panel-scroll">{children}</div>{pagination}</section>;
}
