import { useState, type FormEvent, type ReactNode } from "react";
import { Button, Input } from "antd";
import { KeyRound, LogIn, ShieldCheck } from "lucide-react";

export type PlatformAuthScreenMode = "login" | "password-change" | "session-recovery";

type PlatformAuthScreenProps = {
    mode: PlatformAuthScreenMode;
    username?: string;
    error?: string;
    busy?: boolean;
    unsyncedDraftCount?: number;
    onLogin: (username: string, password: string) => void;
    onChangePassword: (currentPassword: string, newPassword: string) => void;
    onReauthenticate: (username: string, password: string) => void;
    onAbandonDrafts?: () => void;
};

export function PlatformAuthScreen({
    mode,
    username: initialUsername = "",
    error,
    busy = false,
    unsyncedDraftCount = 0,
    onLogin,
    onChangePassword,
    onReauthenticate,
    onAbandonDrafts,
}: PlatformAuthScreenProps) {
    const [username, setUsername] = useState(initialUsername);
    const [password, setPassword] = useState("");
    const [currentPassword, setCurrentPassword] = useState("");
    const [newPassword, setNewPassword] = useState("");
    const [confirmPassword, setConfirmPassword] = useState("");
    const [validationError, setValidationError] = useState<string>();

    const submit = (event: FormEvent<HTMLFormElement>) => {
        event.preventDefault();
        setValidationError(undefined);

        if (mode === "password-change") {
            if (newPassword.length < 12 || newPassword.length > 128) {
                setValidationError("新密码长度须为 12 至 128 个字符。");
                return;
            }
            if (newPassword !== confirmPassword) {
                setValidationError("两次输入的新密码不一致。");
                return;
            }
            onChangePassword(currentPassword, newPassword);
            return;
        }

        if (mode === "session-recovery") {
            onReauthenticate(initialUsername, password);
            return;
        }

        onLogin(username.trim(), password);
    };

    const title = mode === "password-change"
        ? "修改密码"
        : mode === "session-recovery"
            ? "恢复工作区会话"
            : "登录工作区";
    const shownError = validationError || error;

    return (
        <main className="app-user-workspace flex min-h-dvh w-full items-center justify-center px-5 py-10" style={{ background: "var(--user-page-bg)", color: "var(--user-ink)" }}>
            <section
                aria-labelledby="platform-auth-title"
                className="w-full max-w-[400px] border p-6 sm:p-8"
                style={{
                    borderRadius: "var(--r-md)",
                    borderColor: "var(--user-border-strong)",
                    background: "var(--user-surface)",
                    boxShadow: "var(--elevation-card)",
                }}
            >
                <header className="mb-6 flex items-center gap-3">
                    <span className="grid size-9 shrink-0 place-items-center overflow-hidden rounded-[var(--r-md)]" style={{ background: "var(--btn-solid-bg)" }}>
                        <img className="size-7 object-contain" src="/beef-logo.png" alt="BeefTV" />
                    </span>
                    <div className="min-w-0">
                        <div className="text-[14px] leading-[20px] font-medium">BeefTV</div>
                        <h1 id="platform-auth-title" className="text-[16px] leading-[22px] font-medium">{title}</h1>
                    </div>
                </header>

                {mode === "session-recovery" && unsyncedDraftCount > 0 ? (
                    <p role="status" className="mb-4 border px-3 py-2 text-[13px] leading-5" style={{ borderRadius: "var(--r-sm)", borderColor: "var(--user-border)", background: "var(--user-surface-muted)", color: "var(--user-ink)" }}>
                        检测到 {unsyncedDraftCount} 项未同步更改。请恢复原账号以同步，或明确放弃后再切换账号。
                    </p>
                ) : null}

                <form className="grid gap-4" onSubmit={submit}>
                    {mode === "login" ? (
                        <Field label="用户名">
                            <Input autoComplete="username" autoFocus value={username} onChange={(event) => setUsername(event.target.value)} maxLength={32} required disabled={busy} />
                        </Field>
                    ) : null}

                    {mode === "session-recovery" ? (
                        <Field label="原账号">
                            <Input value={initialUsername} readOnly autoComplete="username" />
                        </Field>
                    ) : null}

                    {mode === "password-change" ? (
                        <>
                            {initialUsername ? <Field label="账号"><Input value={initialUsername} readOnly autoComplete="username" /></Field> : null}
                            <Field label="当前密码">
                                <Input.Password autoComplete="current-password" value={currentPassword} onChange={(event) => setCurrentPassword(event.target.value)} required disabled={busy} />
                            </Field>
                            <Field label="新密码">
                                <Input.Password autoComplete="new-password" value={newPassword} onChange={(event) => setNewPassword(event.target.value)} minLength={12} maxLength={128} required disabled={busy} />
                            </Field>
                            <Field label="确认新密码">
                                <Input.Password autoComplete="new-password" value={confirmPassword} onChange={(event) => setConfirmPassword(event.target.value)} minLength={12} maxLength={128} required disabled={busy} />
                            </Field>
                        </>
                    ) : (
                        <Field label="密码">
                            <Input.Password
                                autoComplete="current-password"
                                autoFocus={mode === "session-recovery"}
                                value={password}
                                onChange={(event) => setPassword(event.target.value)}
                                required
                                disabled={busy}
                            />
                        </Field>
                    )}

                    {shownError ? <p role="alert" className="text-[13px] leading-5" style={{ color: "var(--palette-status-error)" }}>{shownError}</p> : null}

                    <Button
                        htmlType="submit"
                        type="primary"
                        size="large"
                        block
                        loading={busy}
                        icon={mode === "password-change" ? <ShieldCheck className="size-4" /> : mode === "login" ? <LogIn className="size-4" /> : <KeyRound className="size-4" />}
                    >
                        {busy ? "正在验证" : mode === "password-change" ? "保存新密码" : mode === "session-recovery" ? "重新认证" : "登录"}
                    </Button>
                </form>

                {mode === "session-recovery" && onAbandonDrafts ? (
                    <div className="mt-4 border-t pt-4" style={{ borderColor: "var(--user-border)" }}>
                        <Button block danger disabled={busy} onClick={onAbandonDrafts}>
                            放弃本地未同步内容并切换账号
                        </Button>
                    </div>
                ) : null}
            </section>
        </main>
    );
}

function Field({ label, children }: { label: string; children: ReactNode }) {
    return (
        <label className="grid gap-1.5 text-[12px] leading-[18px] font-medium" style={{ color: "var(--user-ink-muted)" }}>
            <span>{label}</span>
            {children}
        </label>
    );
}
