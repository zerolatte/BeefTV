const DIGEST_CHUNK_SIZE = 8 << 20;

// 固定分块摘要与服务端一致，大文件始终只读取一个分块到内存。
export async function resourceUploadDigest(file: Blob, assertCurrentSession: () => void): Promise<string> {
    const parts = new Uint8Array(Math.ceil(file.size / DIGEST_CHUNK_SIZE) * 32);
    for (let offset = 0, index = 0; offset < file.size; offset += DIGEST_CHUNK_SIZE, index++) {
        assertCurrentSession();
        const bytes = await file.slice(offset, offset + DIGEST_CHUNK_SIZE).arrayBuffer();
        assertCurrentSession();
        const digest = await crypto.subtle.digest("SHA-256", bytes);
        assertCurrentSession();
        parts.set(new Uint8Array(digest), index * 32);
    }
    const result = await crypto.subtle.digest("SHA-256", parts);
    assertCurrentSession();
    return `sha256-chunks-v1:${Array.from(new Uint8Array(result), value => value.toString(16).padStart(2, "0")).join("")}`;
}
