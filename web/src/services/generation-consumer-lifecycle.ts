import { createWorkspaceSessionRequest, type WorkspaceSessionRequestBinding } from "@/services/workspace-session";

type ActiveGenerationConsumer = {
    controller: WorkspaceSessionRequestBinding;
    settled: Promise<unknown>;
};

export type GenerationConsumerHandle = {
    signal: AbortSignal;
    release(): void;
};

const activeGenerationConsumers = new Set<ActiveGenerationConsumer>();
let pausedTransitions = 0;
let transitionTail = Promise.resolve();

function abortError(): DOMException {
    return new DOMException("The operation was aborted", "AbortError");
}

export function activeGenerationConsumerController(controller: AbortController): AbortController {
    return controller.signal.aborted ? new AbortController() : controller;
}

export function runGenerationConsumer<T>(signal: AbortSignal | undefined, operation: (signal: AbortSignal) => Promise<T>): Promise<T> {
    if (pausedTransitions > 0) return Promise.reject(abortError());

    const controller = createWorkspaceSessionRequest(signal);

    const active = {} as ActiveGenerationConsumer;
    const result = Promise.resolve().then(async () => {
        if (controller.signal.aborted) throw abortError();
        const value = await operation(controller.signal);
        if (!controller.isCurrent() || controller.signal.aborted) throw abortError();
        return value;
    });
    active.controller = controller;
    active.settled = result.finally(() => {
        controller.release();
        activeGenerationConsumers.delete(active);
    });
    activeGenerationConsumers.add(active);
    return active.settled as Promise<T>;
}

export function beginGenerationConsumer(signal?: AbortSignal): GenerationConsumerHandle {
    const controller = createWorkspaceSessionRequest(signal);
    if (pausedTransitions > 0) {
        controller.abort();
        controller.release();
        return { signal: controller.signal, release() {} };
    }
    let release!: () => void;
    const settled = new Promise<void>((resolve) => {
        release = resolve;
    });
    const active: ActiveGenerationConsumer = { controller, settled };
    activeGenerationConsumers.add(active);
    let released = false;
    return {
        signal: controller.signal,
        release() {
            if (released) return;
            released = true;
            controller.release();
            activeGenerationConsumers.delete(active);
            release();
        },
    };
}

async function cancelAndDrainActiveGenerationConsumers() {
    while (activeGenerationConsumers.size) {
        for (const active of activeGenerationConsumers) active.controller.abort();
        await Promise.allSettled([...activeGenerationConsumers].map((active) => active.settled));
    }
}

export function withGenerationConsumersPaused<T>(operation: () => Promise<T>): Promise<T> {
    pausedTransitions += 1;
    const result = transitionTail.then(async () => {
        await cancelAndDrainActiveGenerationConsumers();
        return operation();
    });
    transitionTail = result.then(
        () => undefined,
        () => undefined,
    );
    return result.finally(() => {
        pausedTransitions -= 1;
    });
}

export function cancelAndDrainGenerationConsumers(): Promise<void> {
    return withGenerationConsumersPaused(async () => undefined);
}
