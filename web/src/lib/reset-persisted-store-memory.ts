import type { StoreApi } from "zustand";
import type { PersistOptions, PersistStorage } from "zustand/middleware";

/** 清除身份切换前的内存投影时，不得重新产生已锁定工作区的持久写入。 */
export function resetPersistedStoreMemory<T, P>(
    store: StoreApi<T> & {
        persist: {
            getOptions(): Partial<PersistOptions<T, P>>;
            setOptions(options: Partial<PersistOptions<T, P>>): void;
        };
    },
    patch: Partial<T>,
) {
    const storage = store.persist.getOptions().storage;
    const memoryOnly: PersistStorage<P> = { getItem: () => null, setItem: () => {}, removeItem: () => {} };
    store.persist.setOptions({ storage: memoryOnly });
    try { store.setState(patch); }
    finally { store.persist.setOptions({ storage }); }
}
