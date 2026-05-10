<#import "template.ftl" as layout>

<#-- ✅ Fix 1: loginTimeout flash suppress karo - user ko blank page dikhega briefly
     instead of Keycloak ka raw "loginTimeout" error banner -->
<#if message?has_content && message.type == "error">
<script>
(function() {
    if (window.location.pathname.indexOf('login-actions') !== -1) {
        document.documentElement.style.visibility = 'hidden';
        document.documentElement.style.backgroundColor = 'white';
    }
})();
</script>
</#if>

<@layout.registrationLayout displayInfo=social.displayInfo; section>
    <#if section = "header">
        <div class="login-header">
            <h1>Welcome!</h1>
            <p>Signing up is quick and easy. <br> Let's get started on something great.</p>
        </div>
    <#elseif section = "form">

        <#-- ✅ Fix 2: Cookie se signal padhke banner dikhao — sirf fresh login page pe -->
        <div id="session-timeout-banner" style="
            display: none;
            background: rgba(251,191,36,0.10);
            border: 1px solid rgba(251,191,36,0.35);
            border-radius: 10px;
            padding: 0.7rem 1rem;
            margin-bottom: 1.2rem;
            color: #fbbf24;
            font-size: 0.875rem;
            text-align: center;
        ">
            ⏱&nbsp; ${msg("reauthenticate")}
        </div>

        <script>
        (function () {
            if (window.location.pathname.indexOf('login-actions') !== -1) return;
            var hasCookie = document.cookie.split(';').some(function (c) {
                return c.trim() === 'session_timeout=true';
            });
            if (hasCookie) {
                document.getElementById('session-timeout-banner').style.display = 'block';
                document.cookie = 'session_timeout=; Max-Age=0; path=/; Secure; SameSite=Lax';
            }
        })();
        </script>

        <div id="kc-form">
            <div id="kc-form-wrapper">
                <#if realm.password>
                    <form id="kc-form-login" onsubmit="login.disabled = true; return true;" action="${url.loginAction}" method="post">
                        <div class="form-group">
                            <label for="username">Email</label>
                            <div class="input-wrapper">
                                <svg fill="currentColor" viewBox="0 0 24 24"><path d="M20 4H4c-1.1 0-1.99.9-1.99 2L2 18c0 1.1.9 2 2 2h16c1.1 0 2-.9 2-2V6c0-1.1-.9-2-2-2zm0 4l-8 5-8-5V6l8 5 8-5v2z"/></svg>
                                <input tabindex="1" id="username" class="pf-c-form-control" name="username" value="${(login.username?html!'')}"  type="text" autofocus autocomplete="off" placeholder="example@gmail.com" />
                            </div>
                        </div>

                        <div class="form-group">
                            <label for="password">Password</label>
                            <div class="input-wrapper password-wrapper">
                                <svg fill="currentColor" viewBox="0 0 24 24"><path d="M18 8h-1V6c0-2.76-2.24-5-5-5S7 3.24 7 6v2H6c-1.1 0-2 .9-2 2v10c0 1.1.9 2 2 2h12c1.1 0 2-.9 2-2V10c0-1.1-.9-2-2-2zm-6 9c-1.1 0-2-.9-2-2s.9-2 2-2 2 .9 2 2-.9 2-2 2zm3.1-9H8.9V6c0-1.71 1.39-3.1 3.1-3.1 1.71 0 3.1 1.39 3.1 3.1v2z"/></svg>
                                <input tabindex="2" id="password" class="pf-c-form-control" name="password" type="password" autocomplete="off" placeholder="At least 8 characters" />
                                <button type="button" class="eye-toggle" onclick="togglePassword('password', this)" tabindex="-1" aria-label="Toggle password visibility">
                                    <svg class="eye-icon eye-off" xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M17.94 17.94A10.07 10.07 0 0 1 12 20c-7 0-11-8-11-8a18.45 18.45 0 0 1 5.06-5.94"/><path d="M9.9 4.24A9.12 9.12 0 0 1 12 4c7 0 11 8 11 8a18.5 18.5 0 0 1-2.16 3.19"/><line x1="1" y1="1" x2="23" y2="23"/></svg>
                                    <svg class="eye-icon eye-on" xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" style="display:none"><path d="M1 12s4-8 11-8 11 8 11 8-4 8-11 8-11-8-11-8z"/><circle cx="12" cy="12" r="3"/></svg>
                                </button>
                            </div>
                            <#-- Forgot Password -->
                            <a tabindex="5" href="${url.loginResetPasswordUrl!'/realms/${realm.name}/login-actions/reset-credentials'}" class="forgot-password-link" style="position: relative; z-index: 999; cursor: pointer;">Forgot password?</a>
                        </div>

                        <div id="kc-form-buttons">
                            <input tabindex="4" class="pf-c-button pf-m-primary" name="login" id="kc-login" type="submit" value="Sign in"/>
                        </div>
                    </form>
                </#if>
            </div>

            <#if social.providers?? && social.providers?has_content>
            <#list social.providers as p><#if p.alias == "google">
            <div class="divider">
                <hr><span>OR</span><hr>
            </div>
            <div id="kc-social-providers">
                <ul>
                    <li>
                        <a href="${p.loginUrl}" class="social-btn google" title="Continue with Google">
                            <svg viewBox="0 0 24 24" width="20" height="20"><path d="M22.56 12.25c0-.78-.07-1.53-.2-2.25H12v4.26h5.92c-.26 1.37-1.04 2.53-2.21 3.31v2.77h3.57c2.08-1.92 3.28-4.74 3.28-8.09z" fill="#4285F4"/><path d="M12 23c2.97 0 5.46-.98 7.28-2.66l-3.57-2.77c-.98.66-2.23 1.06-3.71 1.06-2.86 0-5.29-1.93-6.16-4.53H2.18v2.84C3.99 20.53 7.7 23 12 23z" fill="#34A853"/><path d="M5.84 14.09c-.22-.66-.35-1.36-.35-2.09s.13-1.43.35-2.09V7.07H2.18C1.43 8.55 1 10.22 1 12s.43 3.45 1.18 4.93l2.85-2.23.81-.61z" fill="#FBBC05"/><path d="M12 5.38c1.62 0 3.06.56 4.21 1.66l3.15-3.15C17.45 2.09 14.97 1 12 1 7.7 1 3.99 3.47 2.18 7.07l3.66 2.84c.87-2.6 3.3-4.53 6.16-4.53z" fill="#EA4335"/></svg>
                        </a>
                    </li>
                </ul>
            </div>
            </#if></#list>
            </#if>
        </div>
    <#elseif section = "info" >
        <div class="text-center mt-6" style="text-align:center; font-size: 14px; margin-top:20px; color:#6b7280;">
            or <a href="${url.registrationUrl!'#'}" style="color: #3b82f6; text-decoration: none;">create an account</a> if you don't have one yet
        </div>
    </#if>
</@layout.registrationLayout> 