<#import "template.ftl" as layout>
<@layout.registrationLayout displayInfo=social.displayInfo; section>
    <#if section = "header">
        <div class="login-header">
            <h1>Create an Account</h1>
            <p>Join us and start your journey today.</p>
        </div>
    <#elseif section = "form">
        <form id="kc-register-form" action="${url.registrationAction}" method="post">
            
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
                    <input type="text" id="email" class="pf-c-form-control" name="email" value="${(register.formData.email!'')}" autocomplete="email" placeholder="example@gmail.com" />
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

            <div id="kc-form-buttons">
                <input class="pf-c-button pf-m-primary" type="submit" value="Sign Up" id="kc-login"/>
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

                if (!pwInput || !confirmInput) return;

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
                });
                confirmInput.addEventListener('input', updateMatch);
            })();
        </script>
    <#elseif section = "info" >
        <div class="text-center mt-6" style="text-align:center; font-size: 14px; margin-top:20px; color:#6b7280;">
            Already have an account? <a href="${url.loginUrl}" style="color: #3b82f6; text-decoration: none;">Sign in</a>
        </div>
    </#if>
</@layout.registrationLayout>