import { useEffect, useRef, useState, type FormEvent } from "react";
import { createRoot } from "react-dom/client";
import { AlertCircle, ArrowRight, Eye, EyeOff, LoaderCircle, LockKeyhole, Moon, ShieldCheck, Sun } from "lucide-react";
import { changePlatformPassword, platformLogin } from "@/services/api/platform";
import { useThemeStore, watchSystemTheme } from "@/stores/use-theme-store";
import { INSTITUTE_RIDGE_PATH, INSTITUTE_RIDGE_VIEW_BOX } from "./institute-ridge";
import instituteWordmark from "./assets/institute-wordmark.png";
import instituteIcon from "./assets/institute-icon.jpg";
import "./platform.css";

const INSTITUTE_NAME = "长钢研究院";
const INSTITUTE_NAME_LATIN = "Changgang Research Institute";
const WORKBENCH_NAME = "影视创作工作台";

/** 密码输入复用同一套可见切换、标签和错误关联。 */
function PasswordField({ name, label, placeholder, newPassword = false, invalid = false, hint }: {
    name: string;
    label: string;
    placeholder: string;
    newPassword?: boolean;
    invalid?: boolean;
    hint?: string;
}) {
    const [visible, setVisible] = useState(false);
    const id = `platform-login-${name}`;
    return (
        <div className="platform-login-field">
            <label htmlFor={id}>{label}</label>
            <div className="platform-login-password">
                <input id={id} name={name} type={visible ? "text" : "password"} placeholder={placeholder}
                    autoComplete={newPassword ? "new-password" : "current-password"}
                    required minLength={newPassword ? 12 : undefined} maxLength={512}
                    aria-invalid={invalid || undefined}
                    aria-describedby={[hint && `${id}-hint`, invalid && "platform-login-error"].filter(Boolean).join(" ") || undefined} />
                <button className="platform-login-reveal" type="button" aria-label={`显示${label}`}
                    aria-pressed={visible} aria-controls={id} title={visible ? `隐藏${label}` : `显示${label}`}
                    onClick={() => setVisible(value => !value)}>
                    {visible ? <EyeOff size={18} aria-hidden="true" /> : <Eye size={18} aria-hidden="true" />}
                </button>
            </div>
            {hint && <p className="platform-login-hint" id={`${id}-hint`}>{hint}</p>}
        </div>
    );
}

