<#import "template.ftl" as layout>

<@layout.registrationLayout displayInfo=true; section>
    <#if section = "header">
        <div class="login-header">
            <h1>Reset Password</h1>
            <p>Please enter your new password below to reset your credentials.</p>
        </div>
    <#elseif section = "form">
        <div id="kc-form">
            <div id="kc-form-wrapper">
                <form id="kc-passwd-update-form" onsubmit="if(!this.querySelector('#kc-login').disabled){this.querySelector('#kc-login').classList.add('loading'); return true;} return false;" action="${url.loginAction}" method="post">
                    
                    <#-- Username hidden field for browser autofill compatibility -->
                    <input type="text" id="username" name="username" value="${username!}" style="display:none;" autocomplete="username" />
                    <input type="password" id="password" name="password" style="display:none;" autocomplete="current-password" />

                    <div class="form-group">
                        <label for="password-new">New Password</label>
                        <div class="input-wrapper password-wrapper">
                            <svg fill="currentColor" viewBox="0 0 24 24"><path d="M18 8h-1V6c0-2.76-2.24-5-5-5S7 3.24 7 6v2H6c-1.1 0-2 .9-2 2v10c0 1.1.9 2 2 2h12c1.1 0 2-.9 2-2V10c0-1.1-.9-2-2-2zm-6 9c-1.1 0-2-.9-2-2s.9-2 2-2 2 .9 2 2-.9 2-2 2zm3.1-9H8.9V6c0-1.71 1.39-3.1 3.1-3.1 1.71 0 3.1 1.39 3.1 3.1v2z"/></svg>
                            <input tabindex="1" id="password-new" class="pf-c-form-control" name="password-new" type="password" autocomplete="new-password" autofocus placeholder="At least 8 characters" />
                            <button type="button" class="eye-toggle" onclick="togglePassword('password-new', this)" tabindex="-1" aria-label="Toggle password visibility">
                                <svg class="eye-icon eye-off" xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M17.94 17.94A10.07 10.07 0 0 1 12 20c-7 0-11-8-11-8a18.45 18.45 0 0 1 5.06-5.94"/><path d="M9.9 4.24A9.12 9.12 0 0 1 12 4c7 0 11 8 11 8a18.5 18.5 0 0 1-2.16 3.19"/><line x1="1" y1="1" x2="23" y2="23"/></svg>
                                <svg class="eye-icon eye-on" xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" style="display:none"><path d="M1 12s4-8 11-8 11 8 11 8-4 8-11 8-11-8-11-8z"/><circle cx="12" cy="12" r="3"/></svg>
                            </button>
                        </div>
                    </div>

                    <div class="form-group">
                        <label for="password-confirm">Confirm Password</label>
                        <div class="input-wrapper password-wrapper">
                            <svg fill="currentColor" viewBox="0 0 24 24"><path d="M18 8h-1V6c0-2.76-2.24-5-5-5S7 3.24 7 6v2H6c-1.1 0-2 .9-2 2v10c0 1.1.9 2 2 2h12c1.1 0 2-.9 2-2V10c0-1.1-.9-2-2-2zm-6 9c-1.1 0-2-.9-2-2s.9-2 2-2 2 .9 2 2-.9 2-2 2zm3.1-9H8.9V6c0-1.71 1.39-3.1 3.1-3.1 1.71 0 3.1 1.39 3.1 3.1v2z"/></svg>
                            <input tabindex="2" id="password-confirm" class="pf-c-form-control" name="password-confirm" type="password" autocomplete="new-password" placeholder="Confirm your password" />
                            <button type="button" class="eye-toggle" onclick="togglePassword('password-confirm', this)" tabindex="-1" aria-label="Toggle password visibility">
                                <svg class="eye-icon eye-off" xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M17.94 17.94A10.07 10.07 0 0 1 12 20c-7 0-11-8-11-8a18.45 18.45 0 0 1 5.06-5.94"/><path d="M9.9 4.24A9.12 9.12 0 0 1 12 4c7 0 11 8 11 8a18.5 18.5 0 0 1-2.16 3.19"/><line x1="1" y1="1" x2="23" y2="23"/></svg>
                                <svg class="eye-icon eye-on" xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" style="display:none"><path d="M1 12s4-8 11-8 11 8 11 8-4 8-11 8-11-8-11-8z"/><circle cx="12" cy="12" r="3"/></svg>
                            </button>
                        </div>
                    </div>

                    <div class="terms-group">
                        <input type="checkbox" id="logout-sessions" name="logout-sessions" value="on" checked>
                        <label for="logout-sessions">
                            Sign out from other devices
                        </label>
                    </div>

                    <div id="kc-form-buttons">
                        <#if isAppInitiatedAction??>
                            <div style="display: flex; gap: 10px;">
                                <button tabindex="3" class="pf-c-button pf-m-primary" name="login" id="kc-login" type="submit" disabled style="flex: 1; margin-top: 15px !important;">
                                    <span class="btn-text">Reset Password</span>
                                    <span class="btn-spinner"><svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><circle cx="12" cy="12" r="10" stroke-dasharray="31.42" stroke-dashoffset="10"><animateTransform attributeName="transform" type="rotate" from="0 12 12" to="360 12 12" dur="0.8s" repeatCount="indefinite"/></circle></svg></span>
                                </button>
                                <button tabindex="4" class="pf-c-button pf-m-secondary" name="cancel-aia" value="true" type="submit" style="flex: 1; background: #e5e7eb !important; color: #374151 !important; border: 1px solid #d1d5db !important; border-radius: 8px !important; padding: 10px !important; font-weight: 600 !important; font-size: 15px !important; cursor: pointer !important; margin-top: 15px !important;">
                                    Cancel
                                </button>
                            </div>
                        <#else>
                            <button tabindex="3" class="pf-c-button pf-m-primary" name="login" id="kc-login" type="submit" disabled>
                                <span class="btn-text">Reset Password</span>
                                <span class="btn-spinner"><svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><circle cx="12" cy="12" r="10" stroke-dasharray="31.42" stroke-dashoffset="10"><animateTransform attributeName="transform" type="rotate" from="0 12 12" to="360 12 12" dur="0.8s" repeatCount="indefinite"/></circle></svg></span>
                            </button>
                        </#if>
                    </div>
                </form>
            </div>
        </div>
        <script>
            (function() {
                var newPassword = document.getElementById('password-new');
                var confirmPassword = document.getElementById('password-confirm');
                var submitBtn = document.getElementById('kc-login');

                if (!newPassword || !confirmPassword || !submitBtn) return;

                function validateForm() {
                    var val1 = newPassword.value;
                    var val2 = confirmPassword.value;
                    submitBtn.disabled = !(val1 && val2 && val1 === val2 && val1.length >= 8);
                }

                newPassword.addEventListener('input', validateForm);
                confirmPassword.addEventListener('input', validateForm);
            })();
        </script>
    </#if>
</@layout.registrationLayout>
