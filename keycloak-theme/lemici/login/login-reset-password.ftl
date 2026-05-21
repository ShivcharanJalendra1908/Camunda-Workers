<#import "template.ftl" as layout>

<@layout.registrationLayout displayInfo=true; section>
    <#if section = "header">
        <div class="login-header">
            <h1>Forgot Password?</h1>
            <p>Enter your email address and we will send you a link to reset your password.</p>
        </div>
    <#elseif section = "form">
        <div id="kc-form">
            <div id="kc-form-wrapper">
                <form id="kc-reset-password-form" onsubmit="if(!this.querySelector('#kc-login').disabled){this.querySelector('#kc-login').classList.add('loading'); return true;} return false;" action="${url.loginAction}" method="post">
                    <div class="form-group">
                        <label for="username"><#if !realm.loginWithEmailAllowed>${msg("username")}<#elseif !realm.registrationEmailAsUsername>${msg("usernameOrEmail")}<#else>${msg("email")}</#if></label>
                        <div class="input-wrapper">
                            <svg fill="currentColor" viewBox="0 0 24 24"><path d="M20 4H4c-1.1 0-1.99.9-1.99 2L2 18c0 1.1.9 2 2 2h16c1.1 0 2-.9 2-2V6c0-1.1-.9-2-2-2zm0 4l-8 5-8-5V6l8 5 8-5v2z"/></svg>
                            <input tabindex="1" id="username" class="pf-c-form-control" name="username" value="${(auth.attemptedUsername!'')}" type="text" autofocus autocomplete="username" placeholder="Enter your email" />
                        </div>
                    </div>

                    <div id="kc-form-buttons">
                        <button tabindex="2" class="pf-c-button pf-m-primary" name="login" id="kc-login" type="submit" disabled>
                            <span class="btn-text">Send Reset Link</span>
                            <span class="btn-spinner"><svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><circle cx="12" cy="12" r="10" stroke-dasharray="31.42" stroke-dashoffset="10"><animateTransform attributeName="transform" type="rotate" from="0 12 12" to="360 12 12" dur="0.8s" repeatCount="indefinite"/></circle></svg></span>
                        </button>
                    </div>
                </form>
            </div>
        </div>
        <script>
            (function() {
                var usernameInput = document.getElementById('username');
                var submitBtn = document.getElementById('kc-login');

                if (!usernameInput || !submitBtn) return;

                function validateForm() {
                    submitBtn.disabled = !(usernameInput.value.trim());
                }

                usernameInput.addEventListener('input', validateForm);
                // Validate on load in case browser autofills
                setTimeout(validateForm, 100);
            })();
        </script>
    <#elseif section = "info" >
        <div class="text-center mt-6" style="text-align:center; font-size: 14px; margin-top:20px; color:#6b7280;">
            <a href="${url.loginUrl}" style="color: #6D3E93; text-decoration: none; font-weight: 500;">Back to Login</a>
        </div>
    </#if>
</@layout.registrationLayout>
