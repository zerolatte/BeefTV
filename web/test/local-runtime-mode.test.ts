import { expect, test } from "bun:test";

import { isLocalRuntimeModeForSession } from "../src/lib/runtime-mode";

test("只有已确认的local合同开放本地能力", () => {
    expect(isLocalRuntimeModeForSession({ runtimeMode: "local" })).toBe(true);
    expect(isLocalRuntimeModeForSession({ runtimeMode: "platform" })).toBe(false);
    expect(isLocalRuntimeModeForSession({ runtimeMode: "unknown" })).toBe(false);
    expect(isLocalRuntimeModeForSession({})).toBe(false);
});