export function Login({ mustChangePassword = false }: { mustChangePassword?: boolean }) {
    const [changing, setChanging] = useState(mustChangePassword);
    const [busy, setBusy] = useState(false);
    const [error, setError] = useState("");
    const [invalidField, setInvalidField] = useState("");
    const submitting = useRef(false);
    const formRef = useRef<HTMLFormElement>(null);
    const errorRef = useRef<HTMLDivElement>(null);
    const theme = useThemeStore(state => state.theme);
    const setTheme = useThemeStore(state => state.setTheme);

    useEffect(() => watchSystemTheme(), []);

    useEffect(() => {
        const previousTitle = document.title;
        document.title = `${changing ? "设置新密码" : "登录"} · ${INSTITUTE_NAME} · ${WORKBENCH_NAME}`;
        return () => { document.title = previousTitle; };
    }, [changing]);

    useEffect(() => {
        if (changing) formRef.current?.querySelector<HTMLInputElement>('input[name="oldPassword"]')?.focus();
    }, [changing]);

    useEffect(() => {
        if (!error) return;
        if (invalidField) formRef.current?.querySelector<HTMLInputElement>(`input[name="${invalidField}"]`)?.focus();
        else errorRef.current?.focus();
    }, [error, invalidField]);

    async function submit(event: FormEvent<HTMLFormElement>) {
        event.preventDefault();
        if (busy || submitting.current) return;
        const element = event.currentTarget;
        if (!element.reportValidity()) return;
        const form = new FormData(element);
        const password = String(form.get("password") ?? "");
        setError("");
        setInvalidField("");
        if (changing && (password.length < 12 || password !== form.get("confirm"))) {
            setInvalidField(password.length < 12 ? "password" : "confirm");
            setError(password.length < 12 ? "新密码至少需要 12 个字符。" : "两次输入的新密码不一致，请重新确认。");
            return;
        }
        // 同步锁覆盖 React 更新 busy 前的重复提交，表单同时禁用编辑。
        submitting.current = true;
        setBusy(true);
        let reloading = false;
        try {
            if (changing) {
                await changePlatformPassword(String(form.get("oldPassword")), password);
                reloading = true;
                window.location.reload();
            } else {
                const result = await platformLogin(String(form.get("username")), password);
                if (result.user.mustChangePassword) { setChanging(true); element.reset(); }
                else { reloading = true; window.location.reload(); }
            }
        } catch (cause) { setError(cause instanceof Error ? cause.message : "操作失败，请重试"); }
        finally {
            if (!reloading) { submitting.current = false; setBusy(false); }
        }
    }

    return (
        <main className="platform-login" data-theme={theme}>
            <header className="platform-login-masthead">
                <img className="platform-login-wordmark" src={instituteWordmark}
                    alt={`${INSTITUTE_NAME} / ${INSTITUTE_NAME_LATIN}`} width={432} height={168} />
                <div className="platform-login-header-actions">
                    <span className="platform-login-internal"><ShieldCheck size={16} aria-hidden="true" />研究院内部系统</span>
                    <button className="platform-login-theme" type="button" aria-label="深色模式" aria-pressed={theme === "dark"}
                        title={`切换为${theme === "dark" ? "浅色" : "深色"}模式`} onClick={() => setTheme(theme === "dark" ? "light" : "dark")}>
                        {theme === "dark" ? <Sun size={18} aria-hidden="true" /> : <Moon size={18} aria-hidden="true" />}
                    </button>
                </div>
            </header>
            <div className="platform-login-workspace">
                <section className="platform-login-brand" aria-label={WORKBENCH_NAME}>
                    <div className="platform-login-brand-copy">
                        <p className="platform-login-product"><span aria-hidden="true" />{WORKBENCH_NAME}</p>
                        <p className="platform-login-display">从一个想法，<br />到一部作品。</p>
                        <p className="platform-login-tagline">让灵感成形，让故事走上银幕。</p>
                    </div>
                    <div className="platform-login-landscape" aria-hidden="true">
                        <svg className="platform-login-ridge" viewBox={INSTITUTE_RIDGE_VIEW_BOX} focusable="false">
                            <path d={INSTITUTE_RIDGE_PATH} />
                        </svg>
                    </div>
                </section>
                <section className="platform-login-panel" aria-labelledby="platform-login-title">
                    <form ref={formRef} className="platform-login-card" onSubmit={submit} key={String(changing)} aria-busy={busy} aria-labelledby="platform-login-title">
                        <header className="platform-login-heading">
                            <div className="platform-login-heading-mark">
                                <img src={instituteIcon} alt="" width={254} height={252} />
                                <span>{changing ? "账号安全" : "欢迎回到创作空间"}</span>
                            </div>
                            <h1 id="platform-login-title">{changing ? "首次登录，设置新密码" : "登录工作台"}</h1>
                            <p>{changing ? "请先修改管理员提供的临时密码，完成后使用新密码重新登录。" : "使用长钢研究院为你开通的账号，继续创作。"}</p>
                        </header>
                        <fieldset disabled={busy}>
                            <legend className="platform-login-sr">{changing ? "设置新密码" : "登录信息"}</legend>
                            {changing
                                ? <PasswordField name="oldPassword" label="临时密码" placeholder="输入管理员提供的临时密码" />
                                : <div className="platform-login-field">
                                    <label htmlFor="platform-login-username">用户名</label>
                                    <input id="platform-login-username" name="username" autoComplete="username" autoCapitalize="none" spellCheck={false} placeholder="输入你的用户名" required maxLength={64} />
                                </div>}
                            <PasswordField name="password" label={changing ? "新密码" : "密码"} newPassword={changing}
                                placeholder={changing ? "设置新的登录密码" : "输入你的密码"} invalid={invalidField === "password"}
                                hint={changing ? "至少 12 个字符，建议组合字母、数字与符号。" : undefined} />
                            {changing && <PasswordField name="confirm" label="确认新密码" newPassword placeholder="再次输入新密码" invalid={invalidField === "confirm"} />}
                        </fieldset>
                        {error && <div ref={errorRef} id="platform-login-error" role="alert" tabIndex={-1} className="platform-login-error">
                            <AlertCircle size={18} aria-hidden="true" /><p>{error}</p>
                        </div>}
                        <button className="platform-login-submit" type="submit" disabled={busy}>
                            <span>{busy ? changing ? "正在保存密码…" : "正在验证账号…" : changing ? "保存新密码并重新登录" : "进入工作台"}</span>
                            {busy ? <LoaderCircle className="platform-login-spinner" size={18} aria-hidden="true" /> : <ArrowRight size={18} aria-hidden="true" />}
                        </button>
                        <div className="platform-login-access-note">
                            <LockKeyhole size={16} aria-hidden="true" />
                            <p>仅限研究院授权人员使用。<br />账号开通或密码重置，请联系管理员。</p>
                        </div>
                    </form>
                    <span className="platform-login-sr" role="status">{busy ? changing ? "正在保存密码，请稍候。" : "正在验证账号，请稍候。" : ""}</span>
                </section>
            </div>
            <footer className="platform-login-footer"><span>{INSTITUTE_NAME} · {WORKBENCH_NAME}</span><span>基于 BeefTV 开源项目构建</span></footer>
        </main>
    );
}

export function mountPlatformLogin(mustChangePassword = false) { createRoot(document.getElementById("root")!).render(<Login mustChangePassword={mustChangePassword} />); }
