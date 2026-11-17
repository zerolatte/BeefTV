import { expect, test } from "bun:test";
import { createHash } from "node:crypto";
import { resourceUploadDigest } from "../src/services/resource-upload-digest";

test("上传摘要按固定块读取并绑定全部字节", async () => {
    const bytes = new Uint8Array((8 << 20) + 3).fill(7);
    const file = new Blob([bytes]);
    const root = createHash("sha256");
    root.update(createHash("sha256").update(bytes.subarray(0, 8 << 20)).digest());
    root.update(createHash("sha256").update(bytes.subarray(8 << 20)).digest());
    expect(await resourceUploadDigest(file, () => {})).toBe(`sha256-chunks-v1:${root.digest("hex")}`);
    bytes[bytes.length - 1] = 8;
    expect(await resourceUploadDigest(new Blob([bytes]), () => {})).not.toBe(await resourceUploadDigest(file, () => {}));
});

test("计算摘要期间失去会话即停止", async () => {
    let checks = 0;
    await expect(resourceUploadDigest(new Blob(["payload"]), () => {
        if (++checks === 2) throw new DOMException("会话已切换", "AbortError");
    })).rejects.toMatchObject({ name: "AbortError" });
    expect(checks).toBe(2);
});
