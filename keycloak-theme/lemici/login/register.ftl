<#import "template.ftl" as layout>
<@layout.registrationLayout displayInfo=social.displayInfo; section>
    <#if section = "header">
        <div class="login-header">
            <h1>Create an Account</h1>
            <p>Join us and start your journey today.</p>
        </div>
    <#elseif section = "form">
        <form id="kc-register-form" action="${url.registrationAction}" method="post" onsubmit="var btn=document.getElementById('kc-login'); if(!btn.disabled){btn.classList.add('loading'); btn.disabled=true; return true;} return false;">
            
            <#-- Show Username ONLY if 'Email as Username' is OFF in Keycloak -->
            <#if !realm.registrationEmailAsUsername>
                <div class="form-group">
                    <label for="username">Username</label>
                    <div class="input-wrapper">
                        <svg fill="currentColor" viewBox="0 0 24 24"><path d="M12 12c2.21 0 4-1.79 4-4s-1.79-4-4-4-4 1.79-4 4 1.79 4 4 4zm0 2c-2.67 0-8 1.34-8 4v2h16v-2c0-2.66-5.33-4-8-4z"/></svg>
                        <input type="text" id="username" class="pf-c-form-control" name="username" value="${(register.formData.username!'')}" autocomplete="username" placeholder="Type username here" />
                    </div>
                </div>
            </#if>

            <#-- First Name -->
            <div class="form-group">
                <label for="firstName">First Name</label>
                <div class="input-wrapper">
                    <svg fill="currentColor" viewBox="0 0 24 24"><path d="M12 12c2.21 0 4-1.79 4-4s-1.79-4-4-4-4 1.79-4 4 1.79 4 4 4zm0 2c-2.67 0-8 1.34-8 4v2h16v-2c0-2.66-5.33-4-8-4z"/></svg>
                    <input type="text" id="firstName" class="pf-c-form-control" name="firstName" value="${(register.formData.firstName!'')}" placeholder="Type first name here" />
                </div>
            </div>

            <#-- Last Name -->
            <div class="form-group">
                <label for="lastName">Last Name</label>
                <div class="input-wrapper">
                    <svg fill="currentColor" viewBox="0 0 24 24"><path d="M12 12c2.21 0 4-1.79 4-4s-1.79-4-4-4-4 1.79-4 4 1.79 4 4 4zm0 2c-2.67 0-8 1.34-8 4v2h16v-2c0-2.66-5.33-4-8-4z"/></svg>
                    <input type="text" id="lastName" class="pf-c-form-control" name="lastName" value="${(register.formData.lastName!'')}" placeholder="Type last name here" />
                </div>
            </div>

            <#-- Email -->
            <div class="form-group">
                <label for="email">Email</label>
                <div class="input-wrapper">
                    <svg fill="currentColor" viewBox="0 0 24 24"><path d="M20 4H4c-1.1 0-1.99.9-1.99 2L2 18c0 1.1.9 2 2 2h16c1.1 0 2-.9 2-2V6c0-1.1-.9-2-2-2zm0 4l-8 5-8-5V6l8 5 8-5v2z"/></svg>
                    <input type="email" id="email" class="pf-c-form-control" name="email" value="${(register.formData.email!'')}" autocomplete="email" placeholder="Enter your email" />
                </div>
            </div>

            <#-- Password -->
            <#if passwordRequired??>
                <div class="form-group">
                    <label for="password">Password</label>
                    <div class="input-wrapper password-wrapper">
                        <svg fill="currentColor" viewBox="0 0 24 24"><path d="M18 8h-1V6c0-2.76-2.24-5-5-5S7 3.24 7 6v2H6c-1.1 0-2 .9-2 2v10c0 1.1.9 2 2 2h12c1.1 0 2-.9 2-2V10c0-1.1-.9-2-2-2zm-6 9c-1.1 0-2-.9-2-2s.9-2 2-2 2 .9 2 2-.9 2-2 2zm3.1-9H8.9V6c0-1.71 1.39-3.1 3.1-3.1 1.71 0 3.1 1.39 3.1 3.1v2z"/></svg>
                        <input type="password" id="password" class="pf-c-form-control" name="password" autocomplete="new-password" placeholder="At least 8 characters" />
                        <button type="button" class="eye-toggle" onclick="togglePassword('password', this)" tabindex="-1" aria-label="Toggle password visibility">
                            <svg class="eye-icon eye-off" xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M17.94 17.94A10.07 10.07 0 0 1 12 20c-7 0-11-8-11-8a18.45 18.45 0 0 1 5.06-5.94"/><path d="M9.9 4.24A9.12 9.12 0 0 1 12 4c7 0 11 8 11 8a18.5 18.5 0 0 1-2.16 3.19"/><line x1="1" y1="1" x2="23" y2="23"/></svg>
                            <svg class="eye-icon eye-on" xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" style="display:none"><path d="M1 12s4-8 11-8 11 8 11 8-4 8-11 8-11-8-11-8z"/><circle cx="12" cy="12" r="3"/></svg>
                        </button>
                    </div>
                    <div class="password-strength" id="password-strength">
                        <div class="strength-bar"><div class="strength-fill" id="strength-fill"></div></div>
                        <span class="strength-label" id="strength-label"></span>
                    </div>
                </div>

                <div class="form-group">
                    <label for="password-confirm">Confirm Password</label>
                    <div class="input-wrapper password-wrapper">
                        <svg fill="currentColor" viewBox="0 0 24 24"><path d="M18 8h-1V6c0-2.76-2.24-5-5-5S7 3.24 7 6v2H6c-1.1 0-2 .9-2 2v10c0 1.1.9 2 2 2h12c1.1 0 2-.9 2-2V10c0-1.1-.9-2-2-2zm-6 9c-1.1 0-2-.9-2-2s.9-2 2-2 2 .9 2 2-.9 2-2 2zm3.1-9H8.9V6c0-1.71 1.39-3.1 3.1-3.1 1.71 0 3.1 1.39 3.1 3.1v2z"/></svg>
                        <input type="password" id="password-confirm" class="pf-c-form-control" name="password-confirm" placeholder="Confirm your password" />
                        <button type="button" class="eye-toggle" onclick="togglePassword('password-confirm', this)" tabindex="-1" aria-label="Toggle confirm password visibility">
                            <svg class="eye-icon eye-off" xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M17.94 17.94A10.07 10.07 0 0 1 12 20c-7 0-11-8-11-8a18.45 18.45 0 0 1 5.06-5.94"/><path d="M9.9 4.24A9.12 9.12 0 0 1 12 4c7 0 11 8 11 8a18.5 18.5 0 0 1-2.16 3.19"/><line x1="1" y1="1" x2="23" y2="23"/></svg>
                            <svg class="eye-icon eye-on" xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" style="display:none"><path d="M1 12s4-8 11-8 11 8 11 8-4 8-11 8-11-8-11-8z"/><circle cx="12" cy="12" r="3"/></svg>
                        </button>
                    </div>
                    <div class="password-match" id="password-match"></div>
                </div>
            </#if>

            <div class="terms-group">
                <input type="checkbox" id="terms-agree" name="terms-agree" />
                <label for="terms-agree">
                    I agree to the <a href="${url.resourcesPath}/pages/terms.html" target="_blank">Terms of Service</a> and <a href="${url.resourcesPath}/pages/privacy.html" target="_blank">Privacy Policy</a>
                </label>
            </div>

            <div id="kc-form-buttons">
                <button class="pf-c-button pf-m-primary" type="submit" id="kc-login" disabled>
                    <span class="btn-text">Sign Up</span>
                    <span class="btn-spinner"><svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><circle cx="12" cy="12" r="10" stroke-dasharray="31.42" stroke-dashoffset="10"><animateTransform attributeName="transform" type="rotate" from="0 12 12" to="360 12 12" dur="0.8s" repeatCount="indefinite"/></circle></svg></span>
                </button>
            </div>
        </form>
        <script>
            (function() {
                var pwInput = document.getElementById('password');
                var confirmInput = document.getElementById('password-confirm');
                var strengthDiv = document.getElementById('password-strength');
                var strengthFill = document.getElementById('strength-fill');
                var strengthLabel = document.getElementById('strength-label');
                var matchDiv = document.getElementById('password-match');
                var termsCheckbox = document.getElementById('terms-agree');
                var submitBtn = document.getElementById('kc-login');
                var firstNameInput = document.getElementById('firstName');
                var lastNameInput = document.getElementById('lastName');
                var emailInput = document.getElementById('email');

                if (!pwInput || !confirmInput) return;

                function validateForm() {
                    var firstName = firstNameInput ? firstNameInput.value.trim() : '';
                    var lastName = lastNameInput ? lastNameInput.value.trim() : '';
                    var email = emailInput ? emailInput.value.trim() : '';
                    var password = pwInput.value;
                    var confirm = confirmInput.value;
                    var termsChecked = termsCheckbox ? termsCheckbox.checked : false;

                    var allFilled = firstName && lastName && email && password.length >= 8 && confirm;
                    var passwordsMatch = password === confirm;
                    var isValid = allFilled && passwordsMatch && termsChecked;

                    submitBtn.disabled = !isValid;
                }

                if (firstNameInput) firstNameInput.addEventListener('input', validateForm);
                if (lastNameInput) lastNameInput.addEventListener('input', validateForm);
                if (emailInput) emailInput.addEventListener('input', validateForm);
                if (termsCheckbox) termsCheckbox.addEventListener('change', validateForm);

                var levels = [
                    { label: 'Weak', color: '#dc2626', width: '20%' },
                    { label: 'Weak', color: '#dc2626', width: '40%' },
                    { label: 'Fair', color: '#f59e0b', width: '60%' },
                    { label: 'Good', color: '#3b82f6', width: '80%' },
                    { label: 'Strong', color: '#16a34a', width: '100%' }
                ];

                function scorePassword(pw) {
                    var s = 0;
                    if (pw.length >= 8) s++;
                    if (/[a-z]/.test(pw) && /[A-Z]/.test(pw)) s++;
                    if (/[0-9]/.test(pw)) s++;
                    if (/[^a-zA-Z0-9]/.test(pw)) s++;
                    if (pw.length >= 12) s++;
                    return s;
                }

                function updateStrength() {
                    var pw = pwInput.value;
                    if (!pw) {
                        strengthDiv.style.display = 'none';
                        return;
                    }
                    var s = Math.min(scorePassword(pw), 5);
                    var lvl = levels[s > 0 ? s - 1 : 0];
                    strengthDiv.style.display = 'block';
                    strengthFill.style.width = s === 0 ? '20%' : lvl.width;
                    strengthFill.style.backgroundColor = s === 0 ? '#e5e7eb' : lvl.color;
                    strengthLabel.textContent = s === 0 ? 'Too short' : lvl.label;
                    strengthLabel.style.color = s === 0 ? '#9ca3af' : lvl.color;
                }

                function updateMatch() {
                    var pw = pwInput.value;
                    var confirm = confirmInput.value;
                    if (!confirm) {
                        matchDiv.style.display = 'none';
                        matchDiv.className = 'password-match';
                        return;
                    }
                    matchDiv.style.display = 'block';
                    if (pw === confirm) {
                        matchDiv.className = 'password-match match';
                        matchDiv.textContent = 'Passwords match';
                    } else {
                        matchDiv.className = 'password-match no-match';
                        matchDiv.textContent = 'Passwords do not match';
                    }
                }

                pwInput.addEventListener('input', function() {
                    updateStrength();
                    updateMatch();
                    validateForm();
                });
                confirmInput.addEventListener('input', function() {
                    updateMatch();
                    validateForm();
                });
            })();
        </script>

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
                            <span>Continue with Google</span>
                        </a>
                    </li>
                </ul>
            </div>
            </#if></#list>
            </#if>

    <#elseif section = "info" >
        <div class="text-center mt-6" style="text-align:center; font-size: 14px; margin-top:20px; color:#6b7280;">
            Already have an account? <a href="${url.loginUrl}" style="color: #3b82f6; text-decoration: none;">Sign in</a>
        </div>
    </#if>
</@layout.registrationLayout>