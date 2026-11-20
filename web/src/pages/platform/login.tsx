import { useState, type FormEvent } from "react";
import { createRoot } from "react-dom/client";
import { changePlatformPassword, platformLogin } from "@/services/api/platform";
import "./platform.css";

function Login({ mustChangePassword = false }: { mustChangePassword?: boolean }) {
    const [changing, setChanging] = useState(mustChangePassword);
    const [busy, setBusy] = useState(false);
    const [error, setError] = useState("");
    async function submit(event: FormEvent<HTMLFormElement>) {
        event.preventDefault();
        if (busy) return;
        const element = event.currentTarget;
        const form = new FormData(element);
        setBusy(true); setError("");
        try {
            if (changing) {
                if (form.get("password") !== form.get("confirm")) throw new Error("两次输入的新密码不一致");
                await changePlatformPassword(String(form.get("oldPassword")), String(form.get("password")));
                window.location.reload();
            } else {
                const result = await platformLogin(String(form.get("username")), String(form.get("password")));
                if (result.user.mustChangePassword) { setChanging(true); element.reset(); }
                else window.location.reload();
            }
        } catch (cause) { setError(cause instanceof Error ? cause.message : "操作失败，请重试"); }
        finally { setBusy(false); }
    }
    return <main className="platform-login"><form className="platform-login-card" onSubmit={submit} key={String(changing)}>
        <h1>BeefTV</h1><h2>{changing ? "修改临时密码" : "登录创作工作区"}</h2>
        <p>{changing ? "首次登录需要设置新密码，完成后请重新登录。" : "账号由管理员开通，AI 模型和积分由平台提供。"}</p>
        {changing ? <label>临时密码<input name="oldPassword" type="password" autoComplete="current-password" required /></label> : <label>用户名<input name="username" autoComplete="username" required maxLength={64} /></label>}
        <label>{changing ? "新密码（至少 12 个字符）" : "密码"}<input name="password" type="password" autoComplete={changing ? "new-password" : "current-password"} required minLength={changing ? 12 : undefined} maxLength={512} /></label>
        {changing && <label>确认新密码<input name="confirm" type="password" autoComplete="new-password" required /></label>}
        {error && <p role="alert" className="platform-error">{error}</p>}
        <button type="submit" disabled={busy}>{busy ? "处理中…" : changing ? "保存新密码" : "登录"}</button>
    </form></main>;
}

export function mountPlatformLogin(mustChangePassword = false) { createRoot(document.getElementById("root")!).render(<Login mustChangePassword={mustChangePassword} />); }
